package lvset

import (
	"fmt"
	"testing"

	"github.com/openshift/local-storage-operator/pkg/internal"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"

	localv1alpha1 "github.com/openshift/local-storage-operator/pkg/apis/local/v1alpha1"
)

const (
	Ki = 1024
	Mi = Ki * 1024
	Gi = Mi * 1024
)

// test filters

func TestNotReadOnly(t *testing.T) {
	matcherMap := filterMap
	matcher := notReadOnly
	results := []knownMatcherResult{
		// true, no error
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "0"},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: ""},
			expectMatch: true, expectErr: false,
		},
		// false, no error
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "1"},
			expectMatch: false, expectErr: false,
		},
		// true, err
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "-1"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "2"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "100"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{ReadOnly: "-100"},
			expectMatch: true, expectErr: true,
		},
	}
	assertAll(t, results)
}

func TestNotRemovable(t *testing.T) {
	matcherMap := filterMap
	matcher := notRemovable
	results := []knownMatcherResult{
		// true, no error
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "0"},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: ""},
			expectMatch: true, expectErr: false,
		},
		// false, no error
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "1"},
			expectMatch: false, expectErr: false,
		},
		// true, err
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "-1"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "2"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "100"},
			expectMatch: true, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Removable: "-100"},
			expectMatch: true, expectErr: true,
		},
	}
	assertAll(t, results)
}

func TestNotSuspended(t *testing.T) {
	matcherMap := filterMap
	matcher := notSuspended
	results := []knownMatcherResult{
		// true
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{State: "running"},
			expectMatch: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{State: "live"},
			expectMatch: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{State: ""},
			expectMatch: true,
		},
		// false
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{State: "suspended"},
			expectMatch: false,
		},
	}
	assertAll(t, results)
}

func TestNoFilesystemSignature(t *testing.T) {
	matcherMap := filterMap
	matcher := noFilesystemSignature
	results := []knownMatcherResult{
		// true
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{FSType: ""},
			expectMatch: true,
		},
		//false
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{FSType: "ext4"},
			expectMatch: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{FSType: "crypto_LUKS"},
			expectMatch: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{FSType: "swap"},
			expectMatch: false,
		},
	}
	assertAll(t, results)
}

// // test matchers

func TestInSizeRange(t *testing.T) {
	tenGi := resource.MustParse("10Gi")
	fiftyGi := resource.MustParse("50Gi")

	matcherMap := matcherMap
	matcher := inSizeRange
	results := []knownMatcherResult{
		// both specified
		// in size range lower limit
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 10*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: true, expectErr: false,
		},
		// in size range upper limit
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 50*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: true, expectErr: false,
		},
		// in size range, max not specified
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 40*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi},
			expectMatch: true, expectErr: false,
		},
		// in size range, min not specified
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 40*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MaxSize: &fiftyGi},
			expectMatch: true, expectErr: false,
		},
		// nothing specified
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 40*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 1000*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 10000*Ki)},
			spec:        &localv1alpha1.DeviceInclusionSpec{},
			expectMatch: true, expectErr: false,
		},
		// violate min
		// barely
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 9.99*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: false, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 1*Ki)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: false, expectErr: false,
		},
		// violate max
		// barely
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 50.1*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: false, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: fmt.Sprintf("%v", 1000*Gi)},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: false, expectErr: false,
		},
		// bad raw size
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Size: "foo"},
			spec:        &localv1alpha1.DeviceInclusionSpec{MinSize: &tenGi, MaxSize: &fiftyGi},
			expectMatch: false, expectErr: true,
		},
	}
	assertAll(t, results)
}

// func Testin size range(t *testing.T) {
// 	match, err := in size range()
// }
func TestInTypeList(t *testing.T) {
	matcherMap := matcherMap
	matcher := inTypeList
	results := []knownMatcherResult{
		// exact match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.RawDisk)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk}},
			expectMatch: true, expectErr: false,
		},
		// subset
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.RawDisk)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk, localv1alpha1.Partition}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Partition)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk, localv1alpha1.Partition}},
			expectMatch: true, expectErr: false,
		},
		// exact mismatch, fails
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Loop)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk}},
			expectMatch: false, expectErr: false,
		},
		// subset mismatch, fails
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Loop)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk, localv1alpha1.Partition}},
			expectMatch: false, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: "crypt"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{localv1alpha1.RawDisk, localv1alpha1.Partition}},
			expectMatch: false, expectErr: false,
		},
		// case mismatch, works anyway
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.RawDisk)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{"DISK"}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Partition)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{"DISK", "PART"}},
			expectMatch: true, expectErr: false,
		},
		// mispelling, fails
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.RawDisk)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{"diskfoo"}},
			expectMatch: false, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Partition)},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceTypes: []localv1alpha1.DeviceType{"foo", "BAR"}},
			expectMatch: false, expectErr: false,
		},
		// deviceType spec omitted, pass
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.RawDisk)},
			spec:        &localv1alpha1.DeviceInclusionSpec{},
			expectMatch: true, expectErr: false,
		},
		// deviceType spec omitted, fail
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Type: string(localv1alpha1.Partition)},
			spec:        &localv1alpha1.DeviceInclusionSpec{},
			expectMatch: false, expectErr: false,
		},
	}
	assertAll(t, results)
}

