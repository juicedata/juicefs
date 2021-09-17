# Use JuiceFS on Kubernetes

JuiceFS provides the [CSI Driver](https://github.com/juicedata/juicefs-csi-driver) for Kubernetes.

## Table of Content
- [Prerequisites](#Prerequisites)
- [Installation](#Installation)
  - [Install with Helm](#Install-with-Helm)
  - [Install with kubectl](#Install-with-kubectl)
- [Use JuiceFS](#Use-JuiceFS)
- [Monitoring](#Monitoring)
  - [Configure Prometheus server](#Configure-Prometheus-server)
  - [Configure Grafana dashboard](#Configure-Grafana-dashboard)

## Prerequisites

- Kubernetes 1.14+

## Installation

### Install with Helm

To install Helm, refer to the [Helm install guide](https://github.com/helm/helm#install), Helm 3 is required.

1. Prepare a file `values.yaml` with access information about metadata engine (e.g. Redis) and object storage (take Amazon S3 `us-east-1` as an example):

```yaml
storageClasses:
- name: juicefs-sc
  enabled: true
  reclaimPolicy: Retain
  backend:
    name: "test"
    metaurl: "redis://juicefs.afyq4z.0001.use1.cache.amazonaws.com/3"
    storage: "s3"
    accessKey: ""
    secretKey: ""
    bucket: "https://juicefs-test.s3.us-east-1.amazonaws.com"
```

Here we assign AWS [IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_switch-role-ec2.html) for the EC2 Kubernetes node, otherwise the `accessKey` and `secretKey` cannot be empty. We use ElastiCache for Redis as the meta store.

2. Install

```shell
helm repo add juicefs-csi-driver https://juicedata.github.io/juicefs-csi-driver/
helm repo update
helm upgrade juicefs-csi-driver juicefs-csi-driver/juicefs-csi-driver --install -f ./values.yaml
```

3. Check the deployment

- Check pods are running: the deployment will launch a `StatefulSet` named `juicefs-csi-controller` with replica `1` and a `DaemonSet` named `juicefs-csi-node`, so run `kubectl -n kube-system get pods -l app.kubernetes.io/name=juicefs-csi-driver` should see `n+1` (where `n` is the number of worker nodes of the Kubernetes cluster) pods is running. For example:

```sh
$ kubectl -n kube-system get pods -l app.kubernetes.io/name=juicefs-csi-driver
NAME                       READY   STATUS    RESTARTS   AGE
juicefs-csi-controller-0   3/3     Running   0          22m
juicefs-csi-node-v9tzb     3/3     Running   0          14m
```

- Check secret: `kubectl -n kube-system describe secret juicefs-sc-secret` will show the secret with above `backend` fields in `values.yaml`:

```sh
$ kubectl -n kube-system describe secret juicefs-sc-secret
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

- Check storage class: `kubectl get sc juicefs-sc` will show the storage class like this:

```sh
$ kubectl get sc juicefs-sc
NAME         PROVISIONER       RECLAIMPOLICY   VOLUMEBINDINGMODE   ALLOWVOLUMEEXPANSION   AGE
juicefs-sc   csi.juicefs.com   Retain          Immediate           false                  69m
```

🏡 [Back to Top](#Table-of-Content)

### Install with kubectl

1. Deploy the driver:

```bash
kubectl apply -f https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml
```

Here we use the `juicedata/juicefs-csi-driver:latest` image, if you want to use the specified tag such as `v0.7.0`, you should download the deploy YAML file and modified it:

```bash
curl -sSL https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml | sed 's@juicedata/juicefs-csi-driver@juicedata/juicefs-csi-driver:v0.7.0@' | kubectl apply -f -
```

2. Create storage class

- Create secret `juicefs-sc-secret`:

```bash
kubectl -n kube-system create secret generic juicefs-sc-secret \
  --from-literal=name=test \
  --from-literal=metaurl=redis://juicefs.afyq4z.0001.use1.cache.amazonaws.com/3 \
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
reclaimPolicy: Retain
volumeBindingMode: Immediate
```

🏡 [Back to Top](#Table-of-Content)

## Use JuiceFS

Now we can use JuiceFS in our pods. Here we create a `PersistentVolumeClaim` and refer to it in a pod as an example:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: juicefs-pvc
spec:
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 10Pi
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
      claimName: juicefs-pvc
```

Save above content to a file named like `juicefs-app.yaml`, then use command `kubectl apply -f juicefs-app.yaml` to bootstrap the pod. After that, you can check the status of pod:

```sh
$ kubectl get pod juicefs-app
NAME          READY     STATUS    RESTARTS   AGE
juicefs-app   1/1       Running   0          10m
```

If the status of pod is not `Running` (e.g. `ContainerCreating`), there may have some issues. Please refer to the [troubleshooting](https://github.com/juicedata/juicefs-csi-driver/blob/master/docs/troubleshooting.md) document.

For more details about JuiceFS CSI Driver please refer to [project homepage](https://github.com/juicedata/juicefs-csi-driver).

🏡 [Back to Top](#Table-of-Content)

## Monitoring

JuiceFS CSI Driver can export [Prometheus](https://prometheus.io) metrics at port `9567`. For a description of all monitoring metrics, please refer to [JuiceFS Metrics](p8s_metrics.md).

### Configure Prometheus server

Add a job to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'juicefs'
    kubernetes_sd_configs:
    - role: pod
    relabel_configs:
    - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
      action: keep
      regex: juicefs-mount
    - source_labels: [__address__]
      action: replace
      regex: ([^:]+)(:\d+)?
      replacement: $1:9567
      target_label: __address__
    - source_labels: [__meta_kubernetes_pod_node_name]
      target_label: node
      action: replace
```

Here we assume the Prometheus server is running inside Kubernetes cluster, if your Prometheus server is running outside Kubernetes cluster, make sure Kubernetes cluster nodes are reachable from Prometheus server, refer to [this issue](https://github.com/prometheus/prometheus/issues/4633) to add the `api_server` and `tls_config` client auth to the above configuration like this:

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

### Configure Grafana dashboard

JuiceFS provides a [dashboard template](./grafana_template.json) for [Grafana](https://grafana.com), which can be imported to show the collected metrics in Prometheus.

🏡 [Back to Top](#Table-of-Content)