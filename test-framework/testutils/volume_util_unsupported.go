//go:build !linux
// +build !linux

package util

import "testing"

var _ VolumeHelper = &volumeHelper{}

type volumeHelper struct{}

func NewVolumeHelper() VolumeHelper {
	return &volumeHelper{}
}

func (h *volumeHelper) FormatAsExt4(t *testing.T, fname string) {
	panic("FormatAsExt4 is unsupported in this build")
}

func (h *volumeHelper) HasExt4(t *testing.T, fname string) bool {
	panic("HasExt4 is unsupported in this build")
}
