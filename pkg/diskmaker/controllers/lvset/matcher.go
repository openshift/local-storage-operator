package lvset

import (
	"fmt"
	"strings"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
	"github.com/openshift/local-storage-operator/pkg/internal"
	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// filter names:
	notReadOnly           = "notReadOnly"
	notRemovable          = "notRemovable"
	notSuspended          = "notSuspended"
	noFilesystemSignature = "noFilesystemSignature"
	// file access , can't mock test
	noChildren = "noChildren"
	// file access , can't mock test
	canOpenExclusively = "canOpenExclusively"

	// matchers names:
	inSizeRange              = "inSizeRange"
	inTypeList               = "inTypeList"
	inMechanicalPropertyList = "inMechanicalPropertyList"
	inVendorList             = "inVendorList"
	inModelList              = "inModelList"
)

// maps of function identifier (for logs) to filter function.
// These are passed the localv1alpha1.DeviceInclusionSpec to make testing easier,
// but they aren't expected to use it
// they verify that the device itself is good to use
var filterMap = map[string]func(internal.BlockDevice, *localv1alpha1.DeviceInclusionSpec) (bool, error){
	notReadOnly: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		readOnly, err := dev.GetReadOnly()
		return !readOnly, err
	},

	notRemovable: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		removable, err := dev.GetRemovable()
		return !removable, err
	},

	notSuspended: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		matched := dev.State != internal.StateSuspended
		return matched, nil
	},

	noFilesystemSignature: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		matched := dev.FSType == ""
		return matched, nil
	},
	noChildren: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		hasChildren, err := dev.HasChildren()
		return !hasChildren, err
	},
	canOpenExclusively: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		pathname, err := dev.GetDevPath()
		if err != nil {
			return false, fmt.Errorf("pathname: %q: %w", pathname, err)
		}
		fd, errno := unix.Open(pathname, unix.O_RDONLY|unix.O_EXCL, 0)
		// If the device is in use, open will return an invalid fd.
		// When this happens, it is expected that Close will fail and throw an error.
		defer unix.Close(fd)
		if errno == nil {
			// device not in use
			return true, nil
		} else if errno == unix.EBUSY {
			// device is in use
			return false, nil
		}
		// error during call to Open
		return false, fmt.Errorf("pathname: %q: %w", pathname, errno)

	},
}

// functions that match device by *localv1alpha1.DeviceInclusionSpec
var matcherMap = map[string]func(internal.BlockDevice, *localv1alpha1.DeviceInclusionSpec) (bool, error){

	inSizeRange: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		if spec == nil {
			return true, nil
		}
		matched := false
		quantity, err := resource.ParseQuantity(dev.Size)
		if err != nil {
			return false, fmt.Errorf("could not parse device size: %w", err)
		}
		greaterThanOrEqualToMin := true
		if spec.MinSize != nil {
			// quantity greater than min: -1
			// quantity equal to min: 0
			greaterThanOrEqualToMin = spec.MinSize.Cmp(quantity) <= 0
		}

		lessThanOrEqualToMax := true
		if spec.MaxSize != nil {
			// quantity less than max: 1
			// quantity equal to max: 0
			lessThanOrEqualToMax = spec.MaxSize.Cmp(quantity) >= 0
		}

		matched = greaterThanOrEqualToMin && lessThanOrEqualToMax
		return matched, nil
	},
	inTypeList: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		matched := false
		if spec == nil {
			return strings.ToLower(string(localv1alpha1.RawDisk)) == strings.ToLower(dev.Type), nil
		}
		if len(spec.DeviceTypes) < 1 {
			return strings.ToLower(string(localv1alpha1.RawDisk)) == strings.ToLower(dev.Type), nil
		}

		for _, deviceType := range spec.DeviceTypes {
			if strings.ToLower(string(deviceType)) == strings.ToLower(dev.Type) {
				matched = true
				break
			}
		}
		return matched, nil
	},
	inMechanicalPropertyList: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		if spec == nil {
			return true, nil
		}
		if len(spec.DeviceMechanicalProperties) == 0 {
			return true, nil
		}
		matched := false
		rotational, err := dev.GetRotational()
		if err != nil {
			return false, err
		}
		for _, prop := range spec.DeviceMechanicalProperties {
			matchedRotational := prop == localv1alpha1.Rotational && rotational
			matchedNonRotational := prop == localv1alpha1.NonRotational && !rotational
			if matchedRotational || matchedNonRotational {
				matched = true
				break
			}
		}
		return matched, nil
	},
	inVendorList: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		if spec == nil {
			return true, nil
		}
		if len(spec.Vendors) == 0 {
			return true, nil
		}
		matched := false
		for _, vendor := range spec.Vendors {
			if strings.Contains(strings.ToLower(dev.Vendor), strings.ToLower(vendor)) {
				matched = true
				break
			}
		}
		return matched, nil
	},

	inModelList: func(dev internal.BlockDevice, spec *localv1alpha1.DeviceInclusionSpec) (bool, error) {
		if spec == nil {
			return true, nil
		}
		if len(spec.Models) == 0 {
			return true, nil
		}
		matched := false
		for _, model := range spec.Models {
			if strings.Contains(strings.ToLower(dev.Model), strings.ToLower(model)) {
				matched = true
				break
			}
		}
		return matched, nil
	},
}
