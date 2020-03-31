
This directory stores files used for deploying the operator in a plain k8s cluster without OLM.

If you are deploying this operator on a cluster managed by OLM then you should use files from /manifests directory.

## Deploying Operator on plain k8s

1. Run following commands assuming you are in "default" namespace:

```
~> kubectl create -f deploy/service_account.yaml
~> kubectl create -f deploy/role.yaml
~> kubectl create -f deploy/role_binding.yaml
~> kubectl create -f deploy/cluster_role.yaml
~> kubectl create -f deploy/cluster_role_binding.yaml
~> kubectl create -f deploy/cluster_role_binding_pv.yaml
~> kubectl create -f deploy/crds/local.storage.openshift.io_localvolumes_crd.yaml
~> kubectl create -f deploy/operator.yaml
~> kubectl create -f deploy/crds/local.storage.openshift.io_v1_localvolume_cr.yaml
```

This should give you a running operator on a plain k8s cluster. You can now use `cr.yaml` present in this
directory for creating local volumes.
