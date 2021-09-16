# JuiceFS 对比 CephFS

## 共同点

两者都是高可靠，高性能的弹性分布式文件系统，且均有良好的 POSIX 兼容性，在各种文件系统使用场景都可一试。

## 不同点

### 系统架构

两者都采用了数据和元数据分离的架构，但在组件实现上有很大区别。

#### CephFS

是一套完整且独立的系统，倾向于私有云部署；所有数据和元数据都会持久化在 Ceph 自己的存储池（Rados Pool）中。

- 元数据
  - 服务进程（MDS）：无状态且理论可水平扩展。目前已有成熟的主备机制，但多主部署依然有性能和稳定性隐患；生产环境通常采用一主多备或者多主静态隔离
  - 持久化：独立的 Rados 存储池，通常采用 SSD 或更高性能的硬件存储
- 数据：一个或多个 Rados 存储池，支持通过 Layout 指定不同的配置，如分块大小（默认 4 MiB），冗余方式（多副本，EC）等
- 客户端：支持内核客户端（kcephfs），用户态客户端（ceph-fuse）以及基于 libcephfs 实现的 C++、Python 等 SDK；近来社区也提供了 Windows 客户端（ceph-dokan）。同时生态中也有与 Samba 对接的 VFS object 和与 NFS-Ganesha 对接的 FSAL 模块可供考虑。

#### JuiceFS

JuiceFS 主要实现一个 libjfs 库和 FUSE 客户端程序、Java SDK 等，支持对接多种元数据引擎和对象存储，适合在公有云、私有云或混合云环境下部署；

- 元数据：支持多种已有的[数据库实现](../databases_for_metadata.md)，包括：
  - Redis 及各种兼容 Redis 协议的变种（需要支持事务）；
  - SQL 系列：MySQL，PostgreSQL，SQLite 等；
  - 分布式 K/V 存储：已支持 TiKV，计划支持 Apple FoundationDB；
  - 自研引擎：用于公有云上的 JuiceFS 全托管服务；
- 数据：支持超过 30 种公有云上的[对象存储](../how_to_setup_object_storage.md)，也可以和 MinIO，Ceph RADOS，Ceph RGW 等对接；
- 客户端：支持 Unix 用户态挂载，Windows 挂载，完整兼容 HDFS 语义的 Java SDK，[Python SDK](https://github.com/megvii-research/juicefs-python) 以及内置的 S3 网关。

### 功能特性

|                         | CephFS            | JuiceFS       |
| ----------------------- | ----------        | ------------- |
| 文件分块<sup> [1]</sup> | ✓                 | ✓             |
| 元数据事务              | ✓                 | ✓             |
| 强一致性                | ✓                 | ✓             |
| Kubernetes CSI Driver   | ✓                 | ✓             |
| Hadoop 兼容             | ✓                 | ✓             |
| 数据压缩<sup> [2]</sup> | ✓                 | ✓             |
| 数据加密<sup> [3]</sup> | ✓                 | ✓             |
| 快照                    | ✓                 | ✕             |
| 客户端数据缓存          | ✕                 | ✓             |
| Hadoop 数据本地性       | ✕                 | ✓             |
| S3 兼容                 | ✕                 | ✓             |
| 配额                    | 目录级配额        | Volume 级配额 |
| 开发语言                | C++               | Go            |
| 开源协议                | LGPLv2.1 & LGPLv3 | AGPLv3        |

#### 注 1：文件分块

虽然两者都做了大文件的分块，但在实现原理上有本质区别。CephFS 会将文件按分块大小（默认为 4MiB）拆分，每个分块对应一个 Rados object。而 JuiceFS 则将文件先按 64MiB Chunk 拆分，每个 Chunk 在写入时根据实际情况进一步拆分成一个或多个逻辑 Slice，每个 Slice 在写入对象存储时再拆分成默认 4MiB 的 Block，Block 与对象存储中 object 一一对应。在处理覆盖写时，CephFS 需要直接修改对应的 objects，流程较为复杂；尤其是冗余策略为 EC 或者开启数据压缩时，往往需要先读取部分 object 内容，在内存中修改后再写入，这个流程会带来很大的性能开销。而 JuiceFS 在覆盖写时将更新数据作为新 objects 写入并修改元数据即可，性能大幅提升。过程中出现的冗余数据会异步完成垃圾回收。

#### 注 2：数据压缩

严格来讲，CephFS 本身并未提供数据压缩功能，其实际依赖的是 Rados 层 BlueStore 的压缩。而 JuiceFS 则可以在 Block 上传到对象存储之前就进行一次数据压缩，以减少对象存储中的容量使用。换言之，如果用 JuiceFS 对接 Rados，是能做到在 Block 进 Rados 前后各进行一次压缩。另外，就像在**文件分块**中提到的，出于对覆盖写的性能保障，CephFS 一般不会开启 BlueStore 的压缩功能。

#### 注 3：数据加密

Ceph **Messenger v2** 支持网络传输层的数据加密，存储层则与压缩类似，依赖于 OSD 创建时提供的加密功能。JuiceFS 是在上传对象前和下载后执行加解密，在对象存储侧完全透明。
