---
title: 在 AWS 上使用 JuiceFS
sidebar_position: 4
slug: /clouds/aws
---

亚马逊云（AWS）是全球领先的云计算平台，提供几乎所有类型的云计算服务。AWS 丰富的产品线，为创建和使用 JuiceFS 文件系统提供了灵活的选择。

## 可以在哪里使用 JuiceFS {#where-can-juicefs-be-used}

JuiceFS 具有丰富的 API 接口，对 AWS 而言，通常可以在以下产品中使用：

- **Amazon EC2**：通过挂载 JuiceFS 文件系统来使用
- **Amazon Elastic Kubernetes Service（EKS）**：通过 JuiceFS CSI 驱动使用
- **Amazon EMR**：通过 JuiceFS Hadoop Java SDK 使用

## 准备 {#preparation}

一个 JuiceFS 文件系统由两部分组成：

1. **对象存储**：用于数据存储
2. **元数据引擎**：用于元数据存储的数据库

可以根据具体需求，选择在 AWS 上使用全托管的数据库和 S3 对象存储，或者在 EC2、EKS 上自行部署。

:::tip
本文着重介绍使用 AWS 全托管的服务创建 JuiceFS 文件系统的方法，对于自托管的情况，请查阅[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)和[「JuiceFS 支持的对象存储」](../reference/how_to_set_up_object_storage.md)以及相应程序文档。
:::

### 对象存储 {#object-storage}

S3 是 AWS 提供的对象存储服务，可以根据需要在相应地区创建 bucket，也可以通过 [IAM 角色授权](../reference/how_to_set_up_object_storage.md#aksk)让 JuiceFS 客户端自动创建 bucket。

Amazon S3 提供多种[存储类](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/storage-class-intro.html)，例如：

- **S3 Standard**：标准存储，适用于频繁访问数据的通用型存储，实时访问，无取回费用。
- **S3 Standard-IA**：低频存储，适用于长期需要但访问频率不太高的数据，实时访问，有取回费用。
- **S3 Glacier**：归档存储，适用于长期存档几乎不访问的数据，访问前需解冻。

你可以在创建或者挂载 JuiceFS 文件系统时设置存储类，具体请参考[文档](../reference/how_to_set_up_object_storage.md#storage-class)。建议优先选择标准的存储类，其他的存储类虽然有更低的单位存储价格，但会涉及最低存储时长要求和检索（取回）费用。

另外，访问对象存储服务需要通过 Access Key（也叫 access key ID）和 Secret Key（也叫 secret access key）验证用户身份，可以参照文档[「管理 IAM 用户的访问密钥」](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_credentials_access-keys.html)进行创建。当通过 EC2 云服务器访问 S3 时，还可以为 EC2 分配 [IAM 角色](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_roles.html)，实现在 EC2 上免密钥调用 S3 API。

### 数据库 {#database}

AWS 提供了多种基于网络的全托管数据库，可以用于构建 JuiceFS 的元数据引擎，主要有：

- **Amazon MemoryDB for Redis**（以下简称 MemoryDB）：持久的 Redis 内存数据库服务，可提供超快的性能。
- **Amazon RDS**：全托管的 MariaDB、MySQL、PostgreSQL 等数据库。

:::note 注意
虽然 Amazon ElastiCache for Redis（以下简称 ElastiCache）也提供兼容 Redis 协议的服务，但是相比 MemoryDB 来说，ElastiCache 无法提供「强一致性保证」，因此更推荐使用 MemoryDB。
:::

## 在 EC2 上使用 JuiceFS {#using-juicefs-on-ec2}

### 安装 JuiceFS 客户端 {#installing-the-juicefs-client}

请根据 EC2 所使用的操作系统，参考[安装](../getting-started/installation.md)文档安装最新的 JuiceFS 社区版客户端。

这里以 Linux 系统为例，使用一键安装脚本自动安装客户端：

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

### 创建文件系统 {#creating-a-file-system}

#### 准备对象存储 {#preparing-object-storage}

可以通过创建一个拥有 [AmazonS3FullAccess](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/security-iam-awsmanpol.html#security-iam-awsmanpol-amazons3fullaccess) 权限的 IAM 角色分配给 EC2，从而无需使用 Access Key 和 Secret Key 即可直接在 EC2 上创建和使用 S3 Bucket。

#### 准备数据库 {#preparing-the-database}

这里以 MemoryDB 为例，请参考[「Redis 最佳实践」](../administration/metadata/redis_best_practices.md)及 AWS 文档创建数据库。

为了让 EC2 能够访问 Redis 集群，需要将它们创建在相同的 VPC，或者为 Redis 集群的安全组添加规则允许 EC2 实例访问。

:::note 注意
如果创建的是 Redis 7.0 版本集群，需要安装 JuiceFS v1.1 及以上版本客户端。
:::

#### 格式化文件系统 {#formatting-file-system}

```shell
juicefs format --storage s3 \
  --bucket https://s3.ap-east-1.amazonaws.com/myjfs \
  rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
  myjfs
```

### 挂载文件系统 {#mounting-file-system}

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

### 开机自动挂载 {#mounting-at-boot}

请参考文档[启动时自动挂载 JuiceFS](../administration/mount_at_boot.md)。

## 在 Amazon EKS 上使用 JuiceFS {#using-juicefs-on-amazon-eks}

Amazon EKS 支持[三种节点类型](https://docs.aws.amazon.com/zh_cn/eks/latest/userguide/eks-compute.html)：

- **EKS 托管节点组**：使用 Amazon EC2 作为计算节点
- **自行管理的节点**：使用 Amazon EC2 作为计算节点
- **Fargate**：一个无服务器的计算引擎

Fargate 类型节点暂不支持安装 JuiceFS CSI 驱动，请使用「EKS 托管节点组」或者「自行管理的节点」类型。

Amazon EKS 是标准的 Kubernetes 集群，可以使用 `eksctl`、`kubectl`、`helm` 等工具进行管理，请查阅 [JuiceFS CSI 驱动文档](/docs/zh/csi/introduction)了解如何安装和使用。

## 在 Amazon EMR 上使用 JuiceFS {#using-juicefs-on-amazon-emr}

请参考文档[「在 Hadoop 生态使用 JuiceFS」](../deployment/hadoop_java_sdk.md)。
