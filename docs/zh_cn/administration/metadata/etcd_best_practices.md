---
sidebar_label: etcd
sidebar_position: 4
slug: /etcd_best_practices
---

# etcd 最佳实践

## 数据规模

etcd 默认设置了 2GB 的[存储配额](https://etcd.io/docs/latest/op-guide/maintenance/#space-quota)，大概能够支撑存储两百万文件的元数据，可以通过 `--quota-backend-bytes` 选项进行调整，[官方建议](https://etcd.io/docs/latest/dev-guide/limit)不要超过 8GB。

默认情况下，etcd 会保留所有数据的修改历史，直到数据量超过存储配额导致无法提供服务，建议加上如下选项启用[自动数据合并](https://etcd.io/docs/latest/op-guide/maintenance/#auto-compaction)：

```
--auto-compaction-mode revision --auto-compaction-retention 1000000
```

当数据量达到配额导致无法写入时，可以通过手动压缩（`etcdctl compact`）和整理碎片（`etcdctl defrag`）的方式来减少容量。**强烈建议对 etcd 集群的节点逐个进行这些操作，否则可能会导致整个 etcd 集群不可用。**

## 性能

etcd 提供强一致的读写访问，并且所有操作都会涉及到多机事务以及磁盘的数据持久化。**建议使用高性能的 SSD 来部署**，否则会影响到文件系统的性能。更多硬件配置建议请参考[官方文档](https://etcd.io/docs/latest/op-guide/hardware)。

如果 etcd 集群都有掉电保护，或者其它能够保证不会导致所有节点同时宕机的措施，也可以通过 `--unsafe-no-fsync` 选项关闭数据同步落盘，以降低访问时延提高文件系统的性能。**此时如果有两个节点同时宕机，会有数据丢失风险。**

## Kubernetes

建议在 Kubernetes 环境中搭建独立的 etcd 服务供 JuiceFS 使用，而不是使用集群中默认的 etcd 服务，避免当文件系统访问压力高时影响 Kubernetes 集群的稳定性。
