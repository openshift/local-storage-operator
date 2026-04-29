# AGENTS.md

This file provides guidance to LLM tools when working with this project

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

You should use `gopls MCP` server for finding,moving, editing around the code base. The instructions for `gopls MCP` server is below:

## The gopls MCP server

These instructions describe how to efficiently work in the Go programming language using the gopls MCP server. You can load this file directly into a session where the gopls MCP server is connected.

### Detecting a Go workspace

At the start of every session, you MUST use the `go_workspace` tool to learn about the Go workspace. ONLY if you are in a Go workspace, you MUST run `go_vulncheck` immediately afterwards to identify any existing security risks. The rest of these instructions apply whenever that tool indicates that the user is in a Go workspace.

### Go programming workflows

These guidelines MUST be followed whenever working in a Go workspace. There are two workflows described below: the 'Read Workflow' must be followed when the user asks a question about a Go workspace. The 'Edit Workflow' must be followed when the user edits a Go workspace.

You may re-do parts of each workflow as necessary to recover from errors. However, you must not skip any steps.

#### Read workflow

The goal of the read workflow is to understand the codebase.

1. **Understand the workspace layout**: Start by using `go_workspace` to understand the overall structure of the workspace, such as whether it's a module, a workspace, or a GOPATH project.

2. **Find relevant symbols**: If you're looking for a specific type, function, or variable, use `go_search`. This is a fuzzy search that will help you locate symbols even if you don't know the exact name or location.
   EXAMPLE: search for the 'Server' type: `go_search({"query":"server"})`

3. **Understand a file and its intra-package dependencies**: When you have a file path and want to understand its contents and how it connects to other files *in the same package*, use `go_file_context`. This tool will show you a summary of the declarations from other files in the same package that are used by the current file. `go_file_context` MUST be used immediately after reading any Go file for the first time, and MAY be re-used if dependencies have changed.
   EXAMPLE: to understand `server.go`'s dependencies on other files in its package: `go_file_context({"file":"/path/to/server.go"})`

4. **Understand a package's public API**: When you need to understand what a package provides to external code (i.e., its public API), use `go_package_api`. This is especially useful for understanding third-party dependencies or other packages in the same monorepo.
   EXAMPLE: to see the API of the `storage` package: `go_package_api({"packagePaths":["example.com/internal/storage"]})`

#### Editing workflow

The editing workflow is iterative. You should cycle through these steps until the task is complete.

1. **Read first**: Before making any edits, follow the Read Workflow to understand the user's request and the relevant code.

2. **Find references**: Before modifying the definition of any symbol, use the `go_symbol_references` tool to find all references to that identifier. This is critical for understanding the impact of your change. Read the files containing references to evaluate if any further edits are required.
   EXAMPLE: `go_symbol_references({"file":"/path/to/server.go","symbol":"Server.Run"})`

3. **Make edits**: Make the required edits, including edits to references you identified in the previous step. Don't proceed to the next step until all planned edits are complete.

4. **Check for errors**: After every code modification, you MUST call the `go_diagnostics` tool. Pass the paths of the files you have edited. This tool will report any build or analysis errors.
   EXAMPLE: `go_diagnostics({"files":["/path/to/server.go"]})`

5. **Fix errors**: If `go_diagnostics` reports any errors, fix them. The tool may provide suggested quick fixes in the form of diffs. You should review these diffs and apply them if they are correct. Once you've applied a fix, re-run `go_diagnostics` to confirm that the issue is resolved. It is OK to ignore 'hint' or 'info' diagnostics if they are not relevant to the current task. Note that Go diagnostic messages may contain a summary of the source code, which may not match its exact text.

6. **Check for vulnerabilities**: If your edits involved adding or updating dependencies in the go.mod file, you MUST run a vulnerability check on the entire workspace. This ensures that the new dependencies do not introduce any security risks. This step should be performed after all build errors are resolved. EXAMPLE: `go_vulncheck({"pattern":"./..."})`

7. **Run tests**: Once `go_diagnostics` reports no errors (and ONLY once there are no errors), run the tests for the packages you have changed. You can do this with `go test [packagePath...]`. Don't run `go test ./...` unless the user explicitly requests it, as doing so may slow down the iteration loop.

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

## Code Formatting

After editing any Go file, always run formatting and import fixes before finishing:
```bash
gopls format -w <file1> <file2> ...
# gopls imports only accepts one file at a time
gopls imports -w <file>
```

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
