---
title: Kubernetes 使用 JuiceFS
sidebar_position: 2
slug: /how_to_use_on_kubernetes
---

JuiceFS 非常适合用作 Kubernetes 集群的存储层，阅读本文以了解如何使用。

## 以 `hostPath` 方式挂载 JuiceFS

如果你仅仅需要在 Kubernetes 容器中简单使用 JuiceFS，没有其他任何复杂要求（比如隔离性、权限控制），那么完全可以以 [`hostPath` 卷](https://kubernetes.io/zh-cn/docs/concepts/storage/volumes/#hostpath) 的方式使用 JuiceFS，搭建起来也十分简单：

1. 在 Kubernetes 节点上统一安装、挂载 JuiceFS，如果节点众多，考虑[自动化部署](./automation.md)。
1. 在 pod 定义中使用 `hostPath` 卷，直接将宿主机上的 JuiceFS 子目录挂载到容器中：

   ```yaml {8-16}
   apiVersion: v1
   kind: Pod
   metadata:
     name: juicefs-app
   spec:
     containers:
       - ...
         volumeMounts:
           - name: jfs-data
             mountPath: /opt/app-data
     volumes:
       - name: jfs-data
         hostPath:
           # 假设挂载点为 /jfs
           path: "/jfs/myapp/"
           type: Directory
   ```

相比以 CSI 驱动的方式来使用 JuiceFS，`hostPath` 更为简单直接，出问题也更易排查，但也要注意：

* 为求管理方便，一般所有容器都在使用同一个宿主机挂载点，缺乏隔离可能导致数据安全问题，未来也无法在不同应用中单独调整 JuiceFS 挂载参数。请谨慎评估。
* 所有节点都需要提前挂载 JuiceFS，因此集群加入新节点，需要在初始化流程里进行安装和挂载，否则新节点没有 JuiceFS 挂载点，容器将无法创建。
* 宿主机上的 JuiceFS 挂载进程所占用的系统资源（如 CPU、内存等）不受 Kubernetes 控制，有可能占用较多宿主机资源。可以考虑用 [`system-reserved`](https://kubernetes.io/zh-cn/docs/tasks/administer-cluster/reserve-compute-resources/#system-reserved) 来适当调整 Kubernetes 的系统资源预留值，为 JuiceFS 挂载进程预留更多资源。
* 如果宿主机上的 JuiceFS 挂载进程意外退出，将会导致应用 pod 无法正常访问挂载点，此时需要重新挂载 JuiceFS 文件系统并重建应用 pod。作为对比，JuiceFS CSI 驱动提供[「挂载点自动恢复」](https://juicefs.com/docs/zh/csi/recover-failed-mountpoint)功能来解决这个问题。
* 如果你使用 Docker 作为 Kubernetes 容器运行环境，最好令 JuiceFS 先于 Docker 启动，否则在节点重启的时候，偶尔可能出现容器启动时，JuiceFS 尚未挂载好的情况，此时便会因该依赖问题启动失败。以 systemd 为例，可以用下方 unit file 来配置启动顺序：

  ```systemd title="/etc/systemd/system/docker.service.d/override.conf"
  [Unit]
  # 请使用下方命令确定 JuiceFS 挂载服务的名称（例如 jfs.mount）：
  # systemctl list-units | grep "\.mount"
  After=network-online.target firewalld.service containerd.service jfs.mount
  ```

## JuiceFS CSI 驱动

在 Kubernetes 中使用 JuiceFS，请阅读[「JuiceFS CSI 驱动文档」](https://juicefs.com/docs/zh/csi/introduction)。

## 在容器中挂载 JuiceFS

某些情况下，你可能需要在容器中直接挂载 JuiceFS 存储，这需要在容器中使用 JuiceFS 客户端，你可以参考以下 `Dockerfile` 样本将 JuiceFS 客户端集成到应用镜像：

```dockerfile title="Dockerfile"
FROM alpine:latest
LABEL maintainer="Juicedata <https://juicefs.com>"

# Install JuiceFS client
RUN apk add --no-cache curl && \
  JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v') && \
  wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  install juicefs /usr/bin && \
  rm juicefs "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  rm -rf /var/cache/apk/* && \
  apk del curl

ENTRYPOINT ["/usr/bin/juicefs", "mount"]
```

由于 JuiceFS 需要使用 FUSE 设备挂载文件系统，因此在创建 Pod 时需要允许容器在特权模式下运行：

```yaml {19-20}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-run
spec:
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: linuxserver/nginx
          ports:
            - containerPort: 80
          securityContext:
            privileged: true
```

:::caution 注意
容器启用 `privileged: true` 特权模式以后，就具备了访问宿主机所有设备的权限，即拥有了对宿主机内核的完全控制权限。使用不当会带来严重的安全隐患，请您在使用此方式之前进行充分的安全评估。
:::
