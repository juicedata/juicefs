---
sidebar_label: TiKV
sidebar_position: 4
slug: /tikv_best_practices
---
# TiKV 最佳实践

TiKV 通过 Raft 协议保证多副本数据一致性以及高可用，所以建议生产环境中至少部署三个以上副本以保证数据安全和服务稳定。
TiKV 有很好的横向扩容能力，适用于大规模且对性能有一定要求的文件系统场景。

## 垃圾回收

TiKV 原生支持了 MVCC（多版本并发控制）机制，当新写入的数据覆盖旧的数据时，旧的数据不会被替换掉，而是与新写入的数据同时保留，并以时间戳来区分版本。垃圾回收 (GC) 的任务便是清理不再需要的旧数据。

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
此命令同时会清理 JuiceFS 产生的「泄漏对象」和「待清理对象」，请参考[状态检查 & 维护](../status_check_and_maintenance.md#gc)以确认您是否应该使用。
:::

### TiKV 的垃圾回收模式

- gc-worker

可以在通过 TiKV 配置来启用 gc-worker。gc-worker 模式下垃圾会被及时回收，但大量额外的磁盘读写可能会影响元数据引擎性能。

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

## 运行环境与调优

### 硬件选型

根据[TiDB 软件和硬件环境建议配置](https://docs.pingcap.com/zh/tidb/stable/hardware-and-software-requirements)，TiKV 支持部署和运行在 Intel x86-64 架构的 64 位通用硬件服务器平台或者 ARM 架构的硬件服务器平台。对于开发、测试及生产环境的服务器硬件配置（不包含操作系统 OS 本身的占用）有以下要求和建议：

+ **开发与测试环境**

| 组件 |CPU| 内存 | 本地存储 | 网络 | 实例数量 (最低要求)|
|-|-|-|-|-|-|
|PD|4 核 +|8 GB+|SAS, 200 GB+| 千兆网卡 |1|
|TiKV|8 核 +|32 GB+|SSD, 200 GB+| 千兆网卡 |3|

:::note 说明

+ 如进行性能相关的测试，避免采用低性能存储和网络硬件配置，防止对测试结果的正确性产生干扰。
+ TiKV 的 SSD 盘推荐使用 NVME 接口以保证读写更快。

:::

+ **生产环境**

| 组件 |CPU| 内存 | 本地存储 | 网络 | 实例数量 (最低要求)|
|-|-|-|-|-|-|
|PD|8 核 +|16 GB+|SSD| 万兆网卡（2 块最佳）|3|
|TiKV|16 核 +|64 GB+|SSD| 万兆网卡（2 块最佳）|3|

:::note 说明
TiKV 硬盘大小配置建议 PCI-E SSD 不超过 2 TB，普通 SSD 不超过 1.5 TB。
:::

### 网络要求

TiKV 正常运行需要网络环境提供如下的网络端口配置要求，管理员可根据实际环境中组件部署的方案，在网络侧和主机侧开放相关端口：

| 组件 | 默认端口 | 说明 |
|-|-|-|
|TiKV|20160|TiKV 通信端口 |
|TiKV|20180|TiKV 状态信息上报通信端口 |
|PD|2379| 提供 TiDB 和 PD 通信端口 |
|PD|2380|PD 集群节点间通信端口 |

### 磁盘空间要求

| 组件 | 磁盘空间要求 | 健康水位使用率 |
|-|-|-|
|PD| 数据盘和日志盘建议最少各预留 20 GB| 低于 90%|
|TiKV| 数据盘和日志盘建议最少各预留 100 GB| 低于 80%|

## 硬件调优

各种数据库官方都有硬件有一定要求，TiKV 等组件都有最低的 CPU、内存、硬盘、网卡要求。本章节在满足这些需求的基础上，探讨下硬件参数优化，主要参考[数据库硬件调优](https://tidb.net/book/tidb-monthly/2022/2022-03/usercase/tuning-hardware)。

### CPU

+ **CPU 选型**

可以分为计算型和存储型。计算型往往需要更多的 CPU 核心和更高的主频。存储型的 CPU 可能就配置稍微低些。对于计算型和存储型 CPU 选择，拿 JuiceFS 的使用场景来说，PD 和 TiKV 以存储型为主，没有太高的计算负载，可以提前规划使得硬件采购更加合理，节省成本。

+ **CPU 架构：X86/ARM**

X86 架构出现在 intel/AMD 的 CPU 架构中，采用复杂指令集，也是目前最主流服务器的 CPU 架构。ARM 架构 CPU 在手机，mac 笔记本，以及华为等国产服务器厂商中出现。目前各大公司主要采购的是 X86-64 架构的 CPU，也对 ARM 服务器进行了 web 和数据库应用的验证。TiKV 对两种架构均有支持，可根据实际部署情况进行选择。

+ **Numa 绑核**

多核心 CPU 的各核心会被分配到不同的 NUMA node，每个 NUMA node 都有自己专属/本地的主存，访问本地的主存比其跨 NUMA node 访问内存更快，开启 NUMA 会优先就近使用内存。在单机多节点部署时推荐此配置。

+ **CPU-动态节能技术**

cpufreq 是一个动态调整 CPU 频率的模块，可支持五种模式。为保证服务性能应选用 performance 模式，将 CPU 频率固定工作在其支持的最高运行频率上，从而获取最佳的性能，一般都是默认 powersave，可以通过 cpupower frequency-set 修改。

### Memory

+ **关闭 Swap**

swap 用硬盘来承接到达一定阀值的内存访问，由 `vm.swappiness` 参数控制，默认 60，也就是系统内存使用到 40% 时开始使用，TiKV 运行需要有足够的内存。如果内存不足，不建议使用 swap 作为内存不足的缓冲，因为这会降低性能。建议关闭系统 swap。

+ **设置`min_free_kbytes`**

`min_free_kbytes` 内核参数控制了多少内存应该保持空闲而不被文件系统缓存占用。通常情况下，内核会用文件系统缓存占据几乎所有的空闲内存，并根据需要释放内存供进程分配。由于数据库会共享内存中执行大量的分配，默认的内核值可能会导致意外的 OOM（Out-of-Memory kill），在总内存大于 40G 的情况下，建议将该参数配置为至少 1GB，但是不建议超过总内存的 5%，这可以确保 Linux 始终保持足够的内存可用。

+ **关闭透明大页（Transparent Huge Pages，THP）**

数据库的内存访问模式往往是稀疏的而非连续的。当高阶内存碎片化比较严重时，分配 THP 页面会出现较高的延迟，若开启针对 THP 的直接内存规整功能，也会出现系统 CPU 使用率激增的现象，因此建议关闭 THP。

+ **调整虚拟内存 `dirty_ratio`/`dirty_background_ratio` 参数**

`dirty_ratio` 是绝对的脏页百分比值限限制。当脏的 page cache 总量达到系统内存总量的这一百分比后，系统将开始使用 pdflush 操作将脏的 page cache 写入磁盘。默认值为 20％，也就是说如果到达该值时可能会导致应用进程的 IO 等待，通常不需调整。

`dirty_background_ratio` 百分比值。当脏的 page cache 总量达到系统内存总量的这一百分比后，系统开始在后台将脏的 page cache 写入磁盘。默认值为 10％，如果后台刷脏页的慢，而数据写的快就容易触发 dirty_ratio 的限制。通常不需调整。对于高性能 SSD，比如 NVMe 设备来说，设置较低的值有利于提高内存回收时的效率。

### 数据存储

#### 硬盘选型

1. SAS 一般跟 RAID 卡搭配，实现 raid 0/1/10/5 等阵列扩展。
2. SATA 支持热插拔，接口最高 6G/s。
3. PCIE 传输速率更高 8G/s，但是支持多通道，可以线性扩展速率。之前网卡/显卡都在用。上面 3 个接口协议不同，AHCI 转为 SAS 和 SATA 设计，NVMe 协议为 PCIE SSD 设计性能更优。一般核心的 + 高 I/O 的数据库都采用该类型 SSD。
4. 持久内存：傲腾，它提供丰富的底层接口，成本很高，对于需要极致写入性能的，可以考虑。

#### I/O 调度算法

##### noop(no operation)

noop 调度算法是内核中最简单的 IO 调度算法。noop 调度算法将 IO 请求放入到一个 FIFO 队列中，然后逐个执行这些 IO 请求，当然对于一些在磁盘上连续的 IO 请求，noop 调度会适当做一些合并。这个调度算法特别适合那些不希望调度器重新组织 IO 请求顺序的应用，因为内核的 I/O 调度操作会导致性能损失。NVMe SSD 这种高速 I/O 设备可以直接将请求下发给硬件，从而获取更好的性能。

##### CFQ(Completely Fair Queuing)

CFQ 尝试提供由发起 I/O 进程决定的公平的 I/O 调度，该算法为每一个进程分配一个时间窗口，在该时间窗口内，允许进程发出 IO 请求。通过时间窗口在不同进程间的移动，保证了对于所有进程而言都有公平的发出 IO 请求的机会，假如少数进程存在大量密集的 I/O 请求的情况，会出现明显的 I/O 性能下降。

##### deadline

deadline 调度算法主要针对 I/O 请求的延时，每个 I/O 请求都被附加一个最后执行期限。读请求和写请求被分成了两个队列，默认优先处理读 IO，除非写快到 deadline 时才调度。当系统中存在的 I/O 请求进程数量比较少时，与 CFQ 算法相比，deadline 算法可以提供较高的 I/O 吞吐率。

## 常见问题

### 多机并发读写同一个目录，如何避免持续的事务重启现象？

当多客户端在同一个目录下频繁创建/删除子目录时，可能会出现持续的事务重启现象。JuiceFS v1.1 版本开始提供 `--skip-dir-nlink value` 挂载选项，用以指定跳过目录的 nlink 检查之前的重试次数，默认为 20 次。可以适当调小该值，或者设置为 0 禁止重试，从而避免持续的事务重启现象，详情参考[元数据相关的挂载选项](https://juicefs.com/docs/zh/community/command_reference#mount-metadata-options)。
