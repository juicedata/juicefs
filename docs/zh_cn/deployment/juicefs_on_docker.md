---
title: 在 Docker 中使用 JuiceFS
sidebar_position: 6
slug: /juicefs_on_docker
description: 在 Docker 中以不同方式使用 JuiceFS，包括卷映射、卷插件，以及容器中挂载。
---

最简单的用法是卷映射，在宿主机上挂载 JuiceFS，然后映射进容器里即可。注意宿主机如果不是使用 root 进行挂载，需要启用 [`allow_other`](../reference/fuse_mount_options.md#allow_other)，容器内方可正常访问。

```shell
docker run -d --name nginx \
  -v /jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

如果你对挂载管理有着更高的要求，比如希望通过 Docker 来管理挂载点，方便不同的应用容器使用不同的 JuiceFS 文件系统，还可以通过[卷插件](https://github.com/juicedata/docker-volume-juicefs)（Docker volume plugin）与 Docker 引擎集成。

## 卷插件 {#volume-plugin}

在 Docker 中，插件也是一个容器镜像，JuiceFS 卷插件镜像中内置了 [JuiceFS 社区版](../introduction/README.md)以及 [JuiceFS 企业版](https://juicefs.com/docs/zh/cloud)客户端，安装以后，便能够运行卷插件，在 Docker 中创建 JuiceFS Volume。

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

### 通过 Docker Compose 挂载 {#using-docker-compose}

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
# 启动服务
docker-compose up

# 关闭服务并从 Docker 中卸载 JuiceFS 文件系统
docker-compose down --volumes
```

### 排查 {#troubleshooting}

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

## 在 Docker 容器中挂载 JuiceFS {#mount-juicefs-in-docker}

在 Docker 容器中挂载 JuiceFS 通常有两种作用，一种是为容器中的应用提供存储，另一种是把容器中挂载的 JuiceFS 存储映射给主机读写使用。为此，可以使用 JuiceFS 官方维护的镜像，也可以自己编写 Dockerfile 将 JuiceFS 客户端打包进镜像中。详见[「定制容器镜像」](https://juicefs.com/docs/zh/csi/guide/custom-image)。
