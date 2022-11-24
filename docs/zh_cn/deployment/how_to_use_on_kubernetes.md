---
sidebar_label: Kubernetes 使用 JuiceFS
sidebar_position: 3
slug: /how_to_use_on_kubernetes
---

JuiceFS 非常适合用作 Kubernetes 集群的存储层，阅读本文以了解如何使用。

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
