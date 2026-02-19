# local-storage-operator
Operator for local storage

> [!Warning]
> Due to an OLM catalog update, branches 4.12 through 4.17 have transitioned to catalog version 4.18.
>
> Please direct all future backports, bug fixes, and enhancements to version 4.18 or newer. We are no longer accepting updates for versions 4.12 – 4.17.

## Deploying with OLM
Instructions to deploy on OCP >= 4.2 using OLM can be found [here](docs/deploy-with-olm.md)

## Using the must-gather image with the local storage operator
Instructions for using the local storage's must-gather image can be found [here](docs/must-gather.md)

## Bumping OCP version in CSV and OLM metadata

This updates the package versions in `config/manifests/local-storage-operator.package.yaml` and `config/manifests/stable/local-storage-operator.clusterserviceversion.yaml` to 4.20:
```
./hack/update-metadata.sh 4.20
```
