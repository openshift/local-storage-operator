package e2e

import (
	"context"
	goctx "context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/onsi/gomega"
	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	awsPurposeTag = "localvolumeset-functest"
)

// map from node to distribution of disks on that node.
// when executing, a node from the list of available nodes will be assigned one of these configurations and keys
// behaviour unverified for more than 15 disks per node.
// it
type nodeDisks struct {
	disks []disk
	node  corev1.Node
}

type disk struct {
	size int
	name string
	id   string
	path string
}

// discover devices on the node and associate available onces with nodeEnv entries
// might mutate nodeEnv, but the return value should be used
// not specific to any platform
func populateDeviceInfo(t *testing.T, ctx *framework.Context, nodeEnv []nodeDisks) []nodeDisks {
	f := framework.Global
	namespace, err := ctx.GetNamespace()
	if err != nil {
		t.Fatalf("error fetching namespace : %v", err)
	}
	matcher := gomega.NewGomegaWithT(t)
	gomega.SetDefaultEventuallyTimeout(time.Minute * 10)
	gomega.SetDefaultEventuallyPollingInterval(time.Second * 2)
	// discover device names and ids
	localVolumeDiscovery := &localv1alpha1.LocalVolumeDiscovery{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolumeDiscovery",
			APIVersion: localv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auto-discover-devices",
			Namespace: namespace,
		},
	}
	matcher.Eventually(func() error {
		t.Log("creating localvolumediscovery")
		return f.Client.Create(goctx.TODO(), localVolumeDiscovery, &framework.CleanupOptions{TestContext: ctx})
	}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "creating localvolumediscovery")

	// get discovery, and match disks on nodes with nodeEnv entries
	discoveryResults := &localv1alpha1.LocalVolumeDiscoveryResultList{}
	// returns false if not matched, and retries till timeout
	matcher.Eventually(func() bool {
		t.Log("fetching localvolumediscoveryresults")
		matcher.Eventually(func() error {
			return f.Client.List(goctx.TODO(), discoveryResults)
		}, time.Minute, time.Second*2).ShouldNot(gomega.HaveOccurred(), "fetching localvolumediscoveryresults")

		t.Log("matching localvolumediscoveryresults with LocalVolume device IDs")
		for nodeIndex, nodeEnvEntry := range nodeEnv {
			nodeFound := false // try to match result with expected node
			for _, result := range discoveryResults.Items {
				if nodeEnvEntry.node.Name == result.Spec.NodeName { // matched result with node
					t.Logf("found results for node: %q", nodeEnvEntry.node.Name)
					// ensure that one device doesn't get matched to two entries
					matchedPaths := sets.NewString()
					nodeFound = true
					for diskIndex, diskEntry := range nodeEnvEntry.disks {
						deviceFound := false // try to match result with expected disk
						for _, foundDevice := range result.Status.DiscoveredDevices {
							matchesSize := foundDevice.Size == int64(diskEntry.size)*common.GiB
							matchedPreviously := matchedPaths.Has(foundDevice.Path)
							available := foundDevice.Status.State == localv1alpha1.Available
							if !matchedPreviously && matchesSize && available { // matched result with disk
								deviceFound = true
								// save as already matched
								matchedPaths.Insert(foundDevice.Path)
								t.Logf("diskPath: %s", foundDevice.Path)
								nodeEnv[nodeIndex].disks[diskIndex].path = foundDevice.Path
								nodeEnv[nodeIndex].disks[diskIndex].name = filepath.Base(foundDevice.Path)
								if len(foundDevice.DeviceID) > 0 {
									nodeEnv[nodeIndex].disks[diskIndex].id = filepath.Base(foundDevice.DeviceID)
								}
								// device already found
								break
							}

						}
						if !deviceFound {
							t.Logf("no available device found on node %q with size %q", nodeEnvEntry.node.Name, diskEntry.size)
							// retry after interval
							return false
						}
					}
					// node already found
					break
				}
			}
			if !nodeFound {
				t.Logf("node %q not found in localvolumediscoveryresults", nodeEnvEntry.node.Name)
				// retry after interval
				return false
			}
		}

		return true

	}, time.Minute*10, time.Second*2).Should(gomega.BeTrue(), "matching localvolumediscoveryresults with LocalVolume device IDs")
	return nodeEnv

}

