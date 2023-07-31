---
slug: /comparison/juicefs_vs_seaweedfs
---

> SeaweedFS 与 JuiceFS  皆是具有高性能特性的分布式文件存储系统， 本文将列举两者之间的主要异同以便读者更好地了解他们的架构和使用场景。


# 共同点
* SeaweedFS 与 JuiceFS 都采用了元数据与数据分离的存储架构。
* 两者都需要依托外部的数据库服务以管理元数据。
* 在存储文件时都会对文件进行数据拆分。
* 都支持在多种场景下使用。

# 差异点

## 文件存储
* SeaweedFS 内部包含了一个文件存储服务（Volume Server），其设计思路与 Facebook的 Haystack 相近，在小文件的读写上性能表现优异。同时在一些扩展功能中（例如纠删码、合并存储等），SeaweedFS 都能够在内部支持。
* JuiceFS 则是需要依托一个外部的对象存储服务以存储文件，其性能与相关的扩展特性都与所选用的对象存储服务相关。

## 原子性操作
* JuiceFS 严格确保每一项操作的原子性，因此对于元数据服务（例如 Redis、MySQL）的事务能力有着较强的要求。
* SeaweedFS 则对操作的原子性保证较弱（仅在有限的逻辑中使用了数据库事务），在一些复杂的操作过程中如发生异常，可能会出现数据丢失或者错误的现象。

## 数据复制
* SeaweedFS 运用了自身提供的 changelog 功能，原生提供了 2 种模式下的多集群数据复制能力。
* JuiceFS 则需要依托于数据库引擎自生的数据同步能力，同时也暂未支持 changelog 相关特性。

## 客户端使用
* SeaweedFS 与 JuiceFS 都支持较广泛的使用场景，在访问协议上都涵盖了诸如 POSIX、S3 网关、HDFS等方面。
* 而在具体协议的支持完整性方面，JuiceFS 则比 SeaweedFS 更为完整。（例如 SeaweedFS  对于 POSIX 与 HDFS 只实现了基础层面的兼容，而JuiceFS 则是完全兼容）

# 差异表

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


如需更深入了解关于 JuiceFS 与 SeaweedFS 的对比，可以参考 [浅析 SeaweedFS 与 JuiceFS 架构异同](https://juicefs.com/zh-cn/blog/engineering/similarities-and-differences-between-seaweedfs-and-juicefs-structures) 一文。
