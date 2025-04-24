---
sidebar_label: etcd
sidebar_position: 4
slug: /etcd_best_practices
---

# etcd Best Practices

## Data size

By default, etcd sets a [space quota](https://etcd.io/docs/latest/op-guide/maintenance/#space-quota) of 2GB, which can support storing metadata of two million files. Adjusted via the `--quota-backend-bytes` option, [official suggestion](https://etcd.io/docs/latest/dev-guide/limit) do not exceed 8GB.

By default, etcd will keep the modification history of all data until the amount of data exceeds the space quota and the service cannot be provided. It is recommended to add the following options to enable [automatic compaction](https://etcd.io/docs/latest/op-guide/maintenance/#auto-compaction):

````
--auto-compaction-mode revision --auto-compaction-retention 1000000
````

When the amount of data reaches the quota and cannot be written, the capacity can be reduced by manual compaction (`etcdctl compact`) and defragmentation (`etcdctl defrag`). **It is strongly recommended to perform these operations on the nodes of the etcd cluster one by one, otherwise the entire etcd cluster may become unavailable.**

## Performance

etcd provides strongly consistent read and write access, and all operations involve multi-machine transactions and disk data persistence. **It is recommended to use high-performance SSD for deployment**, otherwise it will affect the performance of the file system. For more hardware configuration suggestions, please refer to [official documentation](https://etcd.io/docs/latest/op-guide/hardware).

If the etcd cluster has power-down protection, or other measures that can ensure that all nodes will not go down at the same time, you can also disable data synchronization and disk storage through the `--unsafe-no-fsync` option to reduce access latency and improve files system performance. **At this time, if two nodes are down at the same time, there is a risk of data loss.**

## Kubernetes

It is recommended to build an independent etcd service in the Kubernetes environment for JuiceFS to use, instead of using the default etcd service in the cluster, to avoid affecting the stability of the Kubernetes cluster when the file system access pressure is high.
