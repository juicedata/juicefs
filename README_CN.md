<p align="center"><a href="https://github.com/juicedata/juicefs"><img alt="JuiceFS Logo" src="docs/images/juicefs-logo.png" width="50%" /></a></p>
<p align="center">
    <a href="https://travis-ci.com/juicedata/juicefs"><img alt="Build Status" src="https://travis-ci.com/juicedata/juicefs.svg?token=jKSPwswpc2ph4uMtwpHa&branch=main" /></a>
    <a href="https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA"><img alt="Join Slack" src="https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack" /></a>
    <a href="https://goreportcard.com/report/github.com/juicedata/juicefs"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/juicedata/juicefs" /></a>
    <a href="README.md"><img alt="English Docs" src="https://img.shields.io/badge/docs-English-informational" /></a>
</p>


JuiceFS 是一款高性能 [POSIX](https://en.wikipedia.org/wiki/POSIX) 文件系统，针对云原生环境特别优化设计，在 GNU Affero General Public License v3.0 开源协议下发布。使用 JuiceFS 存储数据，数据本身会被持久化在对象存储（例如，Amazon S3），而数据所对应的元数据可以根据场景需求被持久化在 Redis、MySQL、SQLite 等多种数据库引擎中。JuiceFS 可以简单便捷的将海量云存储直接接入已投入生产环境的大数据、机器学习、人工智能以及各种应用平台，无需修改代码即可像使用本地存储一样高效使用海量云端存储。

📺 **视频**: [什么是 JuiceFS?](https://www.bilibili.com/video/BV1HK4y197va/)

## 核心特性

1. **POSIX 兼容**：像本地文件系统一样使用，无缝对接已有应用，无业务侵入性；
2. **HDFS 兼容**：完整兼容 [HDFS API](docs/zh_cn/hadoop_java_sdk.md)，提供更强的元数据性能；
3. **S3 兼容**：提供 [S3 Gateway](docs/zh_cn/s3_gateway.md) 实现 S3 协议兼容的访问接口；
4. **云原生**：通过 [Kubernetes CSI driver](docs/zh_cn/how_to_use_on_kubernetes.md) 可以很便捷地在 Kubernetes 中使用 JuiceFS；
5. **多端共享**：同一文件系统可在上千台服务器同时挂载，高性能并发读写，共享数据；
6. **强一致性**：确认的修改会在所有挂载了同一文件系统的服务器上立即可见，保证强一致性；
7. **强悍性能**：毫秒级的延迟，近乎无限的吞吐量（取决于对象存储规模），查看[性能测试结果](docs/zh_cn/benchmark.md)；
8. **数据安全**：支持传输中加密（encryption in transit）以及静态加密（encryption at rest），[查看详情](docs/zh_cn/encrypt.md)；
9. **文件锁**：支持 BSD 锁（flock）及 POSIX 锁（fcntl）；
10. **数据压缩**：支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 压缩数据，节省存储空间；

---

[架构](#架构) | [开始使用](#开始使用) | [进阶主题](#进阶主题) | [POSIX 兼容性](#posix-兼容性测试) | [性能测试](#性能测试) | [支持的对象存储](#支持的对象存储) | [谁在使用](#谁在使用) | [产品路线图](#产品路线图) | [反馈问题](#反馈问题) | [贡献](#贡献) | [社区](#社区) | [使用量收集](#使用量收集) | [开源协议](#开源协议) | [致谢](#致谢) | [FAQ](#faq)

---

## 架构

JuiceFS 由三个部分组成：

1. **JuiceFS 客户端**：协调对象存储和元数据存储引擎，以及 POSIX、Hadoop、Kubernetes、S3 Gateway 等文件系统接口的实现；
2. **数据存储**：存储数据本身，支持本地磁盘、对象存储；
3. **元数据引擎**：存储数据对应的元数据，支持 Redis、MySQL、SQLite 等多种引擎；

![JuiceFS Architecture](docs/images/juicefs-arch-new.png)

JuiceFS 依靠 Redis 来存储文件的元数据。Redis 是基于内存的高性能的键值数据存储，非常适合存储元数据。与此同时，所有数据将通过 JuiceFS 客户端存储到对象存储中。[了解详情](docs/zh_cn/architecture.md)

![JuiceFS Storage Format](docs/images/juicefs-storage-format-new.png)

任何存入 JuiceFS 的文件都会被拆分成固定大小的 **"Chunk"**，默认的容量上限是 64 MiB。每个 Chunk 由一个或多个 **"Slice"** 组成，Slice 的长度不固定，取决于文件写入的方式。每个 Slice 又会被进一步拆分成固定大小的 **"Block"**，默认为 4 MiB。最后，这些 Block 会被存储到对象存储。与此同时，JuiceFS 会将每个文件以及它的 Chunks、Slices、Blocks 等元数据信息存储在元数据引擎中。[了解详情](docs/zh_cn/how_juicefs_store_files.md)

![How JuiceFS stores your files](docs/images/how-juicefs-stores-files-new.png)

使用 JuiceFS，文件最终会被拆分成 Chunks、Slices 和 Blocks 存储在对象存储。因此，你会发现在对象存储平台的文件浏览器中找不到存入 JuiceFS 的源文件，存储桶中只有一个 chunks 目录和一堆数字编号的目录和文件。不要惊慌，这正是 JuiceFS 高性能运作的秘诀！

## 开始使用

创建 JuiceFS ，需要以下 3 个方面的准备：

1. 准备 Redis 数据库
2. 准备对象存储
3. 下载安装 JuiceFS 客户端

请参照 [快速上手指南](docs/zh_cn/quick_start_guide.md) 立即开始使用 JuiceFS！

### 命令索引

请点击 [这里](docs/zh_cn/command_reference.md) 查看所有子命令以及命令行参数。

### Kubernetes

在 Kubernetes 中使用 JuiceFS 非常便捷，请查看 [这个文档](docs/zh_cn/how_to_use_on_kubernetes.md) 了解更多信息。

### Hadoop Java SDK

JuiceFS 使用 [Hadoop Java SDK](docs/zh_cn/hadoop_java_sdk.md) 与 Hadoop 生态结合。

## 进阶主题

- [Redis 最佳实践](docs/zh_cn/redis_best_practices.md)
- [如何设置对象存储](docs/zh_cn/how_to_setup_object_storage.md)
- [缓存管理](docs/zh_cn/cache_management.md)
- [故障诊断和分析](docs/zh_cn/fault_diagnosis_and_analysis.md)
- [FUSE 挂载选项](docs/zh_cn/fuse_mount_options.md)
- [在 Windows 中使用 JuiceFS](docs/zh_cn/juicefs_on_windows.md)
- [S3 网关](docs/zh_cn/s3_gateway.md)

请查阅 [JuiceFS 用户手册](docs/zh_cn/README.md) 了解更多信息。

## POSIX 兼容性测试

JuiceFS 通过了 [pjdfstest](https://github.com/pjd/pjdfstest) 最新版所有 8813 项兼容性测试。

```
All tests successful.

Test Summary Report
-------------------
/root/soft/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1323 Failed: 0)
  TODO passed:   693, 697, 708-709, 714-715, 729, 733
Files=235, Tests=8813, 233 wallclock secs ( 2.77 usr  0.38 sys +  2.57 cusr  3.93 csys =  9.65 CPU)
Result: PASS
```

除了 pjdfstests 覆盖的那些 POSIX 特性外，JuiceFS 还支持：

- 关闭再打开（close-to-open）一致性。一旦一个文件写入完成并关闭，之后的打开和读操作保证可以访问之前写入的数据。如果是在同一个挂载点，所有写入的数据都可以立即读。
- 重命名以及所有其他元数据操作都是原子的，由 Redis 的事务机制保证。
- 当文件被删除后，同一个挂载点上如果已经打开了，文件还可以继续访问。
- 支持 mmap
- 支持 fallocate 以及空洞
- 支持扩展属性
- 支持 BSD 锁（flock）
- 支持 POSIX 记录锁（fcntl）

## 性能测试

### 基础性能测试

JuiceFS 提供一个性能测试的子命令来帮助你了解它在你的环境中的性能表现：

![JuiceFS Bench](docs/images/juicefs-bench.png)

### 顺序读写性能

使用 [fio](https://github.com/axboe/fio) 测试了 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 的顺序读写性能，结果如下：

![Sequential Read Write Benchmark](docs/images/sequential-read-write-benchmark.svg)

上图显示 JuiceFS 可以比其他两者提供 10 倍以上的吞吐，详细结果请看[这里](docs/zh_cn/fio.md)。

### 元数据性能

使用 [mdtest](https://github.com/hpc/ior) 测试了 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 的元数据性能，结果如下：

![Metadata Benchmark](docs/images/metadata-benchmark.svg)

上图显示 JuiceFS 的元数据性能显著优于其他两个，详细的测试报告请看[这里](docs/zh_cn/mdtest.md)。

### 性能分析

在文件系统的根目录有一个叫做 `.accesslog` 的虚拟文件，它提供了所有文件系统操作的细节，以及所消耗的时间，比如：

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

每一行的最后一个数字是该操作所消耗的时间，单位是秒。你可以直接利用它来分析各种性能问题，或者尝试 `./juicefs profile /jfs` 命令实时监控统计信息。欲进一步了解此子命令请运行 `./juicefs profile -h` 或参阅[这里](docs/zh_cn/operations_profiling.md)。

## 支持的对象存储

- 亚马逊 S3
- 谷歌云存储
- 微软云存储
- 阿里云 OSS
- 腾讯云 COS
- 青云 QingStor 对象存储
- Ceph RGW
- MinIO
- 本地目录
- Redis

JuiceFS 支持几乎所有主流的对象存储服务，[查看详情](docs/zh_cn/how_to_setup_object_storage.md)。

## 谁在使用

JuiceFS 目前是 beta 状态，核心的存储格式还没有完全确定，如果要使用在生产环境中，请先进行细致认真的评估。如果你对它有兴趣，请尽早测试，并给我们[反馈](https://github.com/juicedata/juicefs/discussions)。

欢迎你在使用 JuiceFS 后告诉我们，向大家分享你的使用经验。我们也收集汇总了一份名单在 [ADOPTERS_CN.md](ADOPTERS_CN.md) 中，也包括了其他开源项目与 JuiceFS 搭配使用的情况。

## 产品路线图

- 稳定存储格式
- 支持使用 FoundationDB 做元数据引擎
- 基于用户和组的配额
- 基于目录的配额
- 快照
- 回收站
- 一次写入多次读取（WORM）

## 反馈问题

我们使用 [GitHub Issues](https://github.com/juicedata/juicefs/issues) 来管理社区反馈的问题，你也可以通过其他[渠道](#社区)跟社区联系。

## 贡献

感谢你的兴趣，请参考 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 社区

欢迎加入 [Discussions](https://github.com/juicedata/juicefs/discussions) 和 [Slack 频道](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA) 跟我们的团队和其他社区成员交流。

## 使用量收集

JuiceFS 的客户端会收集 **匿名** 使用数据来帮助我们更好地了解大家如何使用它，它只上报诸如版本号等使用量数据，不包含任何用户信息，完整的代码在 [这里](pkg/usage/usage.go)。

你也可以通过下面的方式禁用它：

```bash
$ ./juicefs mount --no-usage-report
```

## 开源协议

使用 GNU AGPL v3.0 开源，详见 [LICENSE](LICENSE)。

## 致谢

JuiceFS 的设计参考了 [Google File System](https://research.google/pubs/pub51)、[HDFS](https://hadoop.apache.org) 以及 [MooseFS](https://moosefs.com)，感谢他们的杰出工作。

## FAQ

### 为什么不支持某个对象存储？

已经支持了绝大部分对象存储，参考这个[列表](docs/zh_cn/how_to_setup_object_storage.md#支持的存储服务)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请创建一个 issue 来增加支持。

### 是否可以使用 Redis 集群版？

不可以。JuiceFS 使用了 Redis 的[事务功能](https://redis.io/topics/transactions)来保证元数据操作的原子性，而分布式版还不支持分布式事务。哨兵节点或者其他的 Redis 高可用方法是需要的。

请查看[「Redis 最佳实践」](docs/zh_cn/redis_best_practices.md)了解更多信息。

### JuiceFS 与 XXX 的区别是什么？

请查看[「同类技术对比」](docs/zh_cn/comparison_with_others.md)文档了解更多信息。

更多 FAQ 请查看[完整列表](docs/zh_cn/faq.md)。
