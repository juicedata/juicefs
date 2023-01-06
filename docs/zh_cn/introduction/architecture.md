---
title: 技术架构
sidebar_position: 2
slug: /architecture
decription: 本文介绍 JuiceFS 的技术架构以及由此带来的技术优势，同时介绍 JuiceFS 的文件存储原理。
---

JuiceFS 文件系统由三个部分组成：

![](../images/juicefs-arch-new.png)

**JuiceFS 客户端（Client）**：所有文件读写，乃至于碎片合并、回收站文件过期删除等后台任务，均在客户端中发生。可想而知，客户端需要同时与对象存储和元数据引擎打交道。客户端支持众多接入方式：

- 通过 **FUSE**，JuiceFS 文件系统能够以 POSIX 兼容的方式挂载到服务器，将海量云端存储直接当做本地存储来使用。
- 通过 **Hadoop Java SDK**，JuiceFS 文件系统能够直接替代 HDFS，为 Hadoop 提供低成本的海量存储。
- 通过 **Kubernetes CSI 驱动**，JuiceFS 文件系统能够直接为 Kubernetes 提供海量存储。
- 通过 **S3 网关**，使用 S3 作为存储层的应用可直接接入，同时可使用 AWS CLI、s3cmd、MinIO client 等工具访问 JuiceFS 文件系统。
- 通过 **WebDAV 服务**，以 HTTP 协议，以类似 RESTful API 的方式接入 JuiceFS 并直接操作其中的文件。

**数据存储（Data Storage）**：文件将会切分上传保存在对象存储服务，既可以使用公有云的对象存储，也可以接入私有部署的自建对象存储。JuiceFS 支持几乎所有的公有云对象存储，同时也支持 OpenStack Swift、Ceph、MinIO 等私有化的对象存储。

**元数据引擎（Metadata Engine）**：用于存储文件元数据（metadata），包含以下内容：

- 常规文件系统的元数据：文件名、文件大小、权限信息、创建修改时间、目录结构、文件属性、符号链接、文件锁等。
- JuiceFS 独有的元数据：文件的 chunk 及 slice 映射关系、客户端 session 等。

JuiceFS 采用多引擎设计，目前已支持 Redis、TiKV、MySQL/MariaDB、PostgreSQL、SQLite 等作为元数据服务引擎，也将陆续实现更多元数据存储引擎。欢迎[提交 Issue](https://github.com/juicedata/juicefs/issues) 反馈你的需求。

## JuiceFS 如何存储文件 {#how-juicefs-store-files}

与传统文件系统只能使用本地磁盘存储数据和对应的元数据的模式不同，JuiceFS 会将数据格式化以后存储在对象存储（云存储），同时会将文件的元数据存储在专门的元数据服务中，这样的架构让 JuiceFS 成为一个强一致性的高性能分布式文件系统。

任何存入 JuiceFS 的文件都会被拆分成一个或多个**「Chunk」**（最大 64 MiB）。而每个 Chunk 又由一个或多个**「Slice」**组成。Chunk 的存在是为了对文件做切分，优化大文件性能，而 Slice 则是为了进一步优化各类文件写操作，二者同为文件系统内部的逻辑概念。Slice 的长度不固定，取决于文件写入的方式。每个 Slice 又会被进一步拆分成**「Block」**（默认大小上限为 4 MiB），成为最终上传至对象存储的最小存储单元。

![JuiceFS storage format](../images/juicefs-storage-format-new.png)

因此，你会发现在对象存储平台的文件浏览器中找不到存入 JuiceFS 的源文件，存储桶中只有一个 `chunks` 目录和一堆数字编号的目录和文件，不必惊慌，这正是经过 JuiceFS 拆分存储的数据块。与此同时，文件与 Chunks、Slices、Blocks 的对应关系等元数据信息存储在元数据引擎中。正是这样的分离设计，让 JuiceFS 文件系统得以高性能运作。

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

JuiceFS 的存储设计，还有着以下技术特点：

* 对于任意大小的文件，JuiceFS 都不进行合并存储，这也是为了性能考虑，避免读放大。
* 提供强一致性保证，但也可以根据场景需要与缓存功能一起调优，比如通过设置出更激进的元数据缓存，牺牲一部分一致性，换取更好的性能。详见[「元数据缓存」](../guide/cache_management.md#metadata-cache)。
* 支持并默认开启[「回收站」](../security/trash.md)功能，删除文件后保留一段时间才彻底清理，最大程度避免误删文件导致事故。
