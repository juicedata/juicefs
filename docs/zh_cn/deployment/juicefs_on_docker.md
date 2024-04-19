---
title: 在 Docker 中使用 JuiceFS
sidebar_position: 6
slug: /juicefs_on_docker
description: 在 Docker 中以不同方式使用 JuiceFS，包括卷映射、卷插件，以及容器中挂载。
---

在 Docker 中使用 JuiceFS 文件系统，可以通过卷插件或直接在容器中运行客户端。

## 使用卷插件 {#volume-plugin}

如果你对挂载管理有一定要求，比如希望通过 Docker 来管理挂载点，方便不同的应用容器使用不同的 JuiceFS 文件系统，则可以使用[卷插件](https://github.com/juicedata/docker-volume-juicefs)（Docker volume plugin）。

Docker 插件通常是以镜像形式提供的，[JuiceFS 卷插件镜像](https://hub.docker.com/r/juicedata/juicefs)中内置了 [JuiceFS 社区版](../introduction/README.md)和 [JuiceFS 云服务](https://juicefs.com/docs/zh/cloud)客户端，安装以后，便能够运行卷插件，在 Docker 中创建 JuiceFS Volume。

通过下面的命令安装插件，按照提示为 FUSE 提供必要的权限：

```shell
docker plugin install juicedata/juicefs
```

你可以使用以下命令管理卷插件：

```shell
# 停用插件
docker plugin disable juicedata/juicefs

# 升级插件（需先停用）
docker plugin upgrade juicedata/juicefs
docker plugin enable juicedata/juicefs

# 卸载插件
docker plugin rm juicedata/juicefs
```

### 创建存储卷 {#create-volume}

请将以下命令中的 `<VOLUME_NAME>`、`<META_URL>`、`<STORAGE_TYPE>`、`<BUCKET_NAME>`、`<ACCESS_KEY>`、`<SECRET_KEY>` 替换成你自己的文件系统配置。

```shell
docker volume create -d juicedata/juicefs \
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
docker volume create -d juicedata/juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  jfsvolume
```

如果需要在挂载文件系统时传入额外的环境变量（比如 [Google 云](../reference/how_to_set_up_object_storage.md#google-cloud)），可以对上方命令追加类似 `-o env=FOO=bar,SPAM=egg` 的参数。

### 使用和管理 {#usage-and-management}

```shell
# 创建容器时挂载卷
docker run -it -v jfsvolume:/opt busybox ls /opt

# 卸载后，可以操作删除存储卷，注意这仅仅是删除 Docker 中的对应资源，并不影响 JuiceFS 中存储的数据
docker volume rm jfsvolume
```

### 在 Docker Compose 中使用卷插件  {#using-plugin-in-docker-compose}

下面是在 `docker compose` 中使用 JuiceFS 卷插件的示例：

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
      # 因为 SQLite 在插件容器本地路径创建数据库文件，
      # sqlite:// 将在服务重启时失败。
      # （详见 https://github.com/juicedata/docker-volume-juicefs/issues/37）
      metaurl: ${META_URL}
      storage: ${STORAGE_TYPE}
      bucket: ${BUCKET}
      access-key: ${ACCESS_KEY}
      secret-key: ${SECRET_KEY}
      # 如有需要，可以用 env 传入额外环境变量
      # env: FOO=bar,SPAM=egg
```

使用和管理：

```shell
# 启动服务
docker-compose up

# 关闭服务并从 Docker 中卸载 JuiceFS 文件系统
docker-compose down --volumes
```

### 卷插件问题排查 {#troubleshooting}

无法正常工作时，推荐先[升级卷插件](#volume-plugin)，然后根据问题情况查看日志。

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

## 在容器中使用 JuiceFS 客户端 {#mount-juicefs-in-docker}

相比卷插件，直接在容器中使用 JuiceFS 客户端更加灵活，可以在容器中直接挂载 JuiceFS 文件系统，也可以通过 S3 Gateway、WebDAV 开放文件系统访问。

### 方式一：自行构建镜像

JuiceFS 客户端是一个独立的二进制程序，同时提供 AMD64 和 ARM64 架构的版本，可以在 Dockerfile 中定义下载安装 JuiceFS 客户端的命令，例如：

```Dockerfile
FROM ubuntu:22.04
...
# 使用官方一键安装脚本
RUN curl -sSL https://d.juicefs.com/install | sh - 
```

更多内容详见[「定制容器镜像」](https://juicefs.com/docs/zh/csi/guide/custom-image)。

### 方式二：使用官方维护的镜像

JuiceFS 官方维护的镜像 [`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) ，可以通过 tag 指定所需要的版本。**社区版 tag 为 ce**，例如：latest、ce-v1.1.2、ce-nightly。`latest` 标签仅包含最新的社区版，`nightly` 标签指向最新的开发版本，详情查看 [Docker hub 的 tags 页面](https://hub.docker.com/r/juicedata/mount/tags)。

开始之前，你需要先准备好[对象存储](../reference/how_to_set_up_object_storage.md)和[元数据引擎](../reference/how_to_set_up_metadata_engine.md)。

#### 创建文件系统

通过一个临时容器创建文件系统，例如：

```sh
docker run --rm \
    juicedata/mount:ce-v1.1.2 juicefs format \
    --storage s3 \
    --bucket https://xxx.your-s3-endpoint.com \
    --access-key=ACCESSKEY \
    --secret-key=SECRETKEY \
    rediss://user:password@xxx.your-redis-server.com:6379/1 myjfs
```

请将 `--storage`、`--bucket`、`--access-key`、`--secret-key` 以及元数据引擎的 URL 替换成你自己的配置。

#### 直接在容器中挂载文件系统

创建一个容器并将 JuiceFS 文件系统到挂载到容器中，例如：

```sh
docker run --privileged --name myjfs \
    juicedata/mount:ce-v1.1.2 juicefs mount \
    rediss://user:password@xxx.your-redis-server.com:6379/1 /mnt
```

请将元数据引擎的 URL 替换成你自己的配置，`/mnt` 是挂载点，可以根据需要修改。由于需要使用 FUSE，所以还需要 `--privileged` 权限。

#### 通过 Docker Compose 挂载文件系统

下面是一个使用 Docker Compose 的示例，请将元数据引擎的 URL 和挂载点替换成你自己的配置。

```yaml
version: "3"
services:
    juicefs:
      image: juicedata/mount:ce-v1.1.2
      container_name: myjfs
      volumes:
        - ./mnt:/mnt:rw,rshared
      cap_add:
        - SYS_ADMIN
      devices:
        - /dev/fuse
      security_opt: 
        - apparmor:unconfined
      command: ["juicefs", "mount", "rediss://user:password@xxx.your-redis-server.com:6379/1", "/mnt"]
      restart: unless-stopped
```

在容器中，JuiceFS 文件系统挂载到了 `/mnt` 目录，又通过配置文件中的 volumes 部分将容器中的 `/mnt` 映射到宿主机的 `./mnt` 目录，这样就可以实现在宿主机直接访问容器中挂载的 JuiceFS 文件系统。

#### 通过 S3 Gateway 开放文件系统访问

下面是一个将 JuiceFS 以 S3 Gateway 方式开放访问的示例，请将 `MINIO_ROOT_USER`、`MINIO_ROOT_PASSWORD`、元数据引擎的 URL、监听的地址和端口号替换成你自己的配置。

```yaml
version: "3"
services:
    s3-gateway:
      image: juicedata/mount:ce-v1.1.2
      container_name: juicefs-s3-gateway
      environment:
        - MINIO_ROOT_USER=your-username
        - MINIO_ROOT_PASSWORD=your-password
      ports:
        - "9090:9090"
      command: ["juicefs", "gateway", "rediss://user:password@xxx.your-redis-server.com:6379/1", "0.0.0.0:9090"]
      restart: unless-stopped
```

使用宿主机的 `9090` 端口即可打开 S3 Gateway 的控制台，用相同的地址通过 S3 客户端或者 SDK 读写 JuiceFS 文件系统。
