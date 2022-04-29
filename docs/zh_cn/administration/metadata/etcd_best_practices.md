---
sidebar_label: etcd
sidebar_position: 3
slug: /etcd_best_practices
---
# etcd 最佳实践

## 数据规模

etcd 默认有 2GB 的数据量限制，大概能够支撑两百万文件，可以通过 `--quota-backend-bytes` 参数进行调整，建议不要超过 8GB。

默认情况下，etcd 会保留所有数据的修改历史，直到数据量超过存储限制导致无法提供服务，建议加上如下参数启用自动数据合并：

```
   --auto-compaction-mode revision --auto-compaction-retention 1000000
```

当数据量达到限制时导致无法写入时，可以通过手动压缩（etcdctl compact）和整理碎片（etcdctl defrag）的方式来减少容量。在做这些操作时，强烈建议对 etcd 集群的节点逐个进行，否则可能会导致整个 etcd 集群不可用。

## 性能

etcd 提供强一致的读写访问，并且所有操作都会涉及到多机事务以及磁盘的数据持久化，建议使用高性能的 SSD 来部署，否则会影响到文件系统的性能。

如果 etcd 集群都有掉电保护，或者其他能够保证不会导致所有节点同时宕机的措施，也可以通过 `--unsafe-no-fsync` 参数关闭数据同步落盘，以降低访问时延提高文件系统的性能。此时如果有两个节点同时宕机，会有数据丢失风险。

## Kubernetes

建议在 Kubernetes 环境中搭建独立的 etcd 服务给 JuiceFS 使用，而不是使用集群中默认的 etcd 服务，避免当文件系统访问压力高时影响 Kubernetes 集群的稳定性。
