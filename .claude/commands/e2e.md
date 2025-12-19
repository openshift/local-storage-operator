Prepare a Dockerfile for running e2e tests and show the user how to run them.

This command accepts optional parameters after /e2e to specify which tests to run.
Usage: `/e2e` to run all tests, or `/e2e TestName` to run specific tests.

Steps:
1. Extract the Go version from go.mod
2. Create a Dockerfile.e2e that uses the golang image with that version
3. Create a test runner script that accepts test filter arguments
4. Show the user how to build and run the container with their kubeconfig mounted

The Dockerfile should:
- Use the Go version from go.mod as the base image
- Set /workspace as the working directory
- Copy the project files
- Have a flexible entrypoint that accepts test arguments

After creating the Dockerfile and script, display instructions to the user on how to:
- Build the image: `podman build -f Dockerfile.e2e -t local-storage-e2e .`
- Run all tests: `podman run --rm -v $KUBECONFIG:/root/.kube/config:ro -e KUBECONFIG=/root/.kube/config -e TEST_OPERATOR_NAMESPACE=openshift-local-storage -e TEST_WATCH_NAMESPACE=openshift-local-storage local-storage-e2e`
- Run specific test: `podman run --rm -v $KUBECONFIG:/root/.kube/config:ro -e KUBECONFIG=/root/.kube/config -e TEST_OPERATOR_NAMESPACE=openshift-local-storage -e TEST_WATCH_NAMESPACE=openshift-local-storage local-storage-e2e TestName`

Extract test name from the command parameters if provided.
