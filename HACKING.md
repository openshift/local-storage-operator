# Notes for hacking on local-storage-operator

## For developing on OpenShift and OLM

1. Download and install `opm` tool via - https://github.com/operator-framework/operator-registry

2. Create images as documented below

## Running the operator locally

1. Install LSO via OLM/OperatorHub in GUI

2. Build the operator
```
make build-operator
```

3. Export env variables required by LSO [deployment](https://github.com/openshift/local-storage-operator/blob/8fc42cc8b990907c88a6da551dc85b55c2dc4417/config/manifests/4.10/local-storage-operator.clusterserviceversion.yaml#L363)
```
export DISKMAKER_IMAGE=quay.io/openshift/origin-local-storage-diskmaker:latest
export KUBE_RBAC_PROXY_IMAGE=quay.io/openshift/origin-kube-rbac-proxy:latest
export PRIORITY_CLASS_NAME=openshift-user-critical
```

4. Define LSO namespace as env variable
```
export WATCH_NAMESPACE=openshift-local-storage
```

5. Scale down the operator (on remote cluster)
```
oc scale --replicas=0 deployment.apps/local-storage-operator -n openshift-local-storage
```

6. Run the operator locally
```
~> ./_output/bin/local-storage-operator -kubeconfig=$KUBECONFIG
```

## Automatic creation of operator, bundle and index images

All the images including operator, diskmaker, bundle and index images can be created in one shot using following command:

```
make bundle REGISTRY=quay.io/username
```

You may optionally set `REPO` to override the repository name, `VERSION` to be used as part of the tag, and `TOOL_BIN` to specify which tool to use (defaults to podman or docker).

```
make bundle REGISTRY=quay.io/username REPO=local-storage-operator VERSION=latest TOOL_BIN=`which docker`
```

This command also pushes the images to selected docker registry. So the command above will push the following images to quay:

```
quay.io/username/local-storage-operator:operator-latest
quay.io/username/local-storage-operator:diskmaker-latest
quay.io/username/local-storage-operator:mustgather-latest
quay.io/username/local-storage-operator:bundle-latest
quay.io/username/local-storage-operator:index-latest
```

After pushing all the images, it will print a subscription you can copy and paste to install the catalog source on your cluster:

```
Copy following snippet to apply it to your cluster

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: local-storage
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: quay.io/username/local-storage-operator:index-latest
EOF
```

Then you can install your build through OperatorHub.

### Cleaning up after a deploy

When deploying on OpenShift and OLM, just deleting catalog and subscription is not enough. You obviously have to run:

```
oc delete catalogsource/local-storage -n openshift-marketplace
```

But then you may also have a leftover CSV which must be deleted:

```
oc get csv|grep local
```

You also will have leftover CRD object which must be deleted:

```
oc get crd|grep local
```