// this assumes that the device spaces /dev/sd[h-z] are available on the node
// do not provide more than 20 disksizes
// do not use more than once per node
func createAndAttachAWSVolumes(t *testing.T, ec2Client *ec2.EC2, ctx *framework.Context, namespace string, nodeEnv []nodeDisks) error {
	var err error

	for _, nodeEntry := range nodeEnv {
		node := nodeEntry.node
		if len(nodeEntry.disks) > 20 {
			return fmt.Errorf("can't provision more than 20 disks per node")
		}
		volumes := make([]*ec2.Volume, len(nodeEntry.disks))
		volumeLetters := []string{"g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}
		volumeIDs := make([]*string, 0)
		instanceID, _, zone, err := getAWSNodeInfo(node)
		if err != nil {
			return err
		}

		// create ec2 volumes
		for i, disk := range nodeEntry.disks {
			diskSize := disk.size
			diskName := fmt.Sprintf("sd%s", volumeLetters[i])
			createInput := &ec2.CreateVolumeInput{
				AvailabilityZone: aws.String(zone),
				Size:             aws.Int64(int64(diskSize)),
				VolumeType:       aws.String("gp2"),
				TagSpecifications: []*ec2.TagSpecification{
					{
						ResourceType: aws.String("volume"),
						Tags: []*ec2.Tag{
							{
								Key:   aws.String("Name"),
								Value: aws.String(diskName),
							},
							{
								Key:   aws.String("purpose"),
								Value: aws.String(awsPurposeTag),
							},
							{
								Key:   aws.String("chosen-instanceID"),
								Value: aws.String(instanceID),
							},
						},
					},
				},
			}
			volume, err := ec2Client.CreateVolume(createInput)
			if err != nil {
				return fmt.Errorf("expect to create AWS volume with input %v: %w", createInput, err)
			}
			t.Logf("creating volume: %q (%dGi)", *volume.VolumeId, *volume.Size)
			volumes[i] = volume
			volumeIDs = append(volumeIDs, volume.VolumeId)
		}
		// attach and poll for attachment to complete
		err = wait.Poll(time.Second*5, time.Minute*4, func() (bool, error) {
			describeVolumeInput := &ec2.DescribeVolumesInput{
				VolumeIds: volumeIDs,
			}
			describedVolumes, err := ec2Client.DescribeVolumes(describeVolumeInput)
			if err != nil {
				return false, err
			}
			allAttached := true
			for i, volume := range describedVolumes.Volumes {
				if *volume.State == ec2.VolumeStateInUse {
					t.Logf("volume attachment complete: %q (%dGi)", *volume.VolumeId, *volume.Size)
					continue
				}
				allAttached = false
				if *volume.State == ec2.VolumeStateAvailable {

					t.Logf("volume attachment starting: %q (%dGi)", *volume.VolumeId, *volume.Size)
					attachInput := &ec2.AttachVolumeInput{
						VolumeId:   volume.VolumeId,
						InstanceId: aws.String(instanceID),
						Device:     aws.String(fmt.Sprintf("/dev/sd%s", volumeLetters[i])),
					}
					_, err = ec2Client.AttachVolume(attachInput)
					if err != nil {
						return false, err
					}
				}
			}
			return allAttached, nil

		})
		if err != nil {
			return err
		}
	}

	return err
}

func cleanupAWSDisks(t *testing.T, ec2Client *ec2.EC2) error {
	volumes, err := getAWSTestVolumes(ec2Client)
	if err != nil {
		return fmt.Errorf("failed to list AWS volumes: %+v", err)
	}
	t.Log("using described volumes")
	for _, volume := range volumes {
		t.Logf("detatching AWS disks with volumeId: %q (%dGi)", *volume.VolumeId, *volume.Size)
		input := &ec2.DetachVolumeInput{VolumeId: volume.VolumeId}
		_, err := ec2Client.DetachVolume(input)
		if err != nil {
			t.Logf("detaching disk failed: %+v", err)
		}
	}
	err = wait.Poll(time.Second*2, time.Minute*5, func() (bool, error) {
		volumes, err := getAWSTestVolumes(ec2Client)
		if err != nil {
			return false, fmt.Errorf("failed to list AWS volumes: %+v", err)
		}
		allDeleted := true
		for _, volume := range volumes {
			if *volume.State != ec2.VolumeStateAvailable {
				allDeleted = false
				continue
			}
			t.Logf("deleting AWS disks with volumeId: %q (%dGi)", *volume.VolumeId, *volume.Size)
			input := &ec2.DeleteVolumeInput{VolumeId: volume.VolumeId}
			_, err := ec2Client.DeleteVolume(input)
			if err != nil {
				t.Logf("deleting disk failed: %+v", err)
				allDeleted = false
			}
		}
		return allDeleted, nil
	})
	if err != nil {

		return fmt.Errorf("AWS cleanup of disks: %+v", err)
	}
	return nil
}

// getAWSNodeInfo returns instanceID, region, zone, error
func getAWSNodeInfo(node corev1.Node) (string, string, string, error) {
	var instanceID, region, zone string
	// providerID looks like: aws:///us-east-2a/i-02d314dea14ed4efb
	if !strings.HasPrefix(node.Spec.ProviderID, "aws://") {
		return "", "", "", fmt.Errorf("not an aws based node")
	}
	split := strings.Split(node.Spec.ProviderID, "/")
	instanceID = split[len(split)-1]
	zone = split[len(split)-2]
	region = zone[:len(zone)-1]
	return instanceID, region, zone, nil
}

func getAWSTestVolumes(ec2Client *ec2.EC2) ([]*ec2.Volume, error) {
	output, err := ec2Client.DescribeVolumes(&ec2.DescribeVolumesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("tag:purpose"),
				Values: []*string{aws.String(awsPurposeTag)},
			},
		},
	})

	return output.Volumes, err

}

func getEC2Client(region string) (*ec2.EC2, error) {
	f := framework.Global
	// get AWS credentials
	awsCreds := &corev1.Secret{}
	secretName := types.NamespacedName{Name: "aws-creds", Namespace: "kube-system"}
	err := f.Client.Get(context.TODO(), secretName, awsCreds)
	if err != nil {
		return nil, err
	}
	// detect region
	// base64 decode
	id, found := awsCreds.Data["aws_access_key_id"]
	if !found {
		return nil, fmt.Errorf("cloud credential id not found")
	}
	key, found := awsCreds.Data["aws_secret_access_key"]
	if !found {
		return nil, fmt.Errorf("cloud credential key not found")
	}

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(string(id), string(key), ""),
	})
	if err != nil {
		return nil, err
	}

	// initialize client
	return ec2.New(sess), nil
}
