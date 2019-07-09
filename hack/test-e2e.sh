#!/bin/bash -x
set -e

ARTIFACT_DIR=${ARTIFACT_DIR:-_output}
manifest=$(mktemp)
global_manifest=$(mktemp)

cleanup(){
  local return_code="$?"
  set +e

  cat $manifest > $ARTIFACT_DIR/manifest
  cat $global_manifest > $ARTIFACT_DIR/global_manifest

  exit $return_code
}
trap cleanup exit

if [ -n "${IMAGE_FORMAT:-}" ] ; then
  IMAGE_LOCAL_STORAGE_OPERATOR=$(sed -e "s,\${component},local-storage-operator," <(echo $IMAGE_FORMAT))
else
  IMAGE_LOCAL_STORAGE_OPERATOR=${IMAGE_LOCAL_STORAGE_OPERATOR:-quay.io/openshift/origin-local-storage-operator}
fi

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}
repo_dir="$(dirname $0)/.."
cat ${repo_dir}/deploy/sa.yaml >> ${manifest}
cat ${repo_dir}/deploy/rbac.yaml >> ${manifest}
cat ${repo_dir}/deploy/operator.yaml >> ${manifest}
cat ${repo_dir}/deploy/crd.yaml >> ${global_manifest}

sed -i "s,quay.io/openshift/origin-local-storage-operator,${IMAGE_LOCAL_STORAGE_OPERATOR}," ${manifest}

TEST_NAMESPACE=${NAMESPACE} go test ./test/e2e/... \
  -root=$(pwd) \
  -kubeconfig=${KUBECONFIG} \
  -globalMan ${global_manifest} \
  -namespacedMan ${manifest} \
  -v \
  -parallel=1 \
  -singleNamespace
