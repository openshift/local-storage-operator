#!/bin/bash -x
set -e

ARTIFACT_DIR=${ARTIFACT_DIR:-_output}
manifest=${ARTIFACT_DIR}/manifest.yaml
global_manifest=${ARTIFACT_DIR}/global_manifest.yaml
rm -f $manifest $global_manifest
mkdir -p ${ARTIFACT_DIR}

if [ -n "${IMAGE_FORMAT}" ] ; then
    echo "IMAGE_FORMAT set as '${IMAGE_FORMAT}'"
    IMAGE_LOCAL_STORAGE_OPERATOR=$(sed -e "s,\${component},local-storage-operator," <(echo $IMAGE_FORMAT))
    IMAGE_LOCAL_DISKMAKER=$(sed -e "s,\${component},local-storage-diskmaker," <(echo $IMAGE_FORMAT))
else
    IMAGE_LOCAL_STORAGE_OPERATOR=${IMAGE_LOCAL_STORAGE_OPERATOR:-quay.io/openshift/origin-local-storage-operator}
    IMAGE_LOCAL_DISKMAKER=${IMAGE_LOCAL_DISKMAKER:-quay.io/openshift/origin-local-storage-diskmaker}
fi

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}
repo_dir="$(dirname $0)/.."
cat ${repo_dir}/config/rbac/leader_election_role.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/leader_election_role_binding.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/diskmaker/role.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/diskmaker/role_binding.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/diskmaker/service_account.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/role.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/role_binding.yaml >> ${manifest}
cat ${repo_dir}/config/rbac/service_account.yaml >> ${manifest}
cat ${repo_dir}/config/manager/manager.yaml >> ${manifest}
#cat ${repo_dir}/deploy/operator.yaml >> ${manifest}
cat ${repo_dir}/config/crd/bases/local.storage.openshift.io_localvolumes.yaml >> ${global_manifest}
cat ${repo_dir}/config/crd/bases/local.storage.openshift.io_localvolumesets.yaml >> ${global_manifest}
cat ${repo_dir}/config/crd/bases/local.storage.openshift.io_localvolumediscoveries.yaml >> ${global_manifest}
cat ${repo_dir}/config/crd/bases/local.storage.openshift.io_localvolumediscoveryresults.yaml >> ${global_manifest}

sed -i "s,quay.io/openshift/origin-local-storage-operator,${IMAGE_LOCAL_STORAGE_OPERATOR}," ${manifest}
sed -i "s,quay.io/openshift/origin-local-storage-diskmaker,${IMAGE_LOCAL_DISKMAKER}," ${manifest}
export TEST_WATCH_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-default}
export TEST_OPERATOR_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-default}
export TEST_LOCAL_DISK=${TEST_LOCAL_DISK:-""}

export \
    IMAGE_LOCAL_STORAGE_OPERATOR \
    IMAGE_LOCAL_DISKMAKER

go test -timeout 0 ./test/e2e/... \
  -root=$(pwd) \
  -kubeconfig=${KUBECONFIG} \
  -globalMan ${global_manifest} \
  -namespacedMan ${manifest} \
  -v \
  -parallel=1 \
  #-singleNamespace
