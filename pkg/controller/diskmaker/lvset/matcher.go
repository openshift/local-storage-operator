package lvset

import (
	"fmt"
	"strings"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/api/resource"
)

// var filters []diskFilter
// var matchers []diskMatcher

var filters = []diskFilter{
	{
		name: "notReadyOnly",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			matched := !dev.ReadOnly
			return matched, nil
		},
	},
	{
		name: "notRemovable",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			matched := !dev.Removable
			return matched, nil
		},
	},
	{
		name: "noChildren",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			matched := !dev.HasChildren()
			return matched, nil
		},
	},
	{
		name: "notSuspended",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			matched := dev.State != internal.StateSuspended
			return matched, nil
		},
	},
	{
		name: "canOpenExclusively",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			pathname, errno := dev.GetPathByID()
			fd, errno := unix.Open(pathname, unix.O_RDONLY|unix.O_EXCL, 0)
			// If the device is in use, open will return an invalid fd.
			// When this happens, it is expected that Close will fail and throw an error.
			defer unix.Close(fd)
			if errno == nil {
				// device not in use
				return false, nil
			} else if errno == unix.EBUSY {
				// device is in use
				return true, nil
			}
			// error during call to Open
			return false, errno

		},
	},
	{
		name: "noFilesystemSignature",
		matcher: func(dev internal.BlockDevice) (bool, error) {
			matched := dev.FSType == ""
			return matched, nil
		},
	},
}
var matchers = []diskMatcher{
	{
		name: "inSizeRange",
		matcher: func(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, error) {
			matched := false

			quantity, err := resource.ParseQuantity(dev.Size)
			if err != nil {
				return false, errors.Wrap(err, "could not parse device size")
			}

			greaterThanMin := spec.MinSize.Cmp(quantity) < 0
			lessThanMax := spec.MaxSize.Cmp(resource.MustParse(dev.Size)) > 0
			matched = greaterThanMin && (lessThanMax || spec.MaxSize.IsZero())
			return matched, nil
		},
	},
	{
		name: "inTypeList",
		matcher: func(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, error) {
			matched := false
			for _, deviceType := range spec.DeviceTypes {
				if string(deviceType) == dev.Type {
					matched = true
					break
				}
			}
			return matched, nil
		},
	},
	{
		name: "inVendorList",
		matcher: func(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, error) {
			matched := false
			for _, vendor := range spec.Vendors {
				if strings.Contains(dev.Vendor, vendor) {
					matched = true
					break
				}
			}
			return matched, nil
		},
	},
	{
		name: "inMechanicalPropertyList",
		matcher: func(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, error) {
			matched := false
			for _, prop := range spec.DeviceMechanicalProperties {
				matchedRotational := prop == localv1alpha1.Rotational && dev.Rotational
				matchedNonRotational := prop == localv1alpha1.NonRotational && !dev.Rotational
				if matchedRotational || matchedNonRotational {
					matched = true
					break
				}
			}
			return matched, nil
		},
	},
	{
		name: "inModelList",
		matcher: func(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, error) {
			matched := false
			for _, model := range spec.Models {
				if strings.Contains(dev.Model, model) {
					matched = true
					break
				}
			}
			return matched, nil
		},
	},
}

type diskFilter struct {
	name    string
	matcher func(internal.BlockDevice) (bool, error)
}

func (d diskFilter) isMatch(dev internal.BlockDevice) (bool, string, error) {
	matched, err := d.matcher(dev)
	msg := getMatcherMsg(matched, d.name)
	return matched, msg, err
}

type diskMatcher struct {
	name    string
	matcher func(internal.BlockDevice, localv1alpha1.DeviceInclusionSpec) (bool, error)
}

func (d diskMatcher) isMatch(dev internal.BlockDevice, spec localv1alpha1.DeviceInclusionSpec) (bool, string, error) {
	matched, err := d.matcher(dev, spec)
	msg := getMatcherMsg(matched, d.name)
	return matched, msg, err
}

func getMatcherMsg(matched bool, matcherName string) string {
	const (
		successMsg = "matched successfully"
		failureMsg = "did not match"
	)
	if matched {
		return fmt.Sprintf("%q %s", matcherName, successMsg)
	}

	return fmt.Sprintf("%q %s", matcherName, failureMsg)
}
