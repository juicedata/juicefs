---
sidebar_label: TiKV
sidebar_position: 4
slug: /tikv_best_practices
---
# TiKV 最佳实践

TiKV 通过 Raft 协议保证多副本数据一致性以及高可用，所以建议生产环境中至少部署三个以上副本以保证数据安全和服务稳定。
TiKV 有很好的横向扩容能力，适用于大规模且对性能有一定要求的文件系统场景。

## 垃圾回收

TiKV 原生支持了 MVCC（多版本并发控制）机制，当新写入的数据覆盖旧的数据时，旧的数据不会被替换掉，而是与新写入的数据同时保留，并以时间戳来区分版本。Garbage Collection (GC) 的任务便是清理不再需要的旧数据。

### JuiceFS 的配置

TiKV 根据一个集群变量 `safe-point`（时间戳）来决定是否要清理某个时间之前的旧版本数据。JuiceFS 在 v1.0.4 之前不会设置`safe-point`，TiKV 元数据引擎需要依赖 TiDB 才能正常进行垃圾回收。而在 v1.0.4 之后，JuiceFS 客户端会周期性地设置 `safe-point`，默认会清除三小时之前的旧版本数据，这个时间可在挂载时通过 meta url 的 `gc-interval` 设置。

- 默认 `gc-interval` 的挂载 log

```bash
> sudo ./juicefs mount tikv://localhost:2379 ~/mnt/jfs
2023/04/06 20:23:34.741432 juicefs[17286] <INFO>: Meta address: tikv://localhost:2379 [interface.go:491]
2023/04/06 20:23:34.741561 juicefs[17286] <INFO>: TiKV gc interval is set to 3h0m0s [tkv_tikv.go:84]
...
```

- 设置 `gc-interval` 后的挂载 log

```bash
> sudo ./juicefs mount tikv://localhost:2379\?gc-interval=1h ~/mnt/jfs
2023/04/06 20:25:58.134999 juicefs[17395] <INFO>: Meta address: tikv://localhost:2379?gc-interval=1h [interface.go:491]
2023/04/06 20:25:58.135113 juicefs[17395] <INFO>: TiKV gc interval is set to 1h0m0s [tkv_tikv.go:84]
...
```

#### 主动设置 `safe-point`

JuiceFS 客户端会周期性设置 `safe-point`，除此之外我们也可以通过 gc 子命令来主动设置。

```bash
> ./juicefs gc -v tikv://localhost:2379\?gc-interval=1h --delete
...
2023/04/06 20:41:57.145692 juicefs[18531] <DEBUG>: TiKV GC returns new safe point: 440606737600086016 (2023-04-06 19:41:57.139 +0800 CST) [tkv_tikv.go:248]
...
```

:::tip 提示
此命令同时会清理 JuiceFS 产生的垃圾数据，比如因为 IO pending 而延迟删除的元数据、因为事务回滚而泄漏的文件数据等。使用此命令之前请确保不需要回滚到旧版本文件系统，并且建议您备份元数据。
:::

### TiKV 的垃圾回收模式

- gc-worker

可以在通过 TiKV 配置来启用 gc-worker。gc-worker 模式下垃圾会被及时回收，但大量额外的磁盘磁盘读写可能会影响元数据引擎性能。

```toml
[gc]
enable-compaction-filter = false
```

- compaction-filter

TiKV 默认通过 [compaction-filter](https://docs.pingcap.com/zh/tidb/dev/garbage-collection-configuration#gc-in-compaction-filter-%E6%9C%BA%E5%88%B6) 进行垃圾回收，由 RocksDB 的 Compaction 过程来进行 GC，而不再使用一个单独的 GC worker 线程。这样做的好处是避免了 GC 引起的额外磁盘读取，以及避免清理掉的旧版本残留大量删除标记影响顺序扫描性能。

由于此回收模式依赖 RocksDB compaction，所以设置`safe-point`之后垃圾并不会被及时回收，需要后续持续写入触发 compaction 才能进行 GC。如果您需要主动触发 GC，可以通过 [`tikv-ctl`](https://docs.pingcap.com/zh/tidb/dev/tikv-control) 工具主动进行集群 compaction，从而触发全局 GC。

```bash
> tikv-ctl --pd 127.0.0.1:2379 compact-cluster -b -c default,lock,write
```

## 元数据备份
对于大规模文件系统，需要调高 [tikv_gc_life_time](https://docs.pingcap.com/zh/tidb/stable/dev-guide-timeouts-in-tidb#gc-%E8%B6%85%E6%97%B6) 参数，否则可能会因为 `GC life time is shorter than transaction duration` 导致备份失败。
