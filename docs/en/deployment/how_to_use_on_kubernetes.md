---
sidebar_label: Use JuiceFS on Kubernetes
sidebar_position: 3
slug: /how_to_use_on_kubernetes
---

JuiceFS is an ideal storage layer for Kubernetes, read this chapter to learn how to use JuiceFS in Kubernetes.

## JuiceFS CSI Driver

To use JuiceFS in Kubernetes, refer to [JuiceFS CSI Driver Documentation](https://juicefs.com/docs/csi/introduction/).


## Mount JuiceFS in the container

In some cases, you may need to mount JuiceFS volume directly in the container, which requires the use of the JuiceFS client in the container. You can refer to the following `Dockerfile` example to integrate the JuiceFS client into your application image:

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

Since JuiceFS needs to use the FUSE device to mount the file system, it is necessary to allow the container to run in privileged mode when creating a Pod:

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

:::caution
With the privileged mode being enabled by `privileged: true`, the container has access to all devices of the host, that is, it has full control of the host's kernel. Improper uses will bring serious safety hazards. Please conduct a thorough safety assessment before using it.
:::
