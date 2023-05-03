# local-storage-operator
Operator for local storage

```mermaid
graph LR
    PVC -->|Requests local storage using a StorageClass| StorageClass
    LocalVolume -->|Represents a physical storage device already mounted/existent on a node| Node
    Pod -->|References a PVC| PVC
    PV -->|References| LocalVolume
    LSO((Local Storage Operator)) -->|Manages| LocalVolume
    LSO((Local Storage Operator)) -->|Manages| StorageClass
    LocalVolumeSet -->|Discovers local storage devices on node| Node
    LocalVolumeSet -->|Creates| PV
```

## Deploying with OLM
Instructions to deploy on OCP >= 4.2 using OLM can be found [here](docs/deploy-with-olm.md)

## Using the must-gather image with the local storage operator
Instructions for using the local storage's must-gather image can be found [here](docs/must-gather.md)

## How does a user interact with LSO?
```mermaid
graph LR
    User((User)) -->|Creates| LocalVolumeDiscovery
    User -->|Creates| LocalVolumeSet
    User -->|Creates| LocalVolume
    LocalStorageOperator((LocalStorageOperator)) -->|Manages| LocalVolumeDiscovery
    LocalStorageOperator -->|Manages| LocalVolume
    LocalStorageOperator -->|Manages| StorageClass
```
