# JuiceFS 技术架构

JuiceFS 文件系统由三个部分组成：

1. **JuiceFS 客户端**：协调对象存储和元数据存储引擎，以及 POSIX、Hadoop、Kubernetes、S3 Gateway 等文件系统接口的实现；
2. **数据存储**：存储数据本身，支持本地磁盘、对象存储；
3. **元数据引擎**：存储数据对应的元数据，支持 Redis、MySQL、SQLite 等多种引擎；

![JuiceFS Architecture](../images/juicefs-arch-new.png)

作为文件系统，JuiceFS 会分别处理数据及其对应的元数据，数据会被存储在对象存储中，元数据会被存储在元数据服务引擎中。

在**数据存储**方面，JuiceFS 支持几乎所有的公有云对象存储，同时也支持 OpenStack Swift、Ceph、MinIO 等私有化的对象存储。

在**元数据存储**方面，JuiceFS 采用多引擎设计，目前已支持 [Redis](https://redis.io/)、MySQL/MariaDB、SQLite 等作为元数据服务引擎，也将陆续实现更多元数据存储引擎。欢迎 [提交 Issue](https://github.com/juicedata/juicefs/issues) 反馈你的需求！

在**文件系统接口**实现方面：

- 通过 **FUSE**，JuiceFS 文件系统能够以 POSIX 兼容的方式挂载到服务器，将海量云端存储直接当做本地存储来使用。
- 通过 **Hadoop Java SDK**，JuiceFS 文件系统能够直接替代 HDFS，为 Hadoop 提供低成本的海量存储。
- 通过 **Kubernetes CSI driver**，JuiceFS 文件系统能够直接为 Kubernetes 提供海量存储。
- 通过 **S3 Gateway**，使用 S3 作为存储层的应用可直接接入，同时可使用 AWS CLI、s3cmd、MinIO client 等工具访问 JuiceFS 文件系统。

## 你可能还需要

现在，你可以参照 [快速上手指南](quick_start_guide.md) 立即开始使用 JuiceFS！

你还可以进一步了解 [JuiceFS 如何存储文件](how_juicefs_store_files.md)

