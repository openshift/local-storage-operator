#!/bin/bash
set -e

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}

export TEST_WATCH_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_OPERATOR_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_LOCAL_DISK=${TEST_LOCAL_DISK:-""}

# If arguments are provided, use them as test filter
if [ $# -gt 0 ]; then
  TEST_FILTER="-run=$1"
  echo "Running tests matching: $1"
else
  TEST_FILTER=""
  echo "Running all e2e tests"
fi

go test -timeout 0 ./test/e2e/... \
  -root=$(pwd) \
  -kubeconfig=${KUBECONFIG} \
  -v \
  -parallel=1 \
  ${TEST_FILTER}
