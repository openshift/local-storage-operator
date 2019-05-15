# Deploy local-storage Operator

The local-storage Operator is avaialble in OLM, this makes it easy to consume local storage on your OCP cluster.

## Pre Requisites

* A running OCP cluster (>= 4.1), with unused raw disks.  This operator also works with upstream raw Kubernetes clusters
  and has been tested with version 1.14.
* OLM version >= 0.9.0.

## Deploy OLM

The local-storage-operator is dependent upon OLM, if you're not familiar with OLM, check out the [OLM](https://github.com/operator-framework/operator-lifecycle-manager)
Github repo.  Grab the [latest release](https://github.com/operator-framework/operator-lifecycle-manager/releases) of OLM and install it on your cluster. 

### Create and Subscribe to the local-storage Catalog

By default the local-storage-operator assumes the `local-storage` namespace for it's resources.  If you're not modifying the example manifests, be sure to create
the `local-storage` project or namespace: 
`oc new-project local-storage` or `kubectl create ns local-storage`


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

```yaml
apiVersion: "local.storage.openshift.io/v1alpha1"
kind: "LocalVolume"
metadata:
  name: "local-disks"
  namespace: "local-storage"
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
      deviceNames:
        - xvdf
```

Deploy the local storage CR:

```bash
oc create -f create-cr.yaml
```

### Verify your deployment

```bash
oc get all -n local-storage
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
<< EOF | oc create -f -
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
