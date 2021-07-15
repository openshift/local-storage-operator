This directory stores files used for deploying the operator in a plain k8s cluster without OLM.

If you are deploying this operator on a cluster managed by OLM then you should use files from /config/manifests directory.

## Deploying Operator on plain k8s

1. Run following commands assuming you are in "default" namespace:

```
~> kubectl create -f config/rbac/leader_election_role.yaml
~> kubectl create -f config/rbac/leader_election_role_binding.yaml
~> kubectl create -f config/rbac/diskmaker/role.yaml
~> kubectl create -f config/rbac/diskmaker/role_binding.yaml
~> kubectl create -f config/rbac/diskmaker/service_account.yaml
~> kubectl create -f config/rbac/role.yaml
~> kubectl create -f config/rbac/role_binding.yaml
~> kubectl create -f config/rbac/service_account.yaml
~> kubectl create -f config/manager/manager.yaml
~> kubectl create -f config/crd/bases/local.storage.openshift.io_localvolumes.yaml
~> kubectl create -f config/crd/bases/local.storage.openshift.io_localvolumesets.yaml
~> kubectl create -f config/crd/bases/local.storage.openshift.io_localvolumediscoveries.yaml
~> kubectl create -f config/crd/bases/local.storage.openshift.io_localvolumediscoveryresults.yaml

```

This should give you a running operator on a plain k8s cluster. You can now create Localvolumes, LocalvolumeSet present in `config/samples` directory.

For `LocalVolume` :
~> kubectl create -f config/samples/local_v1_localvolume.yaml
