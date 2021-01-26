<p align="center"><a href="https://github.com/juicedata/juicefs"><img alt="JuiceFS Logo" src="https://github.com/juicedata/juicefs/raw/main/docs/images/juicefs-logo.png" width="50%" /></a></p>
<p align="center">
    <a href="https://travis-ci.com/juicedata/juicefs"><img alt="Build Status" src="https://travis-ci.com/juicedata/juicefs.svg?token=jKSPwswpc2ph4uMtwpHa&branch=main" /></a>
    <a href="https://join.slack.com/t/juicefs/shared_invite/zt-kjbre7de-K8jeTMouDZE8nKEZVHLAMQ"><img alt="Join Slack" src="https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack" /></a>
    <a href="https://goreportcard.com/report/github.com/juicedata/juicefs"><img alt="Go Report" src="https://goreportcard.com/badge/github.com/juicedata/juicefs?dummy=unused" /></a>
    <a href="README.md"><img alt="English Docs" src="https://img.shields.io/badge/docs-English-informational" /></a>
</p>

**JuiceFS** 是一个建立在 [Redis](https://redis.io) 和 S3 等对象存储之上的开源 POSIX 文件系统。它是为云原生环境设计，通过把元数据和数据分别持久化到 Redis 和对象存储中，它相当于一个无状态的中间件，帮助各种应用通过标准的文件系统接口来共享数据。

主要特性有：

- **完整 POSIX 兼容**：已有应用可以无缝对接；
- **极致的性能**：毫秒级的延迟，近乎无限的吞吐量（取决于对象存储规模）；
- **云原生**：完全弹性，很容易实现存储和计算分离架构；
- **共享**：可以被多个客户端同时读写；
- **文件锁**：支持 BSD 锁（flock）及 POSIX 锁（fcntl）；
- **数据压缩**：默认使用 [LZ4](https://lz4.github.io/lz4) 压缩数据，节省存储空间。

---

[架构](#架构) | [开始使用](#开始使用) | [POSIX 兼容性](#posix-兼容性测试) | [性能测试](#性能测试) | [支持的对象存储](#支持的对象存储) | [状态](#状态) | [产品路线图](#产品路线图) | [反馈问题](#反馈问题) | [贡献](#贡献) | [社区](#社区) | [使用量收集](#使用量收集) | [开源协议](#开源协议) | [致谢](#致谢) | [FAQ](#faq)

---

## 架构

JuiceFS 使用 Redis 来存储文件系统的元数据。Redis 是一个开源的内存数据库，可以保障元数据的高性能访问。所有文件的数据会通过客户端存储到对象存储中，以下是它的架构图：

![JuiceFS Architecture](docs/images/juicefs-arch.png)

JuiceFS 中的文件格式，如下图所示。一个文件首先被拆分成固定大小的 **"Chunk"**，默认 64 MiB。每个 Chunk 可以由一个或者多个 **"Slice"** 组成，它们是变长的。对于每一个 Slice，又会被拆分成固定大小的 **"Block"**，默认为 4 MiB（格式化后就不可以修改）。最后，这些 Block 会被压缩和加密保存到对象存储中。压缩和加密都是可选的。

![JuiceFS Storage Format](docs/images/juicefs-storage-format.png)


## 开始使用

### 预编译版本

你可以直接下载预编译的版本：[二进制版本](https://github.com/juicedata/juicefs/releases)。

### 从源代码编译

你需要先安装 [Go](https://golang.org)，然后通过下面的方式来编译：

```bash
$ git clone https://github.com/juicedata/juicefs.git
$ cd juicefs
$ make
```

### 依赖

需要 Redis（2.2 及以上）服务器来存储元数据，请参考 [Redis Quick Start](https://redis.io/topics/quickstart)。

如果是 macOS 系统，还需要 [macFUSE](https://osxfuse.github.io/)。

还需要一个对象存储，测试时可以用本地目录代替。

### 格式化

假定你已经有一个本地运行的 Redis 服务，下面用它来格式化一个叫做 `test` 的文件系统：

```bash
$ ./juicefs format localhost test
```

它会使用默认参数来格式化。如果 Redis 服务不在本地，你可以像这样完整填写它的地址：`redis://user:password@host:6379/1`。Redis 密码可以通过环境变量 `REDIS_PASSWORD` 来指定，避免暴露在命令行选项中。

JuiceFS 还需要一个对象存储，可以通过参数 `--storage`、`--bucket`、`--access-key` 和 `--secret-key` 来指定。它默认会使用本地目录来模拟一个对象存储用于测试，详细的参数请看 `./juicefs format -h`。

如果使用 MinIO 来存数据，可以这么写：

```bash
$ ./juicefs format --storage minio \
   --bucket http://1.2.3.4:9000/mybucket \
   --access-key XXX \
   --secret-key XXX \
   localhost test
```

### 挂载

一旦文件系统格式化好了，你可以把它挂载成一个目录，这个目录叫做 *挂载点*。

```bash
$ ./juicefs mount -d localhost ~/jfs
```

挂载之后你可以像使用本地盘一样使用它，详细的挂载参数，请运行 `./juicefs mount -h`。

### 命令索引

请点击[这里](docs/command_reference.md)查看所有子命令以及命令行参数。

### Kubernetes

JuiceFS 提供 [K8s CSI 驱动](https://github.com/juicedata/juicefs-csi-driver)来简化部署。

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

- 关闭再打开数据一致性。一旦一个文件写入完成并关闭，之后的打开和读操作就可以访问之前写入的数据。如果是在同一个挂载点，所有写入的数据都可以立即读。
- 重命名以及所有其他元数据操作都是原子的，由 Redis 的事务机制保证。
- 当文件被删除后，同一个挂载点上如果已经打开了，文件还可以继续访问。
- 支持 mmap
- 支持 fallocate 以及空洞
- 支持扩展属性
- 支持 BSD 锁（flock）
- 支持 POSIX 记录锁（fcntl）

## 性能测试

### 顺序读写性能

使用 [fio](https://github.com/axboe/fio) 测试了 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 的顺序读写性能，结果如下：

![Sequential Read Write Benchmark](docs/images/sequential-read-write-benchmark.svg)

上图显示 JuiceFS 可以比其他两者提供 10 倍以上的吞吐，详细结果请看[这里](docs/fio.md)。

### 元数据性能

使用 [mdtest](https://github.com/hpc/ior) 测试了 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 的元数据性能，结果如下：

![Metadata Benchmark](docs/images/metadata-benchmark.svg)

上图显示 JuiceFS 的元数据性能显著优于其他两个，详细的测试报告请看[这里](docs/mdtest.md)。

### 性能分析

在文件系统的根目录有一个叫做 `.accesslog` 的虚拟文件，它提供了所有文件系统操作的细节，以及所消耗的时间，比如：

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

每一行的最后一个数字是该操作所消耗的时间，单位是秒。我们可以利用它来分析各种性能问题，未来我们也会提供更多工具来分析它。

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

完整的支持列表，请参照 [juicesync](https://github.com/juicedata/juicesync)。

## 状态

JuiceFS 目前是 beta 状态，核心的存储格式还没有完全确定，还不建议使用到生产环境中。如果你对它有兴趣，请尽早测试，并给我们反馈。

## 产品路线图

- 稳定存储格式
- S3 兼容网关
- Windows 客户端
- 存储加密
- 支持其他数据库

## 反馈问题

我们使用 [GitHub Issues](https://github.com/juicedata/juicefs/issues) 来管理社区反馈的问题，你也可以通过其他[渠道](#社区)跟社区联系。

## 贡献

感谢你的兴趣，请参考 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 社区

欢迎加入 [Discussion](https://github.com/juicedata/juicefs/discussions) 和 [Slack channel](https://join.slack.com/t/juicefs/shared_invite/zt-kjbre7de-K8jeTMouDZE8nKEZVHLAMQ) 跟我们的团队和其他社区成员交流。

## 使用量收集

JuiceFS 的客户端会收集 **匿名** 使用数据来帮助我们更好地了解大家如何使用它，它只上报诸如版本号等使用量数据，不包含任何用户信息，完整的代码在[这里](cmd/usage.go)。

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

已经支持了绝大部分对象存储，参考这个[列表](#支持的对象存储)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请到 [juicesync](https://github.com/juicedata/juicesync) 创建一个 issue 来增加支持。

### 是否可以使用 Redis 集群版？

不可以。JuiceFS 使用了 Redis 的[事务功能](https://redis.io/topics/transactions)来保证元数据操作的原子性，而分布式版还不支持分布式事务。哨兵节点或者其他的 Redis 高可用方法是需要的。
