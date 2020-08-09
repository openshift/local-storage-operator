# Using the must-gather local-storage image

The `must-gather` image provided by the local-storage operator is a supplement to the [must-gather](https://github.com/openshift/must-gather) image provided by OpenShift. This image
is used to gather the local-storage specific resources.

## Usage
```sh
oc adm must-gather --image=quay.io/openshift/local-must-gather:latest
```

This command creates a directory that contains:
- All local.storage.openshift.io resources across the cluster

## Building the image locally
The `Dockerfile.mustgather` can be used to build a local local-storage must-gather image. This file is referenced in the `Makefile`, and can be built using the following command:

```sh
make must-gather REGISTRY=my-repo/
```

The arguments for `make` are as follows:
- `REGISTRY`: Defines a custom registry for creating the must-gather image. If undefined, defaults to `quay.io/openshift/`.
