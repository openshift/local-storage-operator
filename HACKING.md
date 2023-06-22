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

## Automatic creation of images

All images including operator, diskmaker, bundle and index images can be created and pushed in one shot, this will also update LSO CSV file to point to newly created operator and diskmaker images:

> First make sure you're authenticated to private registry for pulling base images: `docker login registry.ci.openshift.org`

```bash
export USER=<username>
export HACK_BUNDLE_IMAGE=quay.io/$USER/test:bundle
export HACK_INDEX_IMAGE=quay.io/$USER/test:index
export HACK_DISKMAKER_IMAGE=quay.io/$USER/test:diskmaker 
export HACK_OPERATOR_IMAGE=quay.io/$USER/test:operator
docker login quay.io -u $USER
make bundle
```

Custom diskmaker and operator images are optional, default images will be used if omitted. In this case the CSV file will not be updated:

```bash
export USER=<username>
export HACK_BUNDLE_IMAGE=quay.io/$USER/test:bundle
export HACK_INDEX_IMAGE=quay.io/$USER/test:index
docker login quay.io -u $USER
make bundle
```

This should give us an index image `quay.io/username/test:index`. Update the `CatalogSource` entry in `examples/olm/catalog-create-subscribe.yaml`
to point to your newly created index image. Once updated, we can install local-storage-operator via following command:

```bash
oc create -f examples/olm/catalog-create-subscribe.yaml
```

## Manual creation of bundle and index image.

1. Since we will be going to test with our version of images, we need to modify CSV file to point to our version of image. This can be done by modifying following file:

```
~> vim config/manifests/local-storage-operator.clusterserviceversion.yaml
```

and change image names in deployment field.

2. Now lets build a bundle image which can be used by index image. This can be done by:

```
~> cd config
~> docker build -f ./bundle.Dockerfile -t quay.io/gnufied/local-storage-bundle:bundle1 .
```

3. Tag and push image to quay.io (or a container registry of your choice). Make sure that images are publicly available.
4. Now lets build index image which we can use from Openshift:

```
~> opm index add --bundles quay.io/gnufied/local-storage-bundle:bundle1 --tag quay.io/gnufied/gnufied-index:1.0.0 --container-tool docker
```

If you are using podman then there is no need to specify container-tool option.

5. Tag and push index image to quay.io (or a container registry of your choice). Make sure that images are publicly available.

6. Edit the catalog source template example `examples/olm/catalog-create-subscribe.yaml` to point to your index image:

```
~> vim examples/olm/catalog-create-subscribe.yaml
```

7. Create a catalogSource and subscribe to the source by applying `examples/olm/catalog-create-subscribe.yaml`.

```
~> oc create -f examples/olm/catalog-create-subscribe.yaml
```

8. Switch to `openshift-local-storage` project and proceed with creating CR and start using the operator.

```
~> oc project openshift-local-storage
```

### Cleaning up after a deploy

When deploying on OpenShift and OLM, just deleting catalog and subscription is not enough. You obviously have to run:

```
oc delete -f examples/olm/catalog-create-subscribe.yaml
```

But then you may also have a leftover CSV which must be deleted:


```
oc get csv|grep local
```

You also will have leftover CRD object which must be deleted:


```
oc get crd|grep local
```
