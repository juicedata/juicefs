# FAQ

## 为什么不支持某个对象存储？

已经支持了绝大部分对象存储，参考这个[列表](../en/how_to_setup_object_storage.md#supported-object-storage)。如果它跟 S3 兼容的话，也可以当成 S3 来使用。否则，请创建一个 issue 来增加支持。

## 是否可以使用 Redis 集群版？

不可以。JuiceFS 使用了 Redis 的[事务功能](https://redis.io/topics/transactions)来保证元数据操作的原子性，而分布式版还不支持分布式事务。哨兵节点或者其它的 Redis 高可用方法是需要的。

## JuiceFS 与 XXX 的区别是什么？

请查看[「与其它项目比较」](../en/comparison_with_others.md)文档了解更多信息。


## JuiceFS 的性能如何？

JuiceFS 是一个分布式文件系统，元数据访问的延时取决于挂载点到服务端之间 1 到 2 个网络来回（通常 1~3 ms），数据访问的延时取决于对象存储的延时 (通常 20~100 ms)。顺序读写的吞吐量可以到 50MiB/s 至 2800MiB/s（查看 [fio 测试结果](../en/fio.md)），取决于网络带宽以及数据是否容易被压缩。

JuiceFS 内置多级缓存（主动失效），一旦缓存预热好，访问的延时和吞吐量非常接近单机文件系统的性能（FUSE 会带来少量的开销）。

## JuiceFS 支持随机读写吗？

支持，包括通过 mmap 等进行的随机读写。目前 JuiceFS 主要是对顺序读写进行了大量优化，对随机读写的优化也在进行中。

## 数据更新什么时候会对其它客户端可见？

所有的元数据更新都是立即对其它客户端可见。通过 `write()` 新写入的数据会缓存在内核和客户端中，可以被当前机器的其它进程看到，其它机器暂时看不到。

而一定时间之后，调用 `fdatasync()` 或者 `close()` 来强制将数据上传到对象存储并更新元数据，其它客户端才能看到更新，这也是绝大多数分布式文件系统采取的策略。

请查看[「客户端写缓存」](../en/cache_management.md#write-cache-in-client)了解更多信息。

## 怎么快速地拷贝大量小文件到 JuiceFS？

请在挂载时加上 [`--writeback` 选项](../en/command_reference.md#juicefs-mount)，它会先把数据写入本机的缓存，然后再异步上传到对象存储，会比直接上传到对象存储快很多倍。

请查看[「客户端写缓存」](../en/cache_management.md#write-cache-in-client)了解更多信息。

## 可以用 `root` 以外的用户挂载吗？

可以，JuiceFS 可以由任何用户挂载。默认的缓存目录是 `$HOME/.juicefs/cache`（macOS）或者 `/var/jfsCache`（Linux），请确保该用户对这个目录有写权限，或者切换到其它有权限的目录。

请查看[「客户端读缓存」](../en/cache_management.md#read-cache-in-client)了解更多信息。

## 怎么卸载 JuiceFS 文件系统？

请使用 [`juicefs umount`](command_reference.md#juicefs-umount) 命令卸载。

## 怎么升级 JuiceFS 客户端？

首先请卸载 JuiceFS 文件系统，然后使用新版本的客户端重新挂载。
