---
title: 在 AWS 上使用 JuiceFS
sidebar_position: 6
slug: /clouds/aws
---

亚马逊 AWS 是全球领先的云计算平台，提供几乎所有类型的云计算服务。AWS 丰富的产品线，为创建和使用 JuiceFS 文件系统提供了灵活的选择。

## 可以在哪里使用 JuiceFS

JuiceFS 具有丰富的 API 接口，对 AWS 而言，通常可以在以下产品中使用：

- **Amazon EC2** - 通过 FUSE 接口挂载使用
- **Amazon EKS** - 通过 JuiceFS CSI Driver 使用
- **Amazon EMR** - 通过 JuiceFS Hadoop Java SDK 使用

## 准备

一个 JuiceFS 文件系统由两部分组成：

1. **对象存储**：用于数据存储
2. **元数据引擎**：用于元数据存储的数据库

可以根据具体需求，选择在 AWS 上使用全托管的数据库和 S3 对象存储，或者在 EC2、EKS 上自行部署。

> 本文着重介绍使用 AWS 全托管的服务创建 JuiceFS 文件系统的方法，对于自托管的情况，请查阅《[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)》和《[JuiceFS 支持的对象存储](../guide/how_to_set_up_object_storage.md)》以及相应程序文档。

### 对象存储

S3 是 AWS 提供的对象存储服务，可以根据需要在相应地区创建 bucket，也可以通过 IAM 角色授权让 JuiceFS 客户端自动创建 bucket。

另外，还可以使用任何 [JuiceFS 支持的对象存储](../guide/how_to_set_up_object_storage.md)，只要确保所选择的对象存储可以通过互联网被 AWS 的服务正常访问即可。

Amazon S3 提供以下几种存储类型（仅供参考，请以 AWS 官方数据为准）：

- **Amazon S3 STANDARD**：标准存储，适用于频繁访问数据的通用型存储，实时访问，无取回费用。
- **Amazon S3 STANDARD_IA**：低频存储，适用于长期需要但访问频率不太高的数据，实时访问，有取回费用。
- **S3 Glacier**：归档存储，适用于长期存档几乎不访问的数据，访问前需解冻。

所有支持“实时访问”的对象存储类型都可以用于构建 JuiceFS 文件系统，由于 S3 Glacier 需要先解冻数据才能访问（无法实时访问），因此无法用于构建 JuiceFS 文件系统。

在存储类型方面，应该优先选择标准类型的 S3，其他的存储类型虽然有更低的单位存储价格，但会涉及最低存储时长要求和检索（取回）费用。

另外，访问对象存储服务需要通过 `access key` 和 `secret key` 验证用户身份，可以参照文档[《使用用户策略控制对存储桶的访问》](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/walkthrough1.html)进行创建。当通过 EC2 云服务器访问 S3 时，还可以为 EC2 分配 [IAM 角色](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_roles.html)，实现在 EC2 上免密钥调用 S3 API。

### 数据库

AWS 提供了多种基于网络的全托管数据库，可以用于构建 JuiceFS 文件系统的主要有：

- **Amazon MemoryDB for Redis**：持久的 Redis 内存数据库服务，可提供超快的性能。
- **Amazon RDS**：全托管的 MariaDB、MySQL、PostgresSQL 等数据库。

另外，还可以使用第三方提供的全托管数据库，只要确保数据库能够通过互联网被 AWS 正常访问即可。在环境支持的情况下，还可以使用单机版的 SQLite 或 BadgerDB 数据库。

## 在 EC2 上使用 JuiceFS

### 安装 JuiceFS 客户端

请根据 EC2 所使用的操作系统，参考[安装](../getting-started/installation.md)文档安装最新的 JuiceFS 社区版客户端。

这里以 Linux 系统为例，使用一键安装脚本自动安装客户端：

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

### 创建文件系统

#### 准备对象存储

可以通过创建一个拥有 [AmazonS3FullAccess](https://us-east-1.console.aws.amazon.com/iamv2/home?region=ap-east-1#/policies/details/arn%3Aaws%3Aiam%3A%3Aaws%3Apolicy%2FAmazonS3FullAccess) 权限的 IAM 角色分配给 EC2，从而无需使用 Access Key 和 Secret Key 即可直接在 EC2 上创建和使用 S3 Bucket。

如果希望使用 Access Key 和 Secret Key 对 S3 的访问进行认证，可以在 IAM 中创建用户，并在安全凭证中“创建访问密钥”。

#### 准备数据库

这里以 MemoryDB for Redis 为例，为了让 EC2 能够访问 Redis 集群，需要将它们创建在相同的 VPC，或者为 Redis 集群的安全组添加规则允许 EC2 实例访问。

> **提示**：如果创建的是 Redis 7.0 版本集群，需要安装 JuiceFS v1.1 及以上版本客户端。

#### 格式化文件系统

```shell
juicefs format --storage s3 \
--bucket https://s3.ap-east-1.amazonaws.com/myjfs \
rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
myjfs
```

#### 挂载使用

```shell
sudo juicefs mount -d \
rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
/mnt/myjfs
```

对于通过 IAM 角色授权 S3 访问创建的文件系统，如果需要在 AWS 外部挂载使用，需要使用 `juicefs config` 为文件系统添加 Access Key 和 Secret Key：

```shell
juicefs config \
--access-key=<your-access-key> \
--secret-key=<your-secret-key> \
rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1
```

#### 开机自动挂载

请参考文档[启动时自动挂载 JuiceFS](../administration/mount_at_boot.md)。

## 在 EKS 上使用 JuiceFS

Amazon EKS 支持两种节点类型：

- **Fargate** - 一个无服务器的计算引擎
- **Managed nodes** -  使用 Amazon EC2 作为计算节点

Fargate 类型节点集群暂不支持安装 JuiceFS CSI Drive，请创建使用 Managed nodes 类型节点的集群。

Amazon EKS 是标准的 Kubernetes 集群，可以使用 eksctl、kubectl、helm 等工具进行管理，请查阅 [JuiceFS CSI Driver 文档](https://juicefs.com/docs/zh/csi/getting_started)安装和使用。

## 在 EMR 上使用 JuiceFS

请参考文档[在 Hadoop 生态使用 JuiceFS](../deployment/hadoop_java_sdk.md)。
