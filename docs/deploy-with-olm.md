# Deploy local-storage Operator

The local-storage Operator is avaialble in OLM, this makes it easy to consume local storage on your OCP cluster.

**Warning: These docs are for internal development and testing. Use https://docs.openshift.com/container-platform/latest/storage/persistent_storage/persistent_storage_local/persistent-storage-local.html docs for installation on OCP**

## Pre Requisites

* A running OCP cluster (>= 4.2), with unused raw disks.  This operator also works with upstream raw Kubernetes clusters
  and has been tested with version 1.14.
* OLM version >= 0.9.0.

## Deploy OLM

The local-storage-operator is dependent upon OLM, if you're not familiar with OLM, check out the [OLM](https://github.com/operator-framework/operator-lifecycle-manager)
Github repo.  Grab the [latest release](https://github.com/operator-framework/operator-lifecycle-manager/releases) of OLM and install it on your cluster. 

### Create and Subscribe to the local-storage Catalog

By default the local-storage-operator assumes the `openshift-local-storage` namespace for its resources and it is automatically created while installing
the operator using this method.


Run
`oc apply -f https://raw.githubusercontent.com/openshift/local-storage-operator/master/examples/olm/catalog-create-subscribe.yaml`
For Kubernetes substitute `oc` with `kubectl`

### Create a CR with Node Selector

The following example assumes that each worker node includes a raw disk attached at ``/dev/xvdf`` that we want to allocate for local-storage provisioning.  Of course you can have different devices, or only have disks on a subset of your worker nodes.

Gather the kubernetes hostname value for your nodes, (in our example we're using only worker nodes, you can omit the label selector if you have master nodes you're deploying pods on):

```bash
oc describe no -l node-role.kubernetes.io/worker | grep hostname
	kubernetes.io/hostname=ip-10-0-136-143
	kubernetes.io/hostname=ip-10-0-140-255
	kubernetes.io/hostname=ip-10-0-144-180
	
```

Create a LocalVolume manifest named ``create-cr.yaml`` using the hostnames obtained above:

#### CR using volumeMode - Filesystem

```yaml
apiVersion: "local.storage.openshift.io/v1"
kind: "LocalVolume"
metadata:
  name: "local-disks"
  namespace: "openshift-local-storage"
spec:
  nodeSelector:
    nodeSelectorTerms:
    - matchExpressions:
        - key: kubernetes.io/hostname
          operator: In
          values:
          - ip-10-0-136-143
          - ip-10-0-140-255
          - ip-10-0-144-180
  storageClassDevices:
    - storageClassName: "local-sc"
      volumeMode: Filesystem
      fsType: xfs
      devicePaths:
        - /dev/xvdf
```

#### CR using volumeMode - Block

```yaml
apiVersion: "local.storage.openshift.io/v1"
kind: "LocalVolume"
metadata:
  name: "local-disks"
  namespace: "openshift-local-storage"
spec:
  nodeSelector:
    nodeSelectorTerms:
    - matchExpressions:
        - key: kubernetes.io/hostname
          operator: In
          values:
          - ip-10-0-136-143
          - ip-10-0-140-255
          - ip-10-0-144-180
  storageClassDevices:
    - storageClassName: "localblock-sc"
      volumeMode: Block 
      devicePaths:
        - /dev/xvdg
```

### Deploy the CR

```bash
oc create -f create-cr.yaml
```

### NOTE about Block and Filesystem modes

With the local-disk provisioner you *MUST* explicitly create a CR for either `Block` or `Fileystem`. 
Unlike some storage that allows you to dynamically specify the volumeMode regardless of the 
storageClass definition, with local disks you must use the correct storageClass definition.

It's also important to note that if you do have a filesystem on the disks you allocated for
the Block CR, you may run in to issues where the pod reports being unschedulable:

``Warning  FailedScheduling  27s (x2 over 27s)  default-scheduler  0/6 nodes are available: 3 node(s) didn't find available persistent volumes to bind, 3 node(s) had taints that the pod didn't tolerate.``

In this example, the disks were prevsiously used, and a filesystem was applied.  Be sure to either use fresh disks, or remove any partitions and file systems if you're setting up a Block mode CR.

### Create a CR using Tolerations

In addition to a node selector, you can also specify [tolerations](https://docs.openshift.com/container-platform/latest/nodes/scheduling/nodes-scheduler-taints-tolerations.html) 
for pods created by the local-storage-operator. This allows the local-storage-operator to select
tainted nodes for use.

The following example demonstrates selecting nodes that have a taint, `localstorage`, 
that contains a value of `local`:

```yaml
apiVersion: "local.storage.openshift.io/v1"
kind: "LocalVolume"
metadata:
  name: "local-disks"
  namespace: "openshift-local-storage"
spec:
  tolerations:
    - key: localstorage
      operator: Equal
      value: "local"
  storageClassDevices:
    - storageClassName: "local-sc"
      volumeMode: Filesystem
      fsType: xfs
      devicePaths:
        - /dev/xvdf
```

The defined tolerations will be passed to the resulting DaemonSets, allowing the diskmaker and provisioner pods to be created for nodes that contain the specified taints.

### Verify your deployment

```bash
oc get all -n openshift-local-storage
NAME                                          READY   STATUS    RESTARTS   AGE
pod/local-disks-local-provisioner-h97hj       1/1     Running   0          46m
pod/local-disks-local-provisioner-j4mnn       1/1     Running   0          46m
pod/local-disks-local-provisioner-kbdnx       1/1     Running   0          46m
pod/local-diskslocal-diskmaker-ldldw          1/1     Running   0          46m
pod/local-diskslocal-diskmaker-lvrv4          1/1     Running   0          46m
pod/local-diskslocal-diskmaker-phxdq          1/1     Running   0          46m
pod/local-storage-manifests-f8zc9             1/1     Running   0          48m
pod/local-storage-operator-54564d9988-vxvhx   1/1     Running   0          47m

NAME                              TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)     AGE
service/local-storage-manifests   ClusterIP   172.30.183.114   <none>        50051/TCP   48m
service/local-storage-operator    ClusterIP   172.30.49.90     <none>        60000/TCP   47m

NAME                                           DESIRED   CURRENT   READY   UP-TO-DATE   AVAILABLE   NODE SELECTOR   AGE
daemonset.apps/local-disks-local-provisioner   3         3         3       3            3           <none>          46m
daemonset.apps/local-diskslocal-diskmaker      3         3         3       3            3           <none>          46m

NAME                                     READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/local-storage-operator   1/1     1            1           47m

NAME                                                DESIRED   CURRENT   READY   AGE
replicaset.apps/local-storage-operator-54564d9988   1         1         1       47m
```

Pay particular attention to the DESIRED/CURRENT number of daemonset processes, if the DESIRED count == 0, that typically means that your label selectors were invalid.

You should now have cooresponding PVs:

```bash
oc get pv
NAME                CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS      CLAIM   STORAGECLASS   REASON   AGE
local-pv-1cec77cf   100Gi      RWO            Delete           Available           local-sc                88m
local-pv-2ef7cd2a   100Gi      RWO            Delete           Available           local-sc                82m
local-pv-3fa1c73    100Gi      RWO            Delete           Available           local-sc                48m
```

### Example Usage

Request a PVC using the local-sc storage class we just created:

```bash
oc create -f - << EOF
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: example-local-claim
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 100Gi 
  storageClassName: local-sc
EOF
```

*NOTE* The default volumeMode for kubernetes pvc's is "Filesystem"
Keep in mind that when using the local-provisioner a claim is held in pending and not bound until a consuming pod is scheduled and created.

```bash
oc get pvc
NAME                  STATUS    VOLUME   CAPACITY   ACCESS MODES   STORAGECLASS   AGE
example-local-claim   Pending                                      local-sc       2s
```
```bash
oc describe pvc example-local-claim
Name:          example-local-claim
Namespace:     default
StorageClass:  local-sc
Status:        Pending
Volume:        
Labels:        <none>
Annotations:   <none>
Finalizers:    [kubernetes.io/pvc-protection]
Capacity:      
Access Modes:  
VolumeMode:    Filesystem
Events:
  Type       Reason                Age                From                         Message
----       ------                ----               ----                         -------
  Normal     WaitForFirstConsumer  13s (x2 over 13s)  persistentvolume-controller  waiting for first consumer to be created before binding
Mounted By:  <none>
```
