#!/bin/bash -x
set -e

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}

export TEST_WATCH_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_OPERATOR_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_LOCAL_DISK=${TEST_LOCAL_DISK:-""}

go test -timeout 0 ./test/e2e/... \
  -root=$(pwd) \
  -kubeconfig=${KUBECONFIG} \
  -v \
  -parallel=1 \
