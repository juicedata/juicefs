# Docker 使用 JuiceFS

目前有三种在 Docker 上使用 JuiceFS 存储的方法：

## 目录
1. [卷映射](#1-卷映射)
2. [Docker Volume Plugin](#2-Docker-Volume-Plugin)
3. [在 Docker 容器中挂载 JuiceFS](#3-在-Docker-容器中挂载-JuiceFS)

## 1. 卷映射

这种方法是将 JuiceFS 挂载点中的目录映射给 Docker 容器。比如， JuiceFS 文件系统挂载在 `/mnt/jfs` 目录，在创建容器时可以这样将 JuiceFS 存储映射到 Docker 容器：

```sh
$ sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

但需要注意，默认情况下，只有挂载 JuiceFS 存储的用户有存储的读写权限，当你需要将 JuiceFS 存储映射给 Docker 容器使用时，如果你没有使用 root 身份挂载 JuiceFS 存储，则需要先开启 FUSE 的 `user_allow_other` 选项，然后再添加  `-o allow_other` 选项重新挂载 JuiceFS 文件系统。

> **注意**：使用 root 用户身份或使用 sudo 挂载的 JuiceFS 存储，会自动添加 `allow_other` 选项，无需手动设置。

### FUSE 设置

默认情况下，`allow_other` 选项只允许 root 用户使用，为了让普通用户也有权限使用该挂载选项，需要修改 FUSE 的配置文件。 

#### 修改配置文件

编辑 FUSE 的配置文件，通常是 `/etc/fuse.conf`：

```sh
$ sudo nano /etc/fuse.conf
```

将配置文件中的 `user_allow_other` 前面的 `#` 注释符删掉，修改后类似下面这样：

```conf
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```

### 重新挂载 JuiceFS

FUSE 的 `user_allow_other` 启用后，你需要重新挂载 JuiceFS 文件系统，使用 `-o` 选项设置 `allow_other`，例如：

```sh
$ juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

🏡 [返回 目录](#目录)

## 2. Docker Volume Plugin

JuiceFS 也支持使用 [volume plugin](https://docs.docker.com/engine/extend/) 方式访问。

```sh
$ docker plugin install juicedata/juicefs
Plugin "juicedata/juicefs" is requesting the following privileges:
 - network: [host]
 - device: [/dev/fuse]
 - capabilities: [CAP_SYS_ADMIN]
Do you grant the above permissions? [y/N]

$ docker volume create -d juicedata/juicefs:latest -o name={{VOLUME_NAME}} -o metaurl={{META_URL}} -o access-key={{ACCESS_KEY}} -o secret-key={{SECRET_KEY}} jfsvolume
$ docker run -it -v jfsvolume:/opt busybox ls /opt
```

将上面 `{{VOLUME_NAME}}`、`{{META_URL}}`、`{{ACCESS_KEY}}`、`{{SECRET_KEY}}` 替换成你自己的文件系统配置。想要了解更多 JuiceFS 卷插件内容，可以访问  [juicedata/docker-volume-juicefs](https://github.com/juicedata/docker-volume-juicefs) 代码仓库。

🏡 [返回 目录](#目录)

## 3. 在 Docker 容器中挂载 JuiceFS

这种方法是将 JuiceFS 文件系统直接在 Docker 容器中进行挂载和使用，相比第一种方式，在容器中直接挂载 JuiceFS 可以缩小文件被误操作的几率。谁使用谁挂载，也让容器管理更清晰直观。

由于在容器中进行文件系统挂载需要将 JuiceFS 客户端拷贝到容器，在常规的容器管理过程中，需要把下载或拷贝 JuiceFS 客户端以及挂载文件系统的过程写入 Dockerfile，然后重新构建镜像。例如，你可以参考以下 Dockerfile，将 JuiceFS 客户端打包到 Alpine 镜像。

```dockerfile
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

另外，由于在容器中使用 FUSE 需要相应的权限，在创建容器时，需要指定 `--privileged=true` 选项，比如：

```sh
$ sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  --privileged=true \
  nginx-with-jfs
```

🏡 [返回 目录](#目录)