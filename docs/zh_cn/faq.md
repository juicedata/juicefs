---
title: 常见问题（FAQ）
slug: /faq
---

## 文档没能解答我的疑问

请首先尝试使用站内搜索功能（右上角），尝试用不同的关键词进行检索，如果文档始终未能解决你的疑问，可以加入 [JuiceFS 开源社区](https://juicefs.com/zh-cn/community)以寻求帮助。

## 一般问题

### JuiceFS 与 XXX 的区别是什么？

请查看[「同类技术对比」](introduction/comparison/juicefs_vs_alluxio.md)文档了解更多信息。

### 怎么升级 JuiceFS 客户端？

首先请卸载 JuiceFS 文件系统，然后使用新版本的客户端重新挂载。

### JuiceFS 的日志在哪里？

不同类型的 JuiceFS 客户端获取日志的方式也不同，详情请参考[「客户端日志」](administration/fault_diagnosis_and_analysis.md#客户端日志)文档。

### 怎么卸载 JuiceFS 文件系统？

请使用 [`juicefs umount`](reference/command_reference.md#juicefs-umount) 命令卸载。

### 卸载挂载点时报 `Resource busy -- try 'diskutil unmount'` 错误

这代表挂载点下的某个文件或者目录正在被使用，无法直接 `umount`，可以检查（如通过 `lsof` 命令）是否有打开的终端正位于 JuiceFS 挂载点的某个目录，或者某个应用程序正在处理挂载点中的文件。如果有，则退出终端或应用程序后再尝试使用 `juicefs umount` 命令卸载文件系统。

## 元数据相关问题

### 支持哨兵或者集群模式的 Redis 作为 JuiceFS 的元数据引擎吗？

支持，另外这里还有一篇 Redis 作为 JuiceFS 元数据引擎的[最佳实践文档](administration/metadata/redis_best_practices.md)可供参考。

### `format: ERR wrong number of arguments for 'auth' command`

这个错误意味着你使用的 Redis 版本小于 6.0.0 同时在执行 `juicefs format` 命令时指定了 username 参数。只有 Redis 6.0.0 版本以后才支持指定 username，因此你需要省略 URL 中的 username 参数，例如 `redis://:password@host:6379/1`。

### `cannot update volume XXX from XXX to XXX`

使用的元数据库已经被 format 过了并且本次 format 无法更新之前的某些配置。需要在手动清理元数据库后再执行 `juicefs format` 命令。

## 对象存储相关问题

### 为什么不支持某个对象存储？

已经支持了绝大部分对象存储，参考这个[列表](guide/how_to_set_up_object_storage.md#supported-object-storage)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请创建一个 issue 来增加支持。

### 为什么在对象存储中看不到那些已经存入 JuiceFS 的原始文件？

请参考[「JuiceFS 如何存储文件」](introduction/architecture.md#how-juicefs-store-files)。

### 为什么我在挂载点删除了文件，但是对象存储占用空间没有变化或者变化很小？

第一个原因是你可能开启了回收站特性。为了保证数据安全回收站默认开启，删除的文件其实被放到了回收站，实际并没有被删除，所以对象存储大小不会变化。回收站的保留时间可以通过 `juicefs format` 指定或者通过 `juicefs config` 修改。请参考[「回收站」](security/trash.md)文档了解更多信息。

第二个原因是 JuiceFS 是异步删除对象存储中的数据，所以对象存储的空间变化会慢一点。如果你需要立即清理对象存储中需要被删除的数据，可以尝试运行 [`juicefs gc`](reference/command_reference.md#gc) 命令。

### 为什么挂载点显示的大小与对象存储占用空间存在差异？

通过[「JuiceFS 支持随机写的实现原理是什么？」](#juicefs-支持随机写的实现原理是什么)这个问题的答案可以推断出，对象存储的占用空间大部分情况下是大于等于实际大小的，尤其是短时间内进行大量的覆盖写产生许多文件碎片后。这些碎片在未触发合并与回收前其仍旧占用着对象存储的空间。不过也不必担心这些碎片一直占用空间，因为在每次读／写文件的时候都会检查并在必要的时候触发该文件相关碎片的整理工作。另外你可以通过 `juicefs gc —-compact -—delete` 命令手动触发合并与回收。

另外如果 JuiceFS 文件系统开启了压缩功能（默认不开启），那么对象存储上存储的对象有可能比实际文件大小更小（取决于不同类型文件的压缩比）。

如果以上因素都已经排除，请检查你使用的对象存储的[存储类型](guide/how_to_set_up_object_storage.md#存储类型)是什么，云服务商可能会针对某些存储类型设置最小计量单位。例如阿里云 OSS 低频访问存储的[最小计量单位](https://help.aliyun.com/document_detail/173534.html)是 64KB，如果单个文件小于 64KB 也会按照 64KB 计算。

### JuiceFS 支持使用对象存储中的某个目录作为 `--bucket` 选项的值吗？

到 JuiceFS 1.0 为止，还不支持该功能。

### JuiceFS 支持访问对象存储中已经存在的数据吗？

到 JuiceFS 1.0 为止，还不支持该功能。

### 一个文件系统可以绑定多个不同的对象存储吗（比如同时用 Amazon S3、GCS 和 OSS 组成一个文件系统）？

不支持。但在创建文件系统时可以设定关联同一个对象存储的多个 bucket，从而解决单个 bucket 对象数量限制的问题，例如，可以为一个文件系统关联多个 S3 Bucket。具体请参考 [`--shards`](./reference/command_reference.md#format) 选项的说明。

## 性能相关问题

### JuiceFS 的性能如何？

JuiceFS 是一个分布式文件系统，元数据访问的延时取决于挂载点到服务端之间 1 到 2 个网络来回（通常 1-3 ms），数据访问的延时取决于对象存储的延时 (通常 20-100 ms)。顺序读写的吞吐量可以到 50MiB/s 至 2800MiB/s（查看 [fio 测试结果](benchmark/fio.md)），取决于网络带宽以及数据是否容易被压缩。

JuiceFS 内置多级缓存（主动失效），一旦缓存预热好，访问的延时和吞吐量非常接近单机文件系统的性能（FUSE 会带来少量的开销）。

### JuiceFS 支持随机读写吗？

支持，包括通过 mmap 等进行的随机读写。目前 JuiceFS 主要是对顺序读写进行了大量优化，对随机读写的优化也在进行中。如果想要更好的随机读性能，建议关闭压缩（[`--compress none`](reference/command_reference.md#format)）。

### JuiceFS 支持随机写的实现原理是什么？

JuiceFS 不将原始文件存入对象存储，而是将其按照某个大小（默认为 4MiB）拆分为 N 个数据块（Block）后，上传到对象存储，然后将数据块的 ID 存入元数据引擎。随机写的时候，逻辑上是要覆盖原本的内容，实际上是把**要覆盖的数据块**的元数据标记为旧数据，同时只上传随机写时产生的**新数据块**到对象存储，并将**新数据块**对应的元数据更新到元数据引擎中。

当读取被覆盖部分的数据时，根据**最新的元数据**，从随机写时上传的**新数据块**读取即可，同时**旧数据块**可能会被后台运行的垃圾回收任务自动清理。这样就将随机写的复杂度转移到读的复杂度上，。

这个只是很粗略的实现逻辑介绍，具体的读写流程非常复杂，可以研读[「JuiceFS 内部实现」](development/data_structures.md)与[「读写请求处理流程」](introduction/io_processing.md)这两篇文档并结合代码一起梳理。

### 怎么快速地拷贝大量小文件到 JuiceFS？

请在挂载时加上 [`--writeback` 选项](reference/command_reference.md#mount)，它会先把数据写入本机的缓存，然后再异步上传到对象存储，会比直接上传到对象存储快很多倍。

请查看[「客户端写缓存」](guide/cache_management.md#writeback)了解更多信息。

### JuiceFS 目前支持分布式缓存吗？

到 JuiceFS 1.0 为止，还不支持该功能。

## 访问相关问题

### 数据更新什么时候会对其它客户端可见？

所有的元数据更新都是立即对其它客户端可见。JuiceFS 保证关闭再打开（close-to-open）一致性，请查看[「一致性」](guide/cache_management.md#consistency)了解更多信息。

通过 `write()` 新写入的数据会缓存在内核和客户端中，可以被当前机器的其它进程看到，其它机器暂时看不到。

调用 `fsync()`、`fdatasync()` 或者 `close()` 来强制将数据上传到对象存储并更新元数据，或者数秒钟自动刷新后，其它客户端才能看到更新，这也是绝大多数分布式文件系统采取的策略。

请查看[「客户端写缓存」](guide/cache_management.md#writeback)了解更多信息。

### 为什么同一个用户在主机 X 上有权限访问 JuiceFS 的文件，在主机 Y 上访问该文件却没有权限？

该用户在主机 X 和主机 Y 上的 UID 或者 GID 不一样。使用 `id` 命令可以显示用户的 UID 和 GID：

```bash
$ id alice
uid=1201(alice) gid=500(staff) groups=500(staff)
```

阅读文档[「多主机间同步账户」](administration/sync_accounts_between_multiple_hosts.md)解决这个问题。

### JuiceFS 除了挂载外还支持哪些方式访问数据？

除了挂载外，还支持以下几种方式：

- Kubernetes CSI 驱动：通过 Kubernetes CSI 驱动的方式将 JuiceFS 作为 Kubernetes 集群的存储层，详情请参考[「JuiceFS CSI 驱动」](deployment/how_to_use_on_kubernetes.md)。
- Hadoop Java SDK：方便在 Hadoop 体系中使用兼容 HDFS 接口的 Java 客户端访问 JuiceFS。详情请参考[「Hadoop 使用 JuiceFS」](deployment/hadoop_java_sdk.md)。
- S3 网关：通过 S3 协议访问 JuiceFS，详情请参考[「配置 JuiceFS S3 网关」](deployment/s3_gateway.md)。
- Docker Volume 插件：在 Docker 中方便使用 JuiceFS 的方式，详情请参考[「Docker 使用 JuiceFS」](deployment/juicefs_on_docker.md)。
- WebDAV 网关：通过 WebDAV 协议访问 JuiceFS

### JuiceFS S3 网关支持多用户管理等高级功能吗？

JuiceFS 内置的 `gateway` 子命令不支持多用户管理等功能，只提供基本的 S3 网关功能。如果需要使用这些高级功能，请参考[文档](deployment/s3_gateway.md#使用功能更完整的-s3-网关)。

### JuiceFS 目前有 SDK 可以使用吗？

截止到 JuiceFS 1.0 发布，社区有两个 SDK，一个是 Juicedata 官方维护的 HDFS 接口高度兼容的 [Java SDK](deployment/hadoop_java_sdk.md)，另一个是由社区用户维护的 [Python SDK](https://github.com/megvii-research/juicefs-python)。
