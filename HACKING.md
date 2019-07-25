# Notes for hacking on local-storage-operator

## For developing on plain k8s

Developing on plain k8s is pretty simple. Just write your code and use Makefile to build your own images
and then update `deploy/operator.yaml` to point to your images and follow rest of the instructions in `deploy/README.md`.

## For developing on OpenShift and OLM

1. Same as plain k8s, make your code change and build local-diskmaker and local-storage-operator images. Push them to quay or docker.io.
2. Next we will have to update CSV inside manifests directory to point to those images.
3. After updating the manifests file, you need to build your own local-registry. You can use `Dockerfile.registry` to do that.


```
docker build --no-cache -t quay.io/gnufied/local-registry:latest -f ./Dockerfile.registry .
```

Push the result image somewhere.

NOTE: When this document was written https://bugzilla.redhat.com/show_bug.cgi?id=1726409 bug prevented local-registry from being usable
with OLM if manifests contain `image-references` file. So please make sure that `manifests/4.2.0/image-references` file is removed
before creating local-registry.

4. When creating a catalog from `examples/olm/catalog-create-subscribe.yaml` file, specify your own image of local registry.

5. Proceed with creating CR and start using the operator.

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
