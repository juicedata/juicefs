# Use JuiceFS on Kubernetes

JuiceFS provides the [CSI driver](https://github.com/juicedata/juicefs-csi-driver) for Kubernetes.



## Prerequisites

- Kubernetes 1.14+



## Installation

### Install with Helm

To install Helm, refer to the [Helm install guide](https://github.com/helm/helm#install), Helm 3 is required.

1. Prepare a file `values.yaml` with access infomation about Redis and object storage (take Amazon S3 `us-east-1` as an example)

```yaml
storageClasses:
- name: juicefs-sc
  enabled: true
  reclaimPolicy: Delete
  backend:
    name: "test"
    metaurl: "redis://juicefs.afyq4z.0001.use1.cache.amazonaws.com/3"
    storage: "s3"
    accessKey: ""
    secretKey: ""
    bucket: "https://juicefs-test.s3.us-east-1.amazonaws.com"
```

Here we assign AWS [IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_switch-role-ec2.html) for the EC2 Kuberentes node, otherwise the `accessKey` and `secretKey` cannot be empty. We use ElastiCache for Redis as the meta store.

2. Install

```shell
helm repo add juicefs-csi-driver https://juicedata.github.io/juicefs-csi-driver/
helm repo update
helm upgrade juicefs-csi-driver juicefs-csi-driver/juicefs-csi-driver --install -f ./values.yaml
```

3. Check the deployment

- Check running pods: the deployment will launch a `StatefulSet` with replica `1` for the `juicefs-csi-controller` and a `DaemonSet` for `juicefs-csi-node`, so run `kubectl -n kube-system get pods | grep juicefs-csi` should see `n+1` （where `n` is the number of worker node of the kubernetes cluster) pods is running.
- Check secret: `kubectl -n kube-system describe secret juicefs-sc-secret` will show the secret with above `backend` fields:

```
Name:         juicefs-sc-secret
Namespace:    kube-system
Labels:       app.kubernetes.io/instance=juicefs-csi-driver
              app.kubernetes.io/managed-by=Helm
              app.kubernetes.io/name=juicefs-csi-driver
              app.kubernetes.io/version=0.7.0
              helm.sh/chart=juicefs-csi-driver-0.1.0
Annotations:  meta.helm.sh/release-name: juicefs-csi-driver
              meta.helm.sh/release-namespace: default

Type:  Opaque

Data
====
access-key:  0 bytes
bucket:      47 bytes
metaurl:     54 bytes
name:        4 bytes
secret-key:  0 bytes
storage:     2 bytes
```


- Check storage class: `kubectl get sc` will show the storage class like this:

```
NAME                 PROVISIONER                RECLAIMPOLICY   VOLUMEBINDINGMODE   ALLOWVOLUMEEXPANSION   AGE
juicefs-sc           csi.juicefs.com            Delete          Immediate           false                  21m
```


### Install with kubectl

1. Deploy the driver:

```bash
kubectl apply -f https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml
```

Here we use the `juicedata/juicefs-csi-driver:latest` image, if we want to use the specified tag such as `v0.7.0` , we should download the deploy YAML file and modified it:

```bash
curl -sSL https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml | sed 's@juicedata/juicefs-csi-driver@juicedata/juicefs-csi-driver:v0.7.0@' | kubectl apply -f -
```

2. Create storage class

- Add secret `juicefs-sc-secret` :

```bash
kubectl -n kube-system create secret generic juicefs-sc-secret \
  --from-literal=name=test \
  --from-literal=meta-url=redis://juicefs.afyq4z.0001.use1.cache.amazonaws.com/3 \
  --from-literal=storage=s3 \
  --from-literal=bucket=https://juicefs-test.s3.us-east-1.amazonaws.com \
  --from-literal=access-key="" \
  --from-literal=secret-key=""

```

- Create storage class use `kubectl apply`:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: juicefs-sc
provisioner: csi.juicefs.com
parameters:
  csi.storage.k8s.io/node-publish-secret-name: juicefs-sc-secret
  csi.storage.k8s.io/node-publish-secret-namespace: kube-system
  csi.storage.k8s.io/provisioner-secret-name: juicefs-sc-secret
  csi.storage.k8s.io/provisioner-secret-namespace: kube-system
reclaimPolicy: Delete
volumeBindingMode: Immediate
```


## Use JuiceFS

Now we can use JuiceFS in our pods.  Here we create a `PVC` and refer it in a pod as an example:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: jtext-pvc
spec:
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 10Gi
  storageClassName: juicefs-sc
---
apiVersion: v1
kind: Pod
metadata:
  name: juicefs-app
spec:
  containers:
  - args:
    - -c
    - while true; do echo $(date -u) >> /data/out.txt; sleep 5; done
    command:
    - /bin/sh
    image: busybox
    name: app
    volumeMounts:
    - mountPath: /data
      name: juicefs-pv
  volumes:
  - name: juicefs-pv
    persistentVolumeClaim:
      claimName: jtext-pvc

```

Save above content to a file named like `juicefs-app.yaml` ，then use command `kubectl apply -f juicefs-app.yaml` to bootstrap the pod.



For more details about JuiceFS CSI driver please refer [JuiceFS CSI driver](https://github.com/juicedata/juicefs-csi-driver).



## Monitoring

JuiceFS CSI driver can export [prometheus](https://prometheus.io) metrics at port `:9560` .

### Configurate Prometheus server

Add a job to `prometheus.yml` :

```yaml
scrape_configs:
  - job_name: 'juicefs'
    kubernetes_sd_configs:
    - role: pod
    relabel_configs:
    - source_labels: [__meta_kubernetes_namespace, __meta_kubernetes_pod_name]
      action: keep
      regex: kube-system;juicefs-csi-node-.+
    - source_labels: [__address__]
      action: replace
      regex: ([^:]+)(:\d+)?
      replacement: $1:9560
      target_label: __address__
    - source_labels: [__meta_kubernetes_pod_node_name]
      target_label: node
      action: replace
```

Here we assume the prometheus server is running inside Kubernetes cluster, if your prometheus server is running outside Kubernetes cluster, make sure Kubernetes cluster nodes are reachable from prometheus server, refer [this issue](https://github.com/prometheus/prometheus/issues/4633) to add the `api_server` and `tls_config` client auth to the above configuration like this:

```yaml
scrape_configs:
  - job_name: 'juicefs'
    kubernetes_sd_configs:
    - api_server: <Kubernetes API Server>
      role: pod
      tls_config:
        ca_file: <...>
        cert_file: <...>
        key_file: <...>
        insecure_skip_verify: false
    relabel_configs:
    ...
    ...
```



### Configurate Grafana dashboard

We provide a [dashboard template](./k8s_grafana_template.json) for [Grafana](https://grafana.com/) , which can be imported to show the collected metrics in Prometheus.

