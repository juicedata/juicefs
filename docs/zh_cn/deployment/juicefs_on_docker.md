---
title: Docker 使用 JuiceFS
sidebar_position: 3
slug: /juicefs_on_docker
---

将 JuiceFS 作为 Docker 持久化存储有以下几种常用方法：

## 1. 卷映射 {#volume-mapping}

这种方法是将 JuiceFS 挂载点中的目录映射给 Docker 容器。比如，JuiceFS 文件系统挂载在 `/mnt/jfs` 目录，在创建容器时可以这样将 JuiceFS 存储映射到 Docker 容器：

```sh
sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

但需要注意，默认情况下，只有挂载 JuiceFS 存储的用户有存储的读写权限，在将 JuiceFS 存储映射给 Docker 容器时，如果你没有使用 root 身份挂载 JuiceFS 存储，则需要先调整 FUSE 设置，打开 `user_allow_other` 选项，然后再添加  `-o allow_other` 选项重新挂载 JuiceFS 文件系统。

:::tip
使用 root 用户或 sudo 命令挂载的 JuiceFS 存储，会自动添加 `allow_other` 选项，无需手动设置。
:::

### 调整 FUSE 设置

默认情况下，`allow_other` 选项只允许 root 用户使用，为了让普通用户也有权限使用该挂载选项，需要修改 FUSE 的配置文件。

编辑 FUSE 的配置文件，通常是 `/etc/fuse.conf`：

```sh
sudo nano /etc/fuse.conf
```

将配置文件中的 `user_allow_other` 前面的 `#` 注释符删掉，修改后类似下面这样：

<!-- autocorrect: false -->
```conf
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```
<!-- autocorrect: true -->

### 重新挂载 JuiceFS

FUSE 的 `user_allow_other` 启用后，你需要重新挂载 JuiceFS 文件系统，使用 `-o` 选项设置 `allow_other`，例如：

```sh
juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

## 2. Docker Volume Plugin（卷插件） {#docker-volume-plugin}

JuiceFS 面向 Docker 环境提供了 [volume plugin](https://docs.docker.com/engine/extend)（卷插件），可以像本地磁盘一样在 JuiceFS 上创建存储卷。

### 解决依赖

因为 JuiceFS 挂载依赖 FUSE，请确保宿主机上已经安装了 FUSE 驱动，以 Debian/Ubuntu 为例：

```shell
sudo apt-get -y install fuse
```

### 安装插件

安装 Volume Plugin（卷插件）：

```shell
sudo docker plugin install juicedata/juicefs --alias juicefs
```

### 命令行下使用

使用 JuiceFS Docker 卷插件创建存储卷的过程类似于在 Docker 容器中使用 JuiceFS 客户端创建和挂载文件系统，因此需要提供数据库和对象存储的信息，以便于卷插件可以完成相应的操作。

:::tip
由于 SQLite 是单机版数据库，在宿主机创建的数据库无法被卷插件容器读取。因此，在使用 Docker 卷插件时，仅可使用基于网络链接的数据库如 Reids、MySQL 等。
:::

#### 创建存储卷

请将以下命令中的 `<VOLUME_NAME>`、`<META_URL>`、`<STORAGE_TYPE>`、`<BUCKET_NAME>`、`<ACCESS_KEY>`、`<SECRET_KEY>` 替换成你自己的文件系统配置。

```shell
sudo docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    -o storage=<STORAGE_TYPE> \
    -o bucket=<BUCKET_NAME> \
    -o access-key=<ACCESS_KEY> \
    -o secret-key=<SECRET_KEY> \
    jfsvolume
```

:::tip
通过指定不同的 `<VOLUME_NAME>` 卷名称和 `<META_URL>` 数据库，即可在同一个对象存储上创建多个文件系统。
:::

对于已经预先创建好的文件系统，在用其创建卷插件时，只需指定文件系统名称和数据库地址，例如：

```shell
sudo docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    jfsvolume
```

#### 使用存储卷

创建容器时挂载卷：

```shell
sudo docker run -it -v jfsvolume:/opt busybox ls /opt
```

#### 删除存储卷

```shell
sudo docker volume rm jfsvolume
```

### 升级和卸载卷插件

升级或卸载 Docker 卷插件之前需要先停用插件：

```shell
sudo docker plugin disable juicefs
```

升级插件：

```shell
sudo docker plugin upgrade juicefs
sudo docker plugin enable juicefs
```

卸载插件：

```shell
sudo docker plugin rm juicefs
```

### 卷插件故障排查

#### 创建的存储卷未被使用却无法删除

出现这种情况可能是在创建存储卷时设置的参数不正确，建议检查对象存储的类型、bucket 名称、Access Key、Secret Key、数据库地址等信息。可以尝试先禁用并重新启用 JuiceFS 卷插件的方式来释放掉有问题的卷，然后使用正确的参数信息重新创建存储卷。

#### 收集卷插件日志

以 systemd 为例，在使用卷插件创建存储卷时的信息会动态输出到 Docker daemon 日志，为了排查故障，可以在执行操作时另开一个终端窗口执行以下命令查看实时日志信息：

```shell
journalctl -f -u docker | grep "plugin="
```

想要了解更多 JuiceFS 卷插件内容，可以访问  [`juicedata/docker-volume-juicefs`](https://github.com/juicedata/docker-volume-juicefs) 代码仓库。

## 3. 在 Docker 容器中挂载 JuiceFS {#mount-juicefs-in-docker}

在 Docker 容器中挂载 JuiceFS 通常有两种作用，一种是为容器中的应用提供存储，另一种是把容器中挂载的 JuiceFS 存储映射给主机读写使用。为此，可以使用 JuiceFS 官方预构建的镜像，也可以自己编写 Dockerfile 将 JuiceFS 客户端打包到满足需要的系统镜像中。

### 使用预构建的镜像

[`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) 是 JuiceFS 官方维护的客户端镜像，里面同时打包了社区版和云服务客户端，程序路径分别为：

