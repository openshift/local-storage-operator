package common

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	symlinkDirVolName = "local-disks"

	devDirVolName = "device-dir"
	devDirPath    = "/dev"

	provisionerConfigVolName = "provisioner-config"

	udevVolName = "run-udev"
	udevPath    = "/run/udev"
)

var (
	hostContainerPropagation = corev1.MountPropagationHostToContainer
	directoryHostPath        = corev1.HostPathDirectory

	// SymlinkHostDirVolume is the corev1.Volume definition for the lso symlink host directory.
	// "/mnt/local-storage" is the default, but it can be controlled by env vars.
	// SymlinkMount is the corresponding mount
	SymlinkHostDirVolume = corev1.Volume{
		Name: "local-disks",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: GetLocalDiskLocationPath(),
			},
		},
	}
	// SymlinkMount is the corresponding mount for SymlinkHostDirVolume
	SymlinkMount = corev1.VolumeMount{
		Name:             "local-disks",
		MountPath:        GetLocalDiskLocationPath(),
		MountPropagation: &hostContainerPropagation,
	}

	// DevHostDirVolume  is the corev1.Volume definition for the "/dev" bind mount used to
	// list block devices.
	// DevMount is the corresponding mount
	DevHostDirVolume = corev1.Volume{
		Name: devDirVolName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: devDirPath,
				Type: &directoryHostPath,
			},
		},
	}
	// DevMount is the corresponding mount for DevHostDirVolume
	DevMount = corev1.VolumeMount{
		Name:             devDirVolName,
		MountPath:        devDirPath,
		MountPropagation: &hostContainerPropagation,
	}

	// ProvisionerConfigHostDirVolume is the corev1.Volume definition for the
	// local-static-provisioner configmap
	// ProvisionerConfigMount is the corresponding mount
	ProvisionerConfigHostDirVolume = corev1.Volume{
		Name: provisionerConfigVolName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ProvisionerConfigMapName,
				},
			},
		},
	}

	// ProvisionerConfigMount is the corresponding mount for ProvisionerConfigHostDirVolume
	ProvisionerConfigMount = corev1.VolumeMount{
		Name:      provisionerConfigVolName,
		ReadOnly:  true,
		MountPath: "/etc/provisioner/config",
	}

	// UDevHostDirVolume is the corev1.Volume definition for the
	// "/run/udev" host bind-mount. This helps lsblk give more accurate output.
	// UDevMount is the corresponding mount
	UDevHostDirVolume = corev1.Volume{
		Name: udevVolName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: udevPath},
		},
	}
	// UDevMount is the corresponding mount for UDevHostDirVolume
	UDevMount = corev1.VolumeMount{
		Name:             udevVolName,
		MountPath:        udevPath,
		MountPropagation: &hostContainerPropagation,
	}
)
