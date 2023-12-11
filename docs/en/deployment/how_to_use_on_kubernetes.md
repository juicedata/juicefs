---
title: Use JuiceFS on Kubernetes
sidebar_position: 2
slug: /how_to_use_on_kubernetes
---

JuiceFS is an ideal storage layer for Kubernetes, read this chapter to learn how to use JuiceFS in Kubernetes.

## Use JuiceFS via `hostPath`

If you simply need to use JuiceFS inside Kubernetes pods, without any special requirements (e.g. isolation, permission control), then [`hostPath`](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath) can be a good practice, which is also really easy to setup:

1. Install and mount JuiceFS on all Kubernetes worker nodes, [Automated Deployment](./automation.md) is recommended for this type of work.
1. Use `hostPath` volume inside pod definition, and mount a JuiceFS sub-directory to container:

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
           # Assuming JuiceFS is mounted on /jfs
           path: "/jfs/myapp/"
           type: Directory
   ```

In comparison to using JuiceFS CSI Driver, `hostPath` is a much more simple practice, and easier to debug when things go wrong, but notice that:

* For ease of management, generally all pods use the same host mount point. Lack of isolation may lead to data security issues, and obviously, you won't be able to adjust JuiceFS mount parameters separately for each application. Please evaluate carefully.
* All worker nodes should mount JuiceFS in advance, so when adding a new node to the cluster, JuiceFS needs to be installed and mounted during the initialization process, otherwise the new node does not have a JuiceFS mount point, and the container will not be created.
* The system resources (such as CPU, memory, etc.) occupied by the JuiceFS mounting process on the host are not controlled by Kubernetes, and may occupy too many host resources. You can consider using [`system-reserved`](https://kubernetes.io/docs/tasks/administer-cluster/reserve-compute-resources/#system-reserved) to properly adjust the system resource reservation of Kubernetes, to reserve more resources for the JuiceFS mount process.
* If the JuiceFS mount process on the host exits unexpectedly, the application pod will not be able to access the mount point normally. In this case, the JuiceFS file system needs to be remounted and the application pod must be rebuilt. However, JuiceFS CSI Driver solves this problem well by providing the [Automatic Mount Point Recovery](https://juicefs.com/docs/csi/recover-failed-mountpoint) mechanism.
* If you're using Docker as Kubernetes container runtime, it's best to start JuiceFS mount prior to Docker in startup order, to avoid containers being created before JuiceFS is properly mounted. For systemd, you can use below unit file to manually control startup order:

  ```systemd title="/etc/systemd/system/docker.service.d/override.conf"
  [Unit]
  # Use below command to obtain JuiceFS mount service name
  # systemctl list-units | grep "\.mount"
  After=network-online.target firewalld.service containerd.service jfs.mount
  ```

## JuiceFS CSI Driver

To use JuiceFS in Kubernetes, refer to [JuiceFS CSI Driver Documentation](https://juicefs.com/docs/csi/introduction).

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
