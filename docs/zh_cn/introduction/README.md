---
title: JuiceFS 简介
sidebar_position: 1
slug: .
pagination_next: introduction/architecture
---

**JuiceFS** 是一款面向云原生设计的高性能分布式文件系统，在 Apache 2.0 开源协议下发布。提供完备的 [POSIX](https://en.wikipedia.org/wiki/POSIX) 兼容性，可将几乎所有对象存储接入本地作为海量本地磁盘使用，亦可同时在跨平台、跨地区的不同主机上挂载读写。

JuiceFS 采用「数据」与「元数据」分离存储的架构，从而实现文件系统的分布式设计。文件数据本身会被切分保存在[对象存储](../reference/how_to_set_up_object_storage.md#supported-object-storage)（例如 Amazon S3），而元数据则可以保存在 Redis、MySQL、TiKV、SQLite 等多种[数据库](../reference/how_to_set_up_metadata_engine.md)中，你可以根据场景与性能要求进行选择。

JuiceFS 提供了丰富的 API，适用于各种形式数据的管理、分析、归档、备份，可以在不修改代码的前提下无缝对接大数据、机器学习、人工智能等应用平台，为其提供海量、弹性、低价的高性能存储。运维人员不用再为可用性、灾难恢复、监控、扩容等工作烦恼，专注于业务开发，提升研发效率。同时运维细节的简化，对 DevOps 极其友好。

<div className="video-container">
  <iframe src="//player.bilibili.com/player.html?aid=931107196&bvid=BV1HK4y197va&cid=350876578&page=1&autoplay=0" width="100%" height="360" scrolling="no" border="0" frameborder="no" framespacing="0" allowfullscreen="true"> </iframe>
</div>

## 核心特性 {#features}

1. **POSIX 兼容**：像本地文件系统一样使用，无缝对接已有应用，无业务侵入性；
2. **HDFS 兼容**：完整兼容 [HDFS API](../deployment/hadoop_java_sdk.md)，提供更强的元数据性能；
3. **S3 兼容**：提供 [S3 网关](../guide/gateway.md) 实现 S3 协议兼容的访问接口；
4. **云原生**：通过 [Kubernetes CSI 驱动](../deployment/how_to_use_on_kubernetes.md) 轻松地在 Kubernetes 中使用 JuiceFS；
5. **分布式设计**：同一文件系统可在上千台服务器同时挂载，高性能并发读写，共享数据；
6. **强一致性**：确认的文件修改会在所有服务器上立即可见，保证强一致性；
7. **强悍性能**：毫秒级延迟，近乎无限的吞吐量（取决于对象存储规模），查看[性能测试结果](../benchmark/benchmark.md)；
8. **数据安全**：支持传输中加密（encryption in transit）和静态加密（encryption at rest），[查看详情](../security/encryption.md)；
9. **文件锁**：支持 BSD 锁（flock）和 POSIX 锁（fcntl）；
10. **数据压缩**：支持 [LZ4](https://lz4.github.io/lz4) 和 [Zstandard](https://facebook.github.io/zstd) 压缩算法，节省存储空间。

## 应用场景 {#scenarios}

JuiceFS 为海量数据存储设计，可以作为很多分布式文件系统和网络文件系统的替代，特别是以下场景：

- **大数据分析**：HDFS 兼容；与主流计算引擎（Spark、Presto、Hive 等）无缝衔接；无限扩展的存储空间；运维成本几乎为 0；性能远好于直接对接对象存储。
- **机器学习**：POSIX 兼容，可以支持所有机器学习、深度学习框架；方便的文件共享还能提升团队管理、使用数据效率。
- **Kubernetes**：JuiceFS 支持 Kubernetes CSI；为容器提供解耦的文件存储，令应用服务可以无状态化；方便地在容器间共享数据。
- **共享工作区**：可以在任意主机挂载；没有客户端并发读写限制；POSIX 兼容已有的数据流和脚本操作。
- **数据备份**：在无限平滑扩展的存储空间备份各种数据，结合共享挂载功能，可以将多主机数据汇总至一处，做统一备份。

## 数据隐私 {#data-privacy}

JuiceFS 是开源软件，你可以在 [GitHub](https://github.com/juicedata/juicefs) 找到完整的源代码。在使用 JuiceFS 存储数据时，数据会按照一定的规则被拆分成数据块并保存在你自己定义的对象存储或其它存储介质中，数据所对应的元数据则存储在你自己定义的数据库中。

## 更多相关信息 {#more-info}

* **案例**：想了解更多相似场景的实践案例，请访问[用户案例](https://juicefs.com/zh-cn/blog/user-stories)。
* **视频**：我们在 [Bilibili 频道](https://space.bilibili.com/1206844881)提供了丰富的视频教程。
* **加入社群**：欢迎加入我们的[微信用户组](https://juicefs.com/zh-cn/wechat-user-group)（中文）或者 [Slack](https://go.juicefs.com/slack)（英文），与 JuiceFS 用户共同探讨。
* **Office Hours**：每月第 2 周的星期三 16:00-17:00（UTC+8）在线上举行，Juicedata 工程师将为你实时答疑解惑。请加入微信用户组获取最新活动信息。
* **AI 助手**：如果你遇到了任何问题，欢迎使用「Ask AI」功能（右下角）求助 AI 助手。AI 助手的知识库来源于文档以及 GitHub 中的相关内容。
