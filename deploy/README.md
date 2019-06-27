
This directory stores files used for deploying the operator in a plain k8s cluster without OLM.

If you are deploying this operator on a cluster managed by OLM then you should use files from /manifests directory.

## Deploying Operator on plain k8s

1. Run following commands assuming you are in "default" namespace:

```
~> kubectl create -f sa.yaml
~> kubectl create -f rbac.yaml
~> kubectl create -f crd.yaml
~> kubectl create -f operator.yaml
```

This should give you a running operator on a plain k8s cluster. You can now use `cr.yaml` present in this
directory for creating local volumes.
