---
title: 在 Docker 中使用 JuiceFS
sidebar_position: 3
slug: /juicefs_on_docker
description: 在 Docker 中以不同方式使用 JuiceFS，包括卷映射、卷插件，以及容器中挂载。
---

最简单的用法是卷映射，在宿主机上挂载 JuiceFS，然后映射进容器里即可，假定挂载点为 `/jfs`：

```shell
docker run -d --name nginx \
  -v /jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

如果你对挂载管理有着更高的要求，比如希望通过 Docker 来管理挂载点，方便不同的应用容器使用不同的 JuiceFS 文件系统，还可以通过[卷插件](https://github.com/juicedata/docker-volume-juicefs)（Docker volume plugin）与 Docker 引擎集成。

## 卷插件

在 Docker 中，插件也是一个个容器镜像，JuiceFS 卷插件镜像中内置了 [JuiceFS 社区版](../introduction/README.md)以及 [JuiceFS 云服务](https://juicefs.com/docs/zh/cloud/)客户端，安装以后，便能够运行卷插件，在 Docker 中创建 JuiceFS Volume。

通过下面的命令安装插件，按照提示为 FUSE 提供必要的权限。

```shell
docker plugin install juicedata/juicefs
```

### 创建存储卷

请将以下命令中的 `<VOLUME_NAME>`、`<META_URL>`、`<STORAGE_TYPE>`、`<BUCKET_NAME>`、`<ACCESS_KEY>`、`<SECRET_KEY>` 替换成你自己的文件系统配置。

```shell
docker volume create -d juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  -o storage=<STORAGE_TYPE> \
  -o bucket=<BUCKET_NAME> \
  -o access-key=<ACCESS_KEY> \
  -o secret-key=<SECRET_KEY> \
  jfsvolume
```

对于已经预先创建好的文件系统，在用其创建卷插件时，只需指定文件系统名称和数据库地址，例如：

```shell
docker volume create -d juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  jfsvolume
```

如果需要在认证、挂载文件系统时传入额外的环境变量（比如 [Google Cloud Platform](../guide/how_to_set_up_object_storage.md#google)），可以对上方命令追加类似 `-o env=FOO=bar,SPAM=egg` 的参数。

### 使用和管理

```shell
# 创建容器时挂载卷
docker run -it -v jfsvolume:/opt busybox ls /opt

# 卸载后，可以操作删除存储卷，注意这仅仅是删除 Docker 中的对应资源，并不影响 JuiceFS 中存储的数据
docker volume rm jfsvolume

# 停用插件
docker plugin disable juicedata/juicefs

# 升级插件（需先停用）
docker plugin upgrade juicedata/juicefs
docker plugin enable juicedata/juicefs

# 卸载
docker plugin rm juicedata/juicefs
```

## 通过 Docker Compose 挂载

下面是使用 `docker-compose` 挂载 JuiceFS 文件系统的例子：

```yaml
version: '3'
services:
busybox:
  image: busybox
  command: "ls /jfs"
  volumes:
    - jfsvolume:/jfs
volumes:
  jfsvolume:
    driver: juicedata/juicefs
    driver_opts:
      name: ${VOL_NAME}
      metaurl: ${META_URL}
      storage: ${STORAGE_TYPE}
      bucket: ${BUCKET}
      access-key: ${ACCESS_KEY}
      secret-key: ${SECRET_KEY}
      # 如有需要，可以用 env 传入额外环境变量
      # env: FOO=bar,SPAM=egg
```

配置文件撰写完毕，可以通过下方命令创建和管理：

```shell
docker-compose up

# 关闭服务并从 Docker 中卸载 JuiceFS 文件系统
docker-compose down --volumes
```

### 排查

无法正常工作时，推荐先升级 [Docker volume plugin](https://hub.docker.com/r/juicedata/juicefs/tags)，然后根据问题情况查看日志。

* 收集 JuiceFS 客户端日志，日志位于 Docker volume plugin 容器内，需要进入容器采集：

  ```shell
  # 确认 docker plugins runtime 目录，根据实际情况可能与下方示范不同
  # ls 打印出来的目录就是容器目录，名称为容器 ID
  ls /run/docker/plugins/runtime-root/plugins.moby

  # 打印 plugin 容器信息
  # 如果打印出的容器列表为空，说明 plugin 容器创建失败
  # 阅读下方查看 plugin 启动日志继续排查
  runc --root /run/docker/plugins/runtime-root/plugins.moby list

  # 进入容器，打印日志
  runc --root /run/docker/plugins/runtime-root/plugins.moby exec 452d2c0cf3fd45e73a93a2f2b00d03ed28dd2bc0c58669cca9d4039e8866f99f cat /var/log/juicefs.log
  ```

  如果发现容器不存在（`ls` 发现目录为空），或者在最后打印日志的阶段发现 `juicefs.log` 不存在，那么多半是挂载本身就失败了，继续查看 plugin 自身的日志寻找原因。

* 收集 plugin 日志，以 systemd 为例：

  ```shell
  journalctl -f -u docker | grep "plugin="
  ```

  如果 plugin 调用 `juicefs` 发生错误，或者 plugin 自身报错，均会在日志里有所体现。

## 在 Docker 容器中挂载 JuiceFS {#mount-juicefs-in-docker}

在 Docker 容器中挂载 JuiceFS 通常有两种作用，一种是为容器中的应用提供存储，另一种是把容器中挂载的 JuiceFS 存储映射给主机读写使用。为此，可以使用 JuiceFS 官方预构建的镜像，也可以自己编写 Dockerfile 将 JuiceFS 客户端打包到满足需要的系统镜像中。

### 使用 Mount Pod 容器镜像

[`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) 是 JuiceFS 官方维护的客户端镜像，同时也是 [CSI 驱动](https://juicefs.com/docs/zh/csi/introduction/)中负责挂载 JuiceFS 文件系统的容器镜像，里面同时打包了社区版和云服务客户端，程序路径分别为：

- 社区版：`/usr/local/bin/juicefs`
- 云服务：`/usr/bin/juicefs`

该镜像提供以下标签：

- `latest` - 包含最新的稳定版客户端
- `nightly` - 包含最新的开发分支客户端
- 形如 `v1.0.4-4.9.0` - 标签中同时指定了社区版和云服务客户端版本号，建议生产环境使用该标签，显式指定客户端版本

### 手动编译镜像

如果需要把 JuiceFS 客户端集成到特定的系统镜像，这时可以自行编写 Dockerfile。在此过程中，你既可以直接下载预编译的客户端，也可以参考 [`juicefs.Dockerfile`](https://github.com/juicedata/juicefs-csi-driver/blob/master/docker/juicefs.Dockerfile) 从源代码编译客户端。

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
docker compose up -d
```

可以随时通过 logs 命令查看容器运行状态：

```shell
docker compose logs -f
```

如果需要停止容器，可以执行 stop 命令：

```shell
docker compose stop
```

如果需要销毁容器，可以执行 down 命令：

```shell
docker compose down
```
