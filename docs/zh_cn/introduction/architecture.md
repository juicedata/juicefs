---
sidebar_label: 技术架构
sidebar_position: 2
slug: /architecture
---
# 技术架构

本文介绍 JuiceFS 的核心架构，以及 JuiceFS 存储文件的原理。

## 核心架构

JuiceFS 文件系统由三个部分组成：

- **JuiceFS 客户端**：协调对象存储和元数据存储引擎，以及 POSIX、Hadoop、Kubernetes CSI Driver、S3 Gateway 等文件系统接口的实现；
- **数据存储**：存储数据本身，支持本地磁盘、公有云或私有云对象存储、HDFS 等介质；
- **元数据引擎**：存储数据对应的元数据（metadata）包含文件名、文件大小、权限组、创建修改时间和目录结构，支持 Redis、MySQL、TiKV 等多种引擎；

![image](../images/juicefs-arch-new.png)

作为文件系统，JuiceFS 会分别处理数据及其对应的元数据，数据会被存储在对象存储中，元数据会被存储在元数据服务引擎中。

在 **数据存储** 方面，JuiceFS 支持几乎所有的公有云对象存储，同时也支持 OpenStack Swift、Ceph、MinIO 等私有化的对象存储。

在 **元数据存储** 方面，JuiceFS 采用多引擎设计，目前已支持 Redis、TiKV、MySQL/MariaDB、PostgreSQL、SQLite 等作为元数据服务引擎，也将陆续实现更多元数据存储引擎。欢迎 [提交 Issue](https://github.com/juicedata/juicefs/issues) 反馈你的需求。

在 **文件系统接口** 实现方面：

- 通过 **FUSE**，JuiceFS 文件系统能够以 POSIX 兼容的方式挂载到服务器，将海量云端存储直接当做本地存储来使用。
- 通过 **Hadoop Java SDK**，JuiceFS 文件系统能够直接替代 HDFS，为 Hadoop 提供低成本的海量存储。
- 通过 **Kubernetes CSI Driver**，JuiceFS 文件系统能够直接为 Kubernetes 提供海量存储。
- 通过 **S3 Gateway**，使用 S3 作为存储层的应用可直接接入，同时可使用 AWS CLI、s3cmd、MinIO client 等工具访问 JuiceFS 文件系统。
- 通过 **WebDAV Server**，使用 HTTP 协议接入 JuiceFS 并直接操作其中的文件。

## 如何存储文件

文件系统作为用户和硬盘之间交互的媒介，它让文件可以妥善的被存储在硬盘上。如你所知，Windows  常用的文件系统有 FAT32、NTFS，Linux 常用的文件系统有 Ext4、XFS、Btrfs 等，每一种文件系统都有其独特的组织和管理文件的方式，它决定了文件系统的存储能力和性能等特征。

JuiceFS 作为一个文件系统也不例外，它的强一致性、高性能等特征离不开它独特的文件管理模式。

与传统文件系统只能使用本地磁盘存储数据和对应的元数据的模式不同，JuiceFS 会将数据格式化以后存储在对象存储（云存储），同时会将数据对应的元数据存储在 Redis 等数据库中。

任何存入 JuiceFS 的文件都会被拆分成固定大小的 **"Chunk"**，默认的容量上限是 64 MiB。每个 Chunk 由一个或多个 **"Slice"** 组成，Slice 的长度不固定，取决于文件写入的方式。每个 Slice 又会被进一步拆分成固定大小的 **"Block"**，默认为 4 MiB。最后，这些 Block 会被存储到对象存储。与此同时，JuiceFS 会将每个文件以及它的 Chunks、Slices、Blocks 等元数据信息存储在元数据引擎中。

![JuiceFS storage format](../images/juicefs-storage-format-new.png)

使用 JuiceFS，文件最终会被拆分成 Chunks、Slices 和 Blocks 存储在对象存储。因此，你会发现在对象存储平台的文件浏览器中找不到存入 JuiceFS 的源文件，存储桶中只有一个 chunks 目录和一堆数字编号的目录和文件。不要惊慌，这正是 JuiceFS 文件系统高性能运作的秘诀！

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

除了挂载文件系统以外，你还可以使用 [JuiceFS S3 网关](../deployment/s3_gateway.md)，这样既可以使用 S3 兼容的客户端，也可以使用内置的基于网页的文件管理器访问 JuiceFS 存储的文件。
