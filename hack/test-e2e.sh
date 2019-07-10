#!/bin/bash -x
set -e

ARTIFACT_DIR=${ARTIFACT_DIR:-_output}
manifest=$(mktemp)
global_manifest=$(mktemp)

# save generated manifest files in _output directory for debugging purposes
cleanup(){
  local return_code="$?"
  set +e

  cp ${manifest} $ARTIFACT_DIR/manifest
  cp ${global_manifest} $ARTIFACT_DIR/global_manifest
  rm -rf ${manifest}
  rm -rf ${global_manifest}
  exit $return_code
}

trap cleanup exit

if [ -n "${IMAGE_FORMAT}" ] ; then
    IMAGE_LOCAL_STORAGE_OPERATOR=$(sed -e "s,\${component},local-storage-operator," <(echo $IMAGE_FORMAT))
    IMAGE_LOCAL_DISKMAKER=$(sed -e "s,\${component},local-storage-diskmaker," <(echo $IMAGE_FORMAT))
else
    IMAGE_LOCAL_STORAGE_OPERATOR=${IMAGE_LOCAL_STORAGE_OPERATOR:-quay.io/openshift/origin-local-storage-operator}
    IMAGE_LOCAL_DISKMAKER=${IMAGE_LOCAL_DISKMAKER:-quay.io/openshift/origin-local-storage-diskmaker}
fi

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}
repo_dir="$(dirname $0)/.."
cat ${repo_dir}/deploy/sa.yaml >> ${manifest}
cat ${repo_dir}/deploy/rbac.yaml >> ${manifest}
cat ${repo_dir}/deploy/operator.yaml >> ${manifest}
cat ${repo_dir}/deploy/crd.yaml >> ${global_manifest}

sed -i "s,quay.io/openshift/origin-local-storage-operator,${IMAGE_LOCAL_STORAGE_OPERATOR}," ${manifest}
sed -i "s,quay.io/openshift/origin-local-storage-diskmaker,${IMAGE_LOCAL_DISKMAKER}," ${manifest}

TEST_NAMESPACE=${NAMESPACE} go test ./test/e2e/... \
  -root=$(pwd) \
  -kubeconfig=${KUBECONFIG} \
  -globalMan ${global_manifest} \
  -namespacedMan ${manifest} \
  -v \
  -parallel=1 \
  -singleNamespace
