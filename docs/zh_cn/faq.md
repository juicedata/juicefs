# FAQ

## 为什么不支持某个对象存储？

已经支持了绝大部分对象存储，参考这个[列表](how_to_setup_object_storage.md#支持的存储服务)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请创建一个 issue 来增加支持。

## 是否可以使用 Redis 集群版？

不可以。JuiceFS 使用了 Redis 的[事务功能](https://redis.io/topics/transactions)来保证元数据操作的原子性，而分布式版还不支持分布式事务。哨兵节点或者其它的 Redis 高可用方法是需要的。

请查看[「Redis 最佳实践」](redis_best_practices.md)了解更多信息。

## JuiceFS 与 XXX 的区别是什么？

请查看[「同类技术对比」](comparison_with_others.md)文档了解更多信息。

## JuiceFS 的性能如何？

JuiceFS 是一个分布式文件系统，元数据访问的延时取决于挂载点到服务端之间 1 到 2 个网络来回（通常 1-3 ms），数据访问的延时取决于对象存储的延时 (通常 20-100 ms)。顺序读写的吞吐量可以到 50MiB/s 至 2800MiB/s（查看 [fio 测试结果](fio.md)），取决于网络带宽以及数据是否容易被压缩。

JuiceFS 内置多级缓存（主动失效），一旦缓存预热好，访问的延时和吞吐量非常接近单机文件系统的性能（FUSE 会带来少量的开销）。

## JuiceFS 支持随机读写吗？

支持，包括通过 mmap 等进行的随机读写。目前 JuiceFS 主要是对顺序读写进行了大量优化，对随机读写的优化也在进行中。如果想要更好的随机读性能，建议关闭压缩（[`--compress none`](command_reference.md#juicefs-format)）。

## 数据更新什么时候会对其它客户端可见？

所有的元数据更新都是立即对其它客户端可见。JuiceFS 保证关闭再打开（close-to-open）一致性，请查看[「一致性」](cache_management.md#一致性)了解更多信息。

通过 `write()` 新写入的数据会缓存在内核和客户端中，可以被当前机器的其它进程看到，其它机器暂时看不到。

调用 `fsync()`、`fdatasync()` 或者 `close()` 来强制将数据上传到对象存储并更新元数据，或者数秒钟自动刷新后，其它客户端才能看到更新，这也是绝大多数分布式文件系统采取的策略。

请查看[「客户端写缓存」](cache_management.md#客户端写缓存)了解更多信息。

## 怎么快速地拷贝大量小文件到 JuiceFS？

请在挂载时加上 [`--writeback` 选项](command_reference.md#juicefs-mount)，它会先把数据写入本机的缓存，然后再异步上传到对象存储，会比直接上传到对象存储快很多倍。

请查看[「客户端写缓存」](cache_management.md#客户端写缓存)了解更多信息。

## 可以用 `root` 以外的用户挂载吗？

可以，JuiceFS 可以由任何用户挂载。默认的缓存目录是 `$HOME/.juicefs/cache`（macOS）或者 `/var/jfsCache`（Linux），请确保该用户对这个目录有写权限，或者切换到其它有权限的目录。

请查看[「客户端读缓存」](cache_management.md#客户端读缓存)了解更多信息。

## 怎么卸载 JuiceFS 文件系统？

请使用 [`juicefs umount`](command_reference.md#juicefs-umount) 命令卸载。

## 怎么升级 JuiceFS 客户端？

首先请卸载 JuiceFS 文件系统，然后使用新版本的客户端重新挂载。

## `docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.`

当你使用 [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) 把宿主机上的一个目录挂载到容器中时，你可能会遇到这个错误。这是因为使用了非 root 用户执行了 `juicefs mount` 命令，进而导致 Docker 没有权限访问这个目录。

这个问题有两种解决方法：

1. 用 root 用户执行 `juicefs mount` 命令
2. 修改 FUSE 的配置文件以及增加 `allow_other` 挂载选项，请查看[这个文档](fuse_mount_options.md#allow_other)了解更多信息。

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

阅读文档[「多主机间同步账户」](sync_accounts_between_multiple_hosts.md)解决这个问题。