- **社区版**：`/usr/local/bin/juicefs`
- **云服务**：`/usr/bin/juicefs`

该镜像提供以下标签：

- **latest** - 包含最新的稳定版客户端
- **nightly** - 包含最新的开发分支客户端

:::tip
生产环境建议手动指定镜像的[版本标签](https://hub.docker.com/r/juicedata/mount/tags)，例如 `:v1.0.0-4.8.0`。
:::

### 手动编译镜像

某些情况下，你可能需要把 JuiceFS 客户端集成到特定的系统镜像，这时需要你自行编写 Dockerfile 文件。在此过程中，你既可以直接下载预编译的客户端，也可以参考 [`juicefs.Dockerfile`](https://github.com/juicedata/juicefs-csi-driver/blob/master/docker/juicefs.Dockerfile) 从源代码编译客户端。

以下是采用下载预编译二进制文件方式的 Dockerfile 文件示例：

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

ENTRYPOINT ["/usr/bin/juicefs", "--version"]
```

### 将容器中挂载的 JuiceFS 存储映射到宿主机

JuiceFS 可以很便利地将云上的对象存储接入本地，让你可以像使用本地磁盘一样读写云存储。而如果能把整个挂载过程放在 Docker 容器中完成，那么不但能够简化操作，也更方便日常的维护和管理。这种方式非常适合企业或家庭服务器、NAS 系统等设备创建云上数据容灾环境。

以下是一个采用 Docker Compose 实现的示例，它在 Docker 容器中完成 JuiceFS 文件系统的创建和挂载，并将容器中的挂载点映射到宿主机的 `$HOME/mnt` 目录。

#### 目录、文件和结构

该示例会在用户的 `$HOME` 目录中创建以下目录和文件：

```shell
juicefs
├── .env
├── Dockerfile
├── db
│   └── home2cloud.db
├── docker-compose.yml
└── mnt
```

以下为 `.env` 文件内容，它用来定义文件系统相关的信息，比如文件系统名称、对象存储类型、Bucket 地址、元数据地址等。这些设置均为环境变量，会在容器构建时被传递到 `docker-compose.yml` 文件中。

```.env
# JuiceFS 文件系统相关配置
JFS_NAME=home2nas
MOUNT_POINT=./mnt
STORAGE_TYPE=oss
BUCKET=https://abcdefg.oss-cn-shanghai.aliyuncs.com
ACCESS_KEY=<your-access-key>
SECRET_KEY=<your-secret-key>
METADATA_URL=sqlite3:///db/${JFS_NAME}.db
```

以下为 `docker-compose.yml` 文件，用来定义容器信息，你可以根据实际需要增加文件系统的创建和挂载相关的选项。

```yml
version: "3"
services:
  makefs:
    image: juicedata/mount
    container_name: makefs
    volumes:
      - ./db:/db
    command: ["juicefs", "format", "--storage", "${STORAGE_TYPE}", "--bucket", "${BUCKET}", "--access-key", "${ACCESS_KEY}", "--secret-key", "${SECRET_KEY}", "${METADATA_URL}", "${JFS_NAME}"]

  juicefs:
    depends_on:
      - makefs
    image: juicedata/mount
    container_name: ${JFS_NAME}
    volumes:
      - ${MOUNT_POINT}:/mnt:rw,rshared
      - ./db:/db
    cap_add:
      - SYS_ADMIN
    devices:
      - /dev/fuse
    security_opt:
      - apparmor:unconfined
    command: ["/usr/local/bin/juicefs", "mount", "${METADATA_URL}", "/mnt"]
    restart: unless-stopped
```

可以根据需要调整上述代码中 format 和 mount 命令的参数，例如，当本地与对象存储的网络连接存在一定延迟且本地存储相对可靠时，可以通过添加 `--writeback` 选项挂载文件系统，让文件可以先存储到本地缓存，再异步上传到对象存储，详情参考[客户端写缓存](../guide/cache_management.md#writeback)。

更多文件系统创建和挂载参数请查看[命令参考](../reference/command_reference.md#mount)。

#### 部署和使用

完成 `.env` 和 `docker-compose.yml` 两个文件的配置，执行命令部署容器：

```shell
sudo docker compose up -d
```

可以随时通过 logs 命令查看容器运行状态：

```shell
sudo docker compose logs -f
```

如果需要停止容器，可以执行 stop 命令：

```shell
sudo docker compose stop
```

如果需要销毁容器，可以执行 down 命令：

```shell
sudo docker compose down
```

#### 注意事项

- 当前示例使用的是单机数据库 SQLite，数据库文件会保存在 `$HOME/juicefs/db/` 目录下，请妥善保管数据库文件。
- 如果需要使用其他数据库，直接调整 `.env` 文件中 `METADATA_URL` 的值即可，比如设置使用 Reids 作为元数据存储： `METADATA_URL=redis://192.168.1.11/1`。
- JuiceFS 客户端每小时都会自动备份一次元数据，备份的数据会以 JSON 格式导出并上传到对象存储的 `meta` 目录中。一旦数据库发生故障，可以使用最新的备份进行恢复。
