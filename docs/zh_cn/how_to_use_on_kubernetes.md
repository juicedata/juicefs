# Kubernetes 使用 JuiceFS

JuiceFS 为 Kubernetes 环境提供了 [CSI Driver](https://github.com/juicedata/juicefs-csi-driver)。


## 先决条件

- Kubernetes 1.14+


## 安装

### 通过 Helm 安装

首先参考 [Helm 安装指南](https://github.com/helm/helm#install)安装 Helm，需要 Helm 3 及以上版本。

1. 准备一个叫做 `values.yaml` 的文件，其中包含 Redis 和对象存储的访问信息（这里以 Amazon S3 的 `us-east-1` 区域为例）：

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

这里我们给 Kubernetes 节点的 EC2 实例分配了 AWS [IAM 角色](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_use_switch-role-ec2.html)，否则 `accessKey` 和 `secretKey` 不能为空。这里以 ElastiCache for Redis 作为元数据引擎。

2. 安装

```shell
helm repo add juicefs-csi-driver https://juicedata.github.io/juicefs-csi-driver/
helm repo update
helm upgrade juicefs-csi-driver juicefs-csi-driver/juicefs-csi-driver --install -f ./values.yaml
```

3. 检查部署

- 检查 pods 正在运行：部署会启动一个叫做 `juicefs-csi-controller` 的 `StatefulSet`（副本数为 1）以及一个叫做 `juicefs-csi-node` 的 `DaemonSet`，因此运行 `kubectl -n kube-system get pods -l app.kubernetes.io/name=juicefs-csi-driver` 命令将会看到 `n+1` 个 pods 正在运行（其中 `n` 是 Kubernetes 集群节点的数量）。例如：

```sh
$ kubectl -n kube-system get pods -l app.kubernetes.io/name=juicefs-csi-driver
NAME                       READY   STATUS    RESTARTS   AGE
juicefs-csi-controller-0   3/3     Running   0          22m
juicefs-csi-node-v9tzb     3/3     Running   0          14m
```

- 检查 Secret：`kubectl -n kube-system describe secret juicefs-sc-secret` 命令将会显示和上面 `values.yaml` 文件中的 `backend` 字段对应的 Secret 信息：

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

- 检查存储类（storage class）：`kubectl get sc juicefs-sc` 命令将会显示类如下面的存储类：

```sh
$ kubectl get sc juicefs-sc
NAME         PROVISIONER       RECLAIMPOLICY   VOLUMEBINDINGMODE   ALLOWVOLUMEEXPANSION   AGE
juicefs-sc   csi.juicefs.com   Retain          Immediate           false                  69m
```

### 通过 kubectl 安装

1. 部署 CSI Driver：

```bash
kubectl apply -f https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml
```

这里我们使用 `juicedata/juicefs-csi-driver:latest` 镜像，如果你希望使用特定标签的镜像（例如 `v0.7.0`），你需要下载部署的 YAML 文件并修改它：

```bash
curl -sSL https://raw.githubusercontent.com/juicedata/juicefs-csi-driver/master/deploy/k8s.yaml | sed 's@juicedata/juicefs-csi-driver@juicedata/juicefs-csi-driver:v0.7.0@' | kubectl apply -f -
```

2. 创建存储类

- 创建叫做 `juicefs-sc-secret` 的 Secret：

```bash
kubectl -n kube-system create secret generic juicefs-sc-secret \
  --from-literal=name=test \
  --from-literal=metaurl=redis://juicefs.afyq4z.0001.use1.cache.amazonaws.com/3 \
  --from-literal=storage=s3 \
  --from-literal=bucket=https://juicefs-test.s3.us-east-1.amazonaws.com \
  --from-literal=access-key="" \
  --from-literal=secret-key=""

```

- 通过 `kubectl apply` 创建存储类：

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


## 使用 JuiceFS

现在我们可以在 pods 中使用 JuiceFS。这里我们创建了一个 `PersistentVolumeClaim` 并在一个 pod 中使用它：

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

将以上内容保存到一个类似叫做 `juicefs-app.yaml` 的文件中，然后运行 `kubectl apply -f juicefs-app.yaml` 命令来启动这个 pod。运行完以后，你可以检查 pod 的状态：

```sh
$ kubectl get pod juicefs-app
NAME          READY     STATUS    RESTARTS   AGE
juicefs-app   1/1       Running   0          10m
```

如果 pod 的状态不是 `Running`（例如 `ContainerCreating`），应该是存在一些问题。请参考[故障处理](https://github.com/juicedata/juicefs-csi-driver/blob/master/docs/troubleshooting.md)文档。

如果想了解更多关于 JuiceFS CSI Driver 的信息，请参考[项目主页](https://github.com/juicedata/juicefs-csi-driver)。


## 监控

JuiceFS CSI Driver 可以在 `9567` 端口导出 [Prometheus](https://prometheus.io) 指标。关于所有监控指标的详细描述，请参考 [JuiceFS 监控指标](p8s_metrics.md)。

### 配置 Prometheus 服务

新增一个任务到 `prometheus.yml`：

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
      replacement: $1:9567
      target_label: __address__
    - source_labels: [__meta_kubernetes_pod_node_name]
      target_label: node
      action: replace
```

这里我们假设 Prometheus 服务运行在 Kubernetes 集群中，如果你的 Prometheus 服务运行在 Kubernetes 集群之外，请确保 Prometheus 服务可以访问 Kubernetes 节点，请参考[这个 issue](https://github.com/prometheus/prometheus/issues/4633) 添加 `api_server` 和 `tls_config` 配置到以上文件：

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

### 配置 Grafana 仪表盘

JuiceFS 为 [Grafana](https://grafana.com) 提供了一个[仪表盘模板](../en/k8s_grafana_template.json)，可以导入到 Grafana 中用于展示 Prometheus 收集的监控指标。