func TestInMechanicalPropertyList(t *testing.T) {
	matcherMap := matcherMap
	matcher := inMechanicalPropertyList
	results := []knownMatcherResult{
		// exact match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "0"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.NonRotational}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "1"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational}},
			expectMatch: true, expectErr: false,
		},
		// subset
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "0"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational, localv1alpha1.NonRotational}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "1"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational, localv1alpha1.NonRotational}},
			expectMatch: true, expectErr: false,
		},
		// exact mismatch, fails
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "0"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational}},
			expectMatch: false, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "1"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.NonRotational}},
			expectMatch: false, expectErr: false,
		},
		// bad parse
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "foo"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational, localv1alpha1.NonRotational}},
			expectMatch: false, expectErr: true,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Rotational: "-1"},
			spec:        &localv1alpha1.DeviceInclusionSpec{DeviceMechanicalProperties: []localv1alpha1.DeviceMechanicalProperty{localv1alpha1.Rotational, localv1alpha1.NonRotational}},
			expectMatch: false, expectErr: true,
		},
	}
	assertAll(t, results)
}

// func TestinVendorList(t *testing.T) {
// 	match, err := inVendorList()
// }

func TestInVendorList(t *testing.T) {
	matcherMap := matcherMap
	matcher := inVendorList
	results := []knownMatcherResult{
		// exact match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"ATA"}},
			expectMatch: true, expectErr: false,
		},
		// substring match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"AT"}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"TA"}},
			expectMatch: true, expectErr: false,
		},
		// different case match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"ata"}},
			expectMatch: true, expectErr: false,
		},
		// subset match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"asus", "ata"}},
			expectMatch: true, expectErr: false,
		},
		// mismatch
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"asus"}},
			expectMatch: false, expectErr: false,
		},
		// subset mismatch
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Vendor: "ATA"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Vendors: []string{"asus", "foo"}},
			expectMatch: false, expectErr: false,
		},
	}
	assertAll(t, results)
}

func TestInModelList(t *testing.T) {
	matcherMap := matcherMap
	matcher := inModelList
	results := []knownMatcherResult{
		// exact match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "SAMSUNG158"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"SAMSUNG158"}},
			expectMatch: true, expectErr: false,
		},
		// substring match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "SAMSUNG158"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"SAMSUNG"}},
			expectMatch: true, expectErr: false,
		},
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "SAMSUNG158"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"158"}},
			expectMatch: true, expectErr: false,
		},
		// different case match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "SAMSUNG158"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"samsung"}},
			expectMatch: true, expectErr: false,
		},
		// subset match
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "ASUS258"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"samsung", "asus"}},
			expectMatch: true, expectErr: false,
		},
		// mismatch
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "VIRTUAL"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"asus"}},
			expectMatch: false, expectErr: false,
		},
		// subset mismatch
		{
			matcherMap: matcherMap, matcher: matcher,
			dev:         internal.BlockDevice{Model: "SAMSUNG158"},
			spec:        &localv1alpha1.DeviceInclusionSpec{Models: []string{"bar", "foo"}},
			expectMatch: false, expectErr: false,
		},
	}
	assertAll(t, results)
}

// a known result for a particular filter that can be asserted
type knownMatcherResult struct {
	// should pass one of filterMap or matcherMap
	matcherMap  map[string]func(internal.BlockDevice, *localv1alpha1.DeviceInclusionSpec) (bool, error)
	matcher     string
	dev         internal.BlockDevice
	spec        *localv1alpha1.DeviceInclusionSpec
	expectMatch bool
	expectErr   bool
}

func assertAll(t *testing.T, results []knownMatcherResult) {
	for _, result := range results {
		result.assert(t)
	}
}

func (r *knownMatcherResult) assert(t *testing.T) {
	t.Logf("matcher name: %s, dev: %+v, deviceInclusionSpec: %+v", r.matcher, r.dev, r.spec)
	matcher, ok := r.matcherMap[r.matcher]
	assert.True(t, ok, "expected to find matcher in map", r.matcher)
	match, err := matcher(r.dev, r.spec)
	if r.expectErr {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
	}
	if r.expectMatch {
		assert.True(t, match)
	} else {
		assert.False(t, match)
	}
}
