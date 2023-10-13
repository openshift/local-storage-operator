//go:build linux
// +build linux

package util

import (
	"os/exec"
	"testing"
)

var _ VolumeHelper = &volumeHelper{}

type volumeHelper struct{}

func NewVolumeHelper() VolumeHelper {
	return &volumeHelper{}
}

func (h *volumeHelper) FormatAsExt4(t *testing.T, fname string) {
	cmd := exec.Command("mkfs.ext4", fname)
	err := cmd.Run()
	if err != nil {
		t.Fatalf("error formatting file: %v; command error: %v", fname, cmd.Err)
	}
}

func (h *volumeHelper) HasExt4(t *testing.T, fname string) bool {
	cmd := exec.Command("fsck.ext4", "-n", fname)
	err := cmd.Run()
	if err != nil {
		// Exit status 8 is returned if fname doesn't contain valid file system
		if exiterr, ok := err.(*exec.ExitError); ok {
			if exiterr.ExitCode() == 8 {
				return false
			}
			t.Fatalf("error checking file system: %v; fsck.ext4 returned: %v", fname, exiterr.ExitCode())
		}
		t.Fatalf("error checking file system: %v; command error: %v", fname, cmd.Err)
	}
	return true
}
