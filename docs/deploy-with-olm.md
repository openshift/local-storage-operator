# Deploy local-storage Operator

The local-storage Operator is avaialble in OLM, this makes it easy to consume local storage on your OCP cluster.

## Pre Requisites

A running OCP cluster (>= 4.1), with unused raw disks.  

## Install and Subscribe to the local-storage catalog

### Create a project/namespace to use for the operator:

```bash 
oc new-project local-storage
```

### Create and Subscribe to the local-storage Catalog

```bash
oc create -f https://github.com/openshift/local-storage-operator/blob/master/examples/olm-deploy/catalog-create-subscribe.yaml
```

The above file uses defaults values and a namespace of ``local-storage`` which is fine in most cases, but download and modify if desired before running oc create.

### Create a CR with Node Selector

The following example assumes that each worker node include a raw disk ``/dev/xvdf`` that we want to allocate for local-storage provisioning.  Of course you can have different devices, or only have disks on a subset of your worker nodes.

Gather the kubernetes hostname value for your worker nodes, (in our example we're using all worker nodes):

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
oc create -f crete-cr.yaml
```

### Verify your deployment

```bash
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


