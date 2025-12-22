# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Local Storage Operator provides persistent volume management for local storage devices in OpenShift clusters. It consists of two main components:
- **local-storage-operator**: The main operator that manages LocalVolume and LocalVolumeSet resources
- **diskmaker**: A DaemonSet component that runs on nodes to discover and prepare local storage devices

## Architecture

### Core Components

- **API Layer** (`api/`): Contains Custom Resource Definitions (CRDs)
  - `v1/LocalVolume`: Manual device configuration for local storage
  - `v1alpha1/LocalVolumeSet`: Automatic device discovery and management
  - `v1alpha1/LocalVolumeDiscovery`: Device discovery configuration
  - `v1alpha1/LocalVolumeDiscoveryResult`: Discovery results

- **Controllers** (`pkg/controllers/`):
  - `localvolume/`: Manages LocalVolume resources and creates provisioner/diskmaker DaemonSets
  - `localvolumeset/`: Handles automatic device management with LocalVolumeSet
  - `localvolumediscovery/`: Discovers available devices on nodes
  - `nodedaemon/`: Common DaemonSet management utilities

- **Diskmaker** (`cmd/diskmaker-manager/`, `pkg/diskmaker/`): Node-level component that:
  - Discovers local storage devices
  - Creates symlinks for device access
  - Creates PersistentVolumes for discovered devices
  - Manages device lifecycle

### Key Workflows

1. **Manual Storage Management (LocalVolume)**:
   - User creates LocalVolume CR specifying device paths and storage classes
   - Operator creates diskmaker and provisioner DaemonSets
   - Diskmaker creates symlinks and PVs for specified devices

2. **Automatic Storage Management (LocalVolumeSet)**:
   - User creates LocalVolumeDiscovery to scan for available devices
   - Discovery process creates LocalVolumeDiscoveryResult with device inventory
   - User creates LocalVolumeSet with device filters (size, type, etc.)
   - Operator automatically manages matching devices

## Development Commands

### Building
```bash
# Build both operator and diskmaker binaries
make build

# Build individual components
make build-operator
make build-diskmaker
```

### Testing
```bash
# Run unit tests
make test

# Run E2E tests (requires cluster)
make test_e2e
# OR use the script directly:
./hack/test-e2e.sh
```

### Code Generation and Validation
```bash
# Generate manifests and code
make generate
make manifests

# Update metadata (bump OCP versions)
make metadata OCP_VERSION=4.20.0

# Verify code formatting and generated files
make verify
make fmt
make vet
```

### Container Images
```bash
# Build container images
make images

# Build all images and push to registry
make bundle REGISTRY=quay.io/username

# Build must-gather image
make must-gather

# Push images to registry
make push
```

### Local Development

To run the operator locally for development:

1. Install LSO via OLM/OperatorHub
2. Build the operator: `make build-operator`
3. Export required environment variables:
   ```bash
   export DISKMAKER_IMAGE=quay.io/openshift/origin-local-storage-diskmaker:latest
   export KUBE_RBAC_PROXY_IMAGE=quay.io/openshift/origin-kube-rbac-proxy:latest
   export PRIORITY_CLASS_NAME=openshift-user-critical
   export WATCH_NAMESPACE=openshift-local-storage
   ```
4. Scale down the remote operator: `oc scale --replicas=0 deployment.apps/local-storage-operator -n openshift-local-storage`
5. Run locally: `./_output/bin/local-storage-operator -kubeconfig=$KUBECONFIG`

## Key Files and Directories

- `Makefile`: Build targets and development commands
- `HACKING.md`: Detailed development and deployment instructions
- `config/manifests/stable/`: OLM bundle manifests and CRDs
- `config/samples/`: Example Custom Resource configurations
- `examples/`: Usage examples for different deployment scenarios
- `test/e2e/`: End-to-end test suites
- `hack/`: Build and verification scripts

## Testing

### Unit Tests
Located in `pkg/` alongside source code. Run with `make test`.

### E2E Tests
Located in `test/e2e/`. Tests cover:
- LocalVolume lifecycle management
- LocalVolumeSet automatic device management
- LocalVolumeDiscovery device detection

Set environment variables for E2E testing:
- `TEST_OPERATOR_NAMESPACE`: Namespace for testing (default: openshift-local-storage)
- `TEST_LOCAL_DISK`: Specific disk device for testing

### Framework
Test framework in `test/framework/` provides utilities for:
- Kubernetes client management
- Resource creation and cleanup
- Volume management operations

## Common Patterns

- Controllers use controller-runtime framework with reconciliation loops
- DaemonSets are managed through `pkg/controllers/nodedaemon/` utilities
- Device management uses filesystem interfaces for testability (`pkg/diskmaker/controllers/lv/fs_interface.go`)
- Metrics are exposed via Prometheus (`pkg/localmetrics/`)
- Event reporting follows Kubernetes patterns for user feedback

## Deployment

The operator supports deployment via:
- **OLM (Operator Lifecycle Manager)**: Recommended for production
- **Manual manifests**: For development and testing

See `docs/deploy-with-olm.md` for detailed deployment instructions.
