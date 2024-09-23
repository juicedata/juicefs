---
title: 在 KubeSphere 上使用 JuiceFS
sidebar_position: 3
slug: /juicefs_on_kubesphere
---

[KubeSphere](https://kubesphere.com.cn) 是在 Kubernetes 之上构建的以应用为中心的多租户容器平台，提供全栈的 IT 自动化运维的能力，简化企业的 DevOps 工作流。

KubeSphere 提供了运维友好的向导式操作界面，即便是 Kubernetes 经验并不丰富的用户，也能相对轻松的上手开始管理和使用。它提供了基于 Helm 的应用市场，可以在图形化界面下非常轻松地安装各种 Kubernetes 应用。

本文将介绍如何在 KubeSphere 中一键部署 JuiceFS CSI Driver，为集群上的各种应用提供数据持久化。

## 前提条件

1. 安装 KubeSphere

   安装 KubeSphere 有两种方法。一是在 Linux 上直接安装，可以参考文档：[在 Linux 安装 KubeSphere](https://kubesphere.com.cn/docs/quick-start/all-in-one-on-linux) ；
二是在已有 Kubernetes 中安装，可以参考文档：[在 Kubernetes 安装 KubeSphere](https://kubesphere.com.cn/docs/quick-start/minimal-kubesphere-on-k8s) 。

2. 在 KubeSphere 中启用应用商店

   在 KubeSphere 中启用应用商店可以参考文档：[KubeSphere 应用商店](https://kubesphere.com.cn/docs/pluggable-components/app-store) 。

## 安装 JuiceFS CSI Driver

如果 KubeSphere 的版本为 v3.2.0 及以上，可以直接在应用商店中安装 CSI Driver，跳过「配置应用模板/应用仓库」步骤，直接进入「安装」步骤；如果 KubeSphere 版本低于 v3.2.0，按照以下步骤配置应用模板/应用仓库。

### 配置应用模板/应用仓库

安装 JuiceFS CSI Driver 首先需要创建应用模板，这里有两种方法。

#### 方法一：应用仓库

在企业空间中点击进去应用管理，选择「应用仓库」，点击创建按钮添加 JuiceFS CSI 仓库，填写：

- 仓库名称：`juicefs-csi-driver`
- Index URL：`https://juicedata.github.io/charts/`

![kubesphere_app_shop](../images/kubesphere_app_shop.png)

#### 方法二：应用模板

先在 JuiceFS CSI Driver 仓库下载 chart 压缩包：[https://github.com/juicedata/juicefs-csi-driver/releases](https://github.com/juicedata/juicefs-csi-driver/releases)。

在「企业空间」中点击进入「应用管理」，选择「应用模板」，点击「创建」，上传 chart 压缩包：

![kubesphere_app_template](../images/kubesphere_app_template.png)

### 安装

在「企业空间」中选择您所需部署的「项目」（KubeSphere 中的项目即为 K8s 中的 namespace），选择「应用负载」，点击「部署新应用」按钮，选择「来自应用商店」，然后选择 `juicefs`：

![kubesphere_shop_juicefs](../images/kubesphere_shop_juicefs.jpg)

若 KubeSphere 版本低于 v3.2.0，根据上一步配置好的应用模板，选择部署应用「来自应用模板」：

![kubesphere_install_csi](../images/kubesphere_install_csi.png)

进入配置修改页面后一致，修改以下两个地方：

- namespace：改成对应的项目名
- storageClass.backend：
  `backend` 部分用来定义文件系统后端的数据库和对象存储，可以查阅[创建文件系统](../getting-started/standalone.md#juicefs-format)了解相关内容。

您也可以通过 KubeSphere 的应用商店快速创建数据库（如 Redis）和对象存储（如 MinIO）。
比如在 KubeSphere 平台搭建 Redis：在当前所在项目中选择「应用负载」，点击「部署新应用」按钮，选择「来自应用商店」，选择「Redis」，然后快速部署即可。Redis 的访问 URL 可以通过部署好的应用的服务名，如下：

![kubesphere_redis](../images/kubesphere_redis.png)

在 KubeSphere 平台搭建 MinIO 也是类似的流程，不过在部署 MinIO 之前可以修改 MinIO 的 accessKey 和 secretKey，并且需要记住配置的值。如下图：

![kubesphere_create_minio](../images/kubesphere_create_minio.png)

> 注：如果部署 MinIO 出现权限问题，可以将配置中的 `securityContext.enables` 设置为 false。

MinIO 的访问 URL 可以通过部署好的应用的服务名，如下：

![kubesphere_minio](../images/kubesphere_minio.png)

Redis 和 MinIO 都搭建好之后，就可以填写 JuiceFS CSI Driver 的 `backend` 值了。其中：

1. `metaurl` 为刚才创建的 Redis 的数据库地址，Redis 的访问地址可用 Redis 应用对应的服务名，如 `redis://redis-rzxoz6:6379/1`
2. `storage` 为对象存储的类型，如 `minio`
3. `bucket` 为刚才创建的 MinIO 的可用 bucket（JuiceFS 会自动创建，不需要手动创建），MinIO 的访问地址可用 MinIO 应用对应的服务名，如 `http://minio-qkp9my:9000/minio/test`
4. `accessKey` 和 `secretKey` 用刚才创建的 MinIO 的 accessKey 和 secretKey

![kubesphere_update_csi](../images/kubesphere_update_csi.png)

配置修改完毕后，点击安装即可。

## 使用

### 部署应用

按照上述方法安装好的 JuiceFS CSI Driver 已经创建好一个 `StorageClass`，名为上述 `storageClass` 的 `name`，比如上述创建的 `StorageClass` 为 `juicefs-sc`，可以直接使用。

然后需要创建一个 PVC，指定使用 `juicefs-sc` 这个 `StorageClass`。在「项目」中，选择「存储管理」，再选择「存储卷」，点击「创建」按钮创建 PVC，其中「存储类型」选择 `juicefs-sc`，如下：

![kubesphere_pvc](../images/kubesphere_pvc.png)

PVC 创建好之后，再在「项目」的「应用负载」中，选择「工作负载」，点击「创建」按钮部署工作负载，其中「基本信息」页填写自己喜欢的名字；「容器镜像」页可以填写镜像 `centos` ；
启动命令 `sh,-c,while true; do echo $(date -u) >> /data/out.txt; sleep 5; done` ；「存储卷来源」选择「已有存储卷」，再选择上一步创建的 PVC，容器内路径填写 `/data` 如下：

![kubesphere_deployment](../images/kubesphere_deployment.png)

![kubesphere_workload](../images/kubesphere_workload.png)

部署完成后可以看到运行中的容器组：

![kubesphere_pod](../images/kubesphere_pod.png)

### 新建 StorageClass

若安装 JuiceFS CSI Driver 的时候没有创建 `StorageClass`，或者需要另外新建，可以遵循以下步骤：

准备好元数据服务和对象存储服务后，新建一个 `Secret`。在「平台管理」页面选择「配置中心」，选择「密钥」，点击「创建」按钮新建：

![kubesphere_create_secret](../images/kubesphere_create_secret.png)

「密钥设置」中填入准备好的元数据服务和对象存储信息，如下：

![kubesphere_update_secret](../images/kubesphere_update_secret.png)

`Secret` 新建好之后，创建 `StorageClass`，在「平台管理」页面选择「存储管理」，选择「存储类型」，点击「创建」按钮新建，其中「存储系统」选择「自定义」：

![kubesphere_sc_create](../images/kubesphere_sc_create.png)

设置页面信息如下，其中「存储系统」填写 `csi.juicefs.com`，另外再设置 4 个参数：

- `csi.storage.k8s.io/provisioner-secret-name`: 刚刚创建好的 secret name
- `csi.storage.k8s.io/provisioner-secret-namespace`: secret 对应的项目名
- `csi.storage.k8s.io/node-publish-secret-name`: 刚刚创建好的 secret name
- `csi.storage.k8s.io/node-publish-secret-namespace`: secret 对应的项目名

![kubesphere_sc_update](../images/kubesphere_sc_update.png)

点击「创建」按钮之后，`StorageClass` 就创建好了。
