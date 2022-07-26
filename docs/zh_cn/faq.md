# FAQ

## 为什么不支持某个对象存储？

已经支持了绝大部分对象存储，参考这个[列表](guide/how_to_setup_object_storage.md#支持的存储服务)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请创建一个 issue 来增加支持。

## Redis 的 sentinel 或者 cluster 模式支持作为 JuiceFS 元数据引擎吗？

支持，另外这里还有一篇 [Redis 作为 JuiceFS 元数据引擎的最佳实践](https://juicefs.com/docs/zh/community/redis_best_practices)文章可供参考。

## JuiceFS 与 XXX 的区别是什么？

请查看[「同类技术对比」](introduction/comparison/juicefs_vs_alluxio.md)文档了解更多信息。

## JuiceFS 的性能如何？

JuiceFS 是一个分布式文件系统，元数据访问的延时取决于挂载点到服务端之间 1 到 2 个网络来回（通常 1-3 ms），数据访问的延时取决于对象存储的延时 (通常 20-100 ms)。顺序读写的吞吐量可以到 50MiB/s 至 2800MiB/s（查看 [fio 测试结果](benchmark/fio.md)），取决于网络带宽以及数据是否容易被压缩。

JuiceFS 内置多级缓存（主动失效），一旦缓存预热好，访问的延时和吞吐量非常接近单机文件系统的性能（FUSE 会带来少量的开销）。

## 为什么在对象存储中看不到存入 JuiceFS 的原文件？
使用 JuiceFS，文件最终会被拆分成 Chunks、Slices 和 Blocks 存储在对象存储。因此，你会发现在对象存储平台的文件浏览器中找不到存入 JuiceFS 的源文件，存储桶中只有一个 chunks 目录和一堆数字编号的目录和文件。不要惊慌，这正是 JuiceFS 文件系统高性能运作的秘诀！详情参考 [JuiceFS 如何存储文件](https://juicefs.com/docs/zh/community/how_juicefs_store_files)。

## JuiceFS 支持随机读写吗？

支持，包括通过 mmap 等进行的随机读写。目前 JuiceFS 主要是对顺序读写进行了大量优化，对随机读写的优化也在进行中。如果想要更好的随机读性能，建议关闭压缩（[`--compress none`](reference/command_reference.md#juicefs-format)）。

## JuiceFS 随机写的基本原理是什么？

JuiceFS 不将原始对象传入对象存储，而是将其按照默认 4M 的大小拆分为 N 块后编号上传到对象存储，然后将编号存入元数据引擎。随机写的时候，逻辑上是要覆盖原本的内容，实际上是把覆盖的那段标记为旧数据，上传随机写部分的到对象存储，当该要读到旧数据部分的时候，从刚刚上传的随机写的部分读新数据即可。这样就将随机写的复杂度转移到读的复杂度上。这个只是宏观的实现逻辑，具体的读写流程可以研读 [JuiceFS内部实现](https://juicefs.com/docs/zh/community/internals/)与[读写流程](https://juicefs.com/docs/zh/community/internals/io_processing)两篇文章并配合代码梳理。

## 为什么我在挂载点删除了文件，但是对象存储占用空间没有变化或者变化很小？

第一个原因是你可能开起了回收站，为了保证数据安全回收站默认开启，删除的文件其实被放到了回收站，实际并没有被删除，所以对象存储大小不会变化。回收站可以通过 `juicefs format` 指定或者通过 `juicefs config` 修改。关于回收站功能请参考[回收站使用文档](https://juicefs.com/docs/zh/community/security/trash/)。

第二个原因是 JuiceFS 删除对象存储是异步删除。所以对象存储的变化会慢一点。

## 为什么挂载点显示的大小与对象存储占用空间存在差异？

通过 [JuiceFS 随机写的基本原理是什么](##JuiceFS 随机写的基本原理是什么) 这个问题的答案可以推断出，对象存储的占用空间大部分情况下是大于等于实际大小的，尤其是短时间内进行大量的覆盖写产生许多文件碎片后。这些碎片在未触发合并与回收前其仍旧占用着对象存储的空间。不过也不必担心这些碎片一直占用空间，因为在每次读文件的时候都会检查并在必要的时候触发该文件相关碎片的整理工作。另外你可以通过 `juicefs gc —-compact -—delete` 命令手动触发合并与回收。

## 数据更新什么时候会对其它客户端可见？

所有的元数据更新都是立即对其它客户端可见。JuiceFS 保证关闭再打开（close-to-open）一致性，请查看[「一致性」](guide/cache_management.md#一致性)了解更多信息。

通过 `write()` 新写入的数据会缓存在内核和客户端中，可以被当前机器的其它进程看到，其它机器暂时看不到。

调用 `fsync()`、`fdatasync()` 或者 `close()` 来强制将数据上传到对象存储并更新元数据，或者数秒钟自动刷新后，其它客户端才能看到更新，这也是绝大多数分布式文件系统采取的策略。

请查看[「客户端写缓存」](guide/cache_management.md#客户端写缓存)了解更多信息。

## 怎么快速地拷贝大量小文件到 JuiceFS？

请在挂载时加上 [`--writeback` 选项](reference/command_reference.md#juicefs-mount)，它会先把数据写入本机的缓存，然后再异步上传到对象存储，会比直接上传到对象存储快很多倍。

请查看[「客户端写缓存」](guide/cache_management.md#客户端写缓存)了解更多信息。

## 可以用 `root` 以外的用户挂载吗？

可以，JuiceFS 可以由任何用户挂载。默认的缓存目录是 `$HOME/.juicefs/cache`（macOS）或者 `/var/jfsCache`（Linux），请确保该用户对这个目录有写权限，或者切换到其它有权限的目录。

请查看[「客户端读缓存」](guide/cache_management.md#客户端读缓存)了解更多信息。

## 怎么卸载 JuiceFS 文件系统？

请使用 [`juicefs umount`](reference/command_reference.md#juicefs-umount) 命令卸载。

## 卸载挂载点报 `Resource busy -- try 'diskutil unmount'` 错误

这代表有挂载点下的某个文件或者目录正在被使用，无法直接 `umount`，可以检查下是否有终端在挂载点或者挂载点下的目录，如果有退出或者关闭终端后再使用 `juicefs umount` 即可。

## 怎么升级 JuiceFS 客户端？

首先请卸载 JuiceFS 文件系统，然后使用新版本的客户端重新挂载。

## `docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.`

当你使用 [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) 把宿主机上的一个目录挂载到容器中时，你可能会遇到这个错误。这是因为使用了非 root 用户执行了 `juicefs mount` 命令，进而导致 Docker 没有权限访问这个目录。

这个问题有两种解决方法：

1. 用 root 用户执行 `juicefs mount` 命令
2. 修改 FUSE 的配置文件以及增加 `allow_other` 挂载选项，请查看[这个文档](reference/fuse_mount_options.md#allow_other)了解更多信息。

## `/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1` 或者 `/go/pkg/tool/linux_amd64/compile: signal: killed`

这个错误有可能是因为 GCC 版本过低导致，请尝试升级 GCC 到 5.4 及以上版本。

## `format: ERR wrong number of arguments for 'auth' command`

这个错误意味着你使用的 Redis 版本小于 6.0.0 同时在执行 `juicefs format` 命令时指定了 username 参数。只有 Redis 6.0.0 版本以后才支持指定 username，因此你需要省略 URL 中的 username 参数，例如 `redis://:password@host:6379/1`。

## `fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory`

这个错误意味着使用了非 root 用户执行 `juicefs mount` 命令，并且 `fusermount` 这个命令也找不到。

这个问题有两种解决方法：

1. 用 root 用户执行 `juicefs mount` 命令
2. 安装 `fuse` 包（例如 `apt-get install fuse`、`yum install fuse`）

## `fuse: fuse: fork/exec /usr/bin/fusermount: permission denied`

这个错误意味着当前用户没有执行 `fusermount` 命令的权限。例如，你可以通过下面的命令检查 `fusermount` 命令的权限：

```sh
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount
```

上面的例子表示只有 root 用户和 `fuse` 用户组的用户有权限执行。另一个例子：

```sh
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

上面的例子表示所有用户都有权限执行。

## 为什么同一个用户在主机 X 上有权限访问 JuiceFS 的文件，在主机 Y 上访问该文件却没有权限？

该用户在主机 X 和主机 Y 上的 UID 或者 GID 不一样。使用 `id` 命令可以显示用户的 UID 和 GID：

```bash
$ id alice
uid=1201(alice) gid=500(staff) groups=500(staff)
```

阅读文档[「多主机间同步账户」](administration/sync_accounts_between_multiple_hosts.md)解决这个问题。

## JuiceFS 除了普通挂载外还支持哪些方式访问数据？

除了普通挂载外，还支持以下几种方式：

- S3 Gateway 方式: 通过 S3 协议访问 JuiceFS，详情参考 [JuiceFS S3 gateway 使用指南](https://juicefs.com/docs/zh/community/s3_gateway)
- Webdav 方式：通过 webdav 协议访问 JuiceFS
- Docker Volume Plugin：在 Docker 中方便使用 JuiceFS 的方式，关于如何再 Docker 中使用使用 JuiceFS 请参考 [Docker 使用 JuiceFS 指南](https://juicefs.com/docs/zh/community/juicefs_on_docker/)
- CSI Driver：通过 Kubernetes CSI Driver 方式将 JuiceFS 用为 Kubernetes 集群的存储层，详情参考 [Kubernetes 使用 JuiceFS 指南](https://juicefs.com/docs/zh/community/how_to_use_on_kubernetes)
- Hadoop SDK： 方便在 Hadoop 体系中使用的 HDFS 接口[高度兼容](https://hadoop.apache.org/docs/current/hadoop-project-dist/hadoop-common/filesystem/introduction.html)的 Java 客户端。详情参考[在 Hadoop 中使用 JuiceFS](https://juicefs.com/docs/zh/community/hadoop_java_sdk)

## JuiceFS 的日志在哪里？

JuiceFS 后台挂载的时候日志才会写入日志文件，前台挂载或者其他前台的命令都会将日志直接打印到终端。

- Mac 系统上日志文件默认是 `/Users/$User/.juicefs/juicefs.log`
- Linux 系统上 root 用户启动时日志文件默认是 `/var/log/juicefs.log`，非 root 用户启动日志文件默认是 `~/.juicefs/juicefs.log`

## 如何销毁一个文件系统？

使用 `juicefs destroy` 销毁一个文件系统，该命令将会清空元数据引擎与对象存储中的相关数据。关于该命令的使用详情请参考这个[帮助文档](https://juicefs.com/docs/zh/community/administration/destroy)

## JuiceFS gateway 支持多用户管理等高级功能吗？

JuiceFS 内置的 gateway 子命令不支持多用户管理等功能，只提供基本的 S3 Gateway 功能。如果需要使用这些高级功能，可以参考我们的这个[仓库](https://github.com/juicedata/minio/tree/gateway)，其将 JuiceFS 作为 MinIO gateway 的一种实现，支持 MinIO gateway 的完整功能。

## JuiceFS 支持使用对象存储中的某个目录作为 `—-bucket` 参数吗？

到 JuiceFS 1.0 为止，还不支持该功能。

## JuiceFS 支持对接对象存储中已经存在的数据吗？

到 JuiceFS 1.0 为止，还不支持该功能。

## JuiceFS 目前支持分布式缓存吗？

到 JuiceFS 1.0 为止，还不支持该功能。

## JuiceFS 目前有 SDK 可以使用吗？

截止到 JuiceFS 1.0 发布，社区有两个 SDK，一个是 JuiceFS 官方维护的 HDFS 接口高度兼容的 [Java SDK](https://juicefs.com/docs/zh/community/hadoop_java_sdk)，另一个是由社区用户维护的 [Python SDK](https://github.com/megvii-research/juicefs-python)。
