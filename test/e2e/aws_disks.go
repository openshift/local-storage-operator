package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	localv1alpha1 "github.com/openshift/local-storage-operator/api/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/common"
	framework "github.com/openshift/local-storage-operator/test/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	awsPurposeTag = "localvolumeset-functest"
)

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

func populateDeviceInfo(namespace string, nodeEnv []nodeDisks) []nodeDisks {
	f := framework.Global
	localVolumeDiscovery := &localv1alpha1.LocalVolumeDiscovery{
		TypeMeta: metav1.TypeMeta{
			Kind:       "LocalVolumeDiscovery",
			APIVersion: localv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auto-discover-devices",
			Namespace: namespace,
		},
	}
	Eventually(func() error {
		f.Logf("creating localvolumediscovery")
		err := f.Client.Create(context.TODO(), localVolumeDiscovery, nil)
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "creating localvolumediscovery")

	DeferCleanup(func() {
		eventuallyDelete(localVolumeDiscovery)
	})

	discoveryResults := &localv1alpha1.LocalVolumeDiscoveryResultList{}
	Eventually(func(ctx context.Context) bool {
		f.Logf("fetching localvolumediscoveryresults")
		Eventually(func(listCtx context.Context) error {
			return f.Client.List(listCtx, discoveryResults)
		}, time.Minute, time.Second*2).ShouldNot(HaveOccurred(), "fetching localvolumediscoveryresults")

		f.Logf("matching localvolumediscoveryresults with LocalVolume device IDs")
		for nodeIndex, nodeEnvEntry := range nodeEnv {
			nodeFound := false
			for _, result := range discoveryResults.Items {
				if nodeEnvEntry.node.Name == result.Spec.NodeName {
					f.Logf("found results for node: %q", nodeEnvEntry.node.Name)
					matchedPaths := sets.NewString()
					nodeFound = true
					for diskIndex, diskEntry := range nodeEnvEntry.disks {
						deviceFound := false
						f.Logf("looking for disk with size %d on node %s", diskEntry.size, nodeEnvEntry.node.Name)
						for _, foundDevice := range result.Status.DiscoveredDevices {
							matchesSize := foundDevice.Size == int64(diskEntry.size)*common.GiB
							matchedPreviously := matchedPaths.Has(foundDevice.Path)
							available := foundDevice.Status.State == localv1alpha1.Available
							if !matchedPreviously && matchesSize && available {
								deviceFound = true
								matchedPaths.Insert(foundDevice.Path)
								f.Logf("diskPath: %s", foundDevice.Path)
								nodeEnv[nodeIndex].disks[diskIndex].path = foundDevice.Path
								nodeEnv[nodeIndex].disks[diskIndex].name = filepath.Base(foundDevice.Path)
								if len(foundDevice.DeviceID) > 0 {
									nodeEnv[nodeIndex].disks[diskIndex].id = filepath.Base(foundDevice.DeviceID)
								}
								break
							}
						}
						if !deviceFound {
							f.Logf("no available device found on node %q with size %d", nodeEnvEntry.node.Name, diskEntry.size)
							return false
						}
					}
					break
				}
			}
			if !nodeFound {
				f.Logf("node %q not found in localvolumediscoveryresults", nodeEnvEntry.node.Name)
				return false
			}
		}

		return true

	}, time.Minute*10, time.Second*2).Should(BeTrue(), "matching localvolumediscoveryresults with LocalVolume device IDs")
	return nodeEnv

}

func createAndAttachAWSVolumes(ec2Client *ec2.Client, namespace string, nodeEnv []nodeDisks) {
	var wg sync.WaitGroup
	errs := make(chan error, len(nodeEnv))
	for _, nodeEntry := range nodeEnv {
		wg.Add(1)
		go func(entry nodeDisks) {
			defer wg.Done()
			defer GinkgoRecover()
			if err := createAndAttachAWSVolumesForNode(entry, ec2Client); err != nil {
				errs <- err
			}
		}(nodeEntry)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		Fail(err.Error())
	}
}

func createAndAttachAWSVolumesForNode(nodeEntry nodeDisks, ec2Client *ec2.Client) error {
	f := framework.Global
	node := nodeEntry.node
	if len(nodeEntry.disks) > 20 {
		return fmt.Errorf("can't provision more than 20 disks per node %q", nodeEntry.node.Name)
	}
	volumeLetters := []string{"g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z"}
	volumeIDs := make([]string, 0)
	instanceID, _, zone, err := getAWSNodeInfo(node)
	if err != nil {
		return fmt.Errorf("failed to get AWS node info for node %q: %+v", nodeEntry.node.Name, err)
	}

	for i, disk := range nodeEntry.disks {
		diskSize := disk.size
		diskName := fmt.Sprintf("sd%s", volumeLetters[i])
		createInput := &ec2.CreateVolumeInput{
			AvailabilityZone: aws.String(zone),
			Size:             aws.Int32(int32(diskSize)),
			VolumeType:       "gp2",
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: "volume",
					Tags: []ec2types.Tag{
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
		volume, err := ec2Client.CreateVolume(context.TODO(), createInput)
		if err != nil {
			return fmt.Errorf("failed to create AWS volume for node %q: %+v", nodeEntry.node.Name, err)
		}
		f.Logf("creating volume: %q (%dGi)", *volume.VolumeId, *volume.Size)
		volumeIDs = append(volumeIDs, *volume.VolumeId)
	}
	err = wait.Poll(time.Second*5, time.Minute*4, func() (bool, error) {
		describeVolumeInput := &ec2.DescribeVolumesInput{
			VolumeIds: volumeIDs,
		}
		describedVolumes, err := ec2Client.DescribeVolumes(context.TODO(), describeVolumeInput)
		if err != nil {
			return false, err
		}
		allAttached := true
		for i, volume := range describedVolumes.Volumes {
			if volume.State == ec2types.VolumeStateInUse {
				f.Logf("volume attachment complete: %q (%dGi)", *volume.VolumeId, *volume.Size)
				continue
			}
			allAttached = false
			if volume.State == ec2types.VolumeStateAvailable {

				f.Logf("volume attachment starting: %q (%dGi)", *volume.VolumeId, *volume.Size)
				attachInput := &ec2.AttachVolumeInput{
					VolumeId:   volume.VolumeId,
					InstanceId: aws.String(instanceID),
					Device:     aws.String(fmt.Sprintf("/dev/sd%s", volumeLetters[i])),
				}
				_, err = ec2Client.AttachVolume(context.TODO(), attachInput)
				if err != nil {
					return false, err
				}
			}
		}
		return allAttached, nil

	})
	if err != nil {
		return fmt.Errorf("failed to create and attach aws disks for node %q: %+v", nodeEntry.node.Name, err)
	}
	return nil
}

func cleanupAWSDisks(ec2Client *ec2.Client) error {
	f := framework.Global
	volumes, err := getAWSTestVolumes(ec2Client)
	if err != nil {
		return fmt.Errorf("failed to list AWS volumes: %+v", err)
	}
	f.Logf("using described volumes")
	for _, volume := range volumes {
		f.Logf("detatching AWS disks with volumeId: %q (%dGi)", *volume.VolumeId, *volume.Size)
		input := &ec2.DetachVolumeInput{VolumeId: volume.VolumeId}
		_, err := ec2Client.DetachVolume(context.TODO(), input)
		if err != nil {
			f.Logf("detaching disk failed: %+v", err)
		}
	}
	err = wait.Poll(time.Second*2, time.Minute*5, func() (bool, error) {
		volumes, err := getAWSTestVolumes(ec2Client)
		if err != nil {
			return false, fmt.Errorf("failed to list AWS volumes: %+v", err)
		}
		allDeleted := true
		for _, volume := range volumes {
			if volume.State != ec2types.VolumeStateAvailable {
				f.Logf("volume %q is in state %q, waiting for state %q", *volume.VolumeId, volume.State, ec2types.VolumeStateAvailable)
				allDeleted = false
				continue
			}
			f.Logf("deleting AWS disks with volumeId: %q (%dGi)", *volume.VolumeId, *volume.Size)
			input := &ec2.DeleteVolumeInput{VolumeId: volume.VolumeId}
			_, err := ec2Client.DeleteVolume(context.TODO(), input)
			if err != nil {
				f.Logf("deleting disk failed: %+v", err)
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

func getAWSNodeInfo(node corev1.Node) (string, string, string, error) {
	var instanceID, region, zone string
	if !strings.HasPrefix(node.Spec.ProviderID, "aws://") {
		return "", "", "", fmt.Errorf("not an aws based node")
	}
	split := strings.Split(node.Spec.ProviderID, "/")
	instanceID = split[len(split)-1]
	zone = split[len(split)-2]
	region = zone[:len(zone)-1]
	return instanceID, region, zone, nil
}

func getAWSTestVolumes(ec2Client *ec2.Client) ([]ec2types.Volume, error) {
	output, err := ec2Client.DescribeVolumes(context.TODO(), &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:purpose"),
				Values: []string{awsPurposeTag},
			},
		},
	})

	return output.Volumes, err

}

func getEC2Client(region string) (*ec2.Client, error) {
	f := framework.Global
	awsCreds := &corev1.Secret{}
	secretName := types.NamespacedName{Name: "aws-creds", Namespace: "kube-system"}
	err := f.Client.Get(context.TODO(), secretName, awsCreds)
	if err != nil {
		return nil, err
	}
	id, found := awsCreds.Data["aws_access_key_id"]
	if !found {
		return nil, fmt.Errorf("cloud credential id not found")
	}
	key, found := awsCreds.Data["aws_secret_access_key"]
	if !found {
		return nil, fmt.Errorf("cloud credential key not found")
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(string(id), string(key), "")),
	)
	if err != nil {
		return nil, err
	}

	return ec2.NewFromConfig(cfg), nil
}
