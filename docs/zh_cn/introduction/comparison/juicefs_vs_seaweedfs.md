---
slug: /comparison/juicefs_vs_seaweedfs
---

SeaweedFS 与 JuiceFS 皆是高性能分布式文件存储系统，本章列举两者之间的主要异同，帮助你更好地了解他们的架构和使用场景。内容从[浅析 SeaweedFS 与 JuiceFS 架构异同](https://juicefs.com/zh-cn/blog/engineering/similarities-and-differences-between-seaweedfs-and-juicefs-structures)一文总结而成，如有需要可以详读文章，进一步了解。

# 共同点

* SeaweedFS 与 JuiceFS 都采用了元数据与数据分离的存储架构。
* 两者都需要依托外部的数据库服务以管理元数据。
* 在存储文件时都会对文件进行数据拆分。
* 都支持在多种场景下使用。

# 差异点

|                  | **SeaweedFS**      | **JuiceFS**           |
| ---------------- | ------------------ | --------------------- |
| 元数据           | 多引擎             | 多引擎                |
| 元数据操作原子性 | 未保证             | 通过数据库事务保证    |
| 变更日志         | 有                 | 无                    |
| 数据存储         | 包含               | 外部服务              |
| 纠删码           | 支持               | 依赖外部服务          |
| 数据合并         | 支持               | 依赖外部服务          |
| 文件拆分         | 8MB                | 64MB + 4MB            |
| 分层存储         | 支持               | 依赖外部服务          |
| 数据压缩         | 支持（基于扩展名） | 支持（全局设置）      |
| 存储加密         | 支持               | 支持                  |
| POSIX 兼容性     | 基本               | 完整                  |
| S3 协议          | 基本               | 基本                  |
| WebDAV 协议      | 支持               | 支持                  |
| HDFS 兼容性      | 基本               | 完整                  |
| CSI 驱动         | 支持               | 支持                  |
| 客户端缓存       | 不支持             | 支持                  |
| 集群数据复制     | 双向异步、多模式   | 不支持                |
| 云上数据缓存     | 支持（手动同步）   | 不支持                |
| 回收站           | 不支持             | 支持                  |
| 运维工具         | 提供               | 提供                  |
| 发布时间         | 2015.4             | 2021.1                |
| 主要维护者       | 个人（Chris Lu）   | 公司（Juicedata Inc） |
| 语言             | Go                 | Go                    |
| 开源协议         | Apache License 2.0 | Apache License 2.0    |

下方是一些关键的系统设计区别。

## 文件存储

* SeaweedFS 内部包含了一个文件存储服务（设计思路与 Facebook 的 [Haystack](https://engineering.fb.com/2009/04/30/core-data/needle-in-a-haystack-efficient-storage-of-billions-of-photos) 相近），在小文件的读写上性能表现优异。同时支持纠删码、合并存储等功能。
* JuiceFS 则依赖外部的对象存储服务，其性能与相关的扩展特性都与所选用的对象存储服务相关。

## 原子性操作

* JuiceFS 严格确保每一项操作的原子性，因此对于元数据引擎（例如 Redis、MySQL）的事务能力有着较强的要求。
* SeaweedFS 则对操作的原子性保证较弱（仅在有限的逻辑中使用了数据库事务），在一些复杂的操作过程中如发生异常，可能会出现数据丢失或者错误的现象。

## 数据复制

对于多个集群之间的数据复制，SeaweedFS 支持「Active-Active」与「Active-Passive」两种异步的复制模式，Active-Active 模式中，两个集群都能够参与文件写入并双向同步，Active-Passive 模式则是主从关系，Passive 一方只读。2 种模式都是通过传递 changelog 再应用的机制实现了不同集群数据间的一致性，对于每一条 changelog，其中会有一个签名信息以保证同一个修改不会被循环多次。在集群节点数量超过 2 个节点的 Active-Active 模式下，SeaweedFS 的一些操作（如重命名目录）会受到一些限制。

JuiceFS 社区版不支持集群之间的数据同步功能，但可以使用元数据引擎和对象存储自身的数据复制能力实现文件系统镜像功能，功能上类似 SeaweedFS 的 Active-Passive 模式。JuiceFS 商业版则支持[镜像集群](https://juicefs.com/docs/zh/cloud/guide/mirror)和[跨区域数据复制](https://juicefs.com/docs/zh/cloud/guide/replication)功能功能。

## 客户端使用

SeaweedFS 与 JuiceFS 都支持较广泛的使用场景，都支持 POSIX、S3 网关、HDFS。JuiceFS 在不少协议中提供更完整的兼容性支持，例如 SeaweedFS 对于 POSIX 与 HDFS 只实现了基础层面的兼容，而 JuiceFS 则是完全兼容。
