# Redis 最佳实践

这是一份关于 Redis 的最佳实践指南。Redis 是 JuiceFS 架构中的关键组件，它负责存储所有元数据并响应客户端对元数据的操作。Redis 出现任何问题（服务不可用或数据丢失），都会对用户体验造成影响。

**强烈建议使用公有云上的托管 Redis 服务。** 更多信息，请参阅[「推荐的 Redis 托管服务」](#推荐的-redis-托管服务)。如果您需要在生产环境中自主维护 Redis，请继续阅读以下内容。

## 内存使用量

JuiceFS 元数据引擎的使用空间主要与文件系统中的文件数量有关，根据我们的经验，每一个文件的元数据会大约占用 300 字节内存。因此，如果要存储 1 亿个文件，大约需要 30GiB 内存。

你可以通过 Redis 的 [`INFO memory`](https://redis.io/commands/info) 命令查看具体的内存使用量，例如：

```
> INFO memory
used_memory: 19167628056
used_memory_human: 17.85G
used_memory_rss: 20684886016
used_memory_rss_human: 19.26G
...
used_memory_overhead: 5727954464
...
used_memory_dataset: 13439673592
used_memory_dataset_perc: 70.12%
```

其中 `used_memory_rss` 是 Redis 实际使用的总内存大小，这里既包含了存储在 Redis 中的数据大小（也就是上面的 `used_memory_dataset`），也包含了一些 Redis 的[系统开销](https://redis.io/commands/memory-stats)（也就是上面的 `used_memory_overhead`）。前面提到每个文件的元数据大约占用 300 字节是通过 `used_memory_dataset` 来计算的，如果你发现你的 JuiceFS 文件系统中单个文件元数据占用空间远大于 300 字节，可以尝试运行 [`juicefs gc`](command_reference.md#juicefs-gc) 命令来清理可能存在的冗余数据。

---

> **注意**：以下内容摘自 Redis 官方文档。它可能已经过时，请以官方文档的最新版本为准。

## 高可用性

[Redis 哨兵](https://redis.io/topics/sentinel) 是 Redis 官方的高可用解决方案。它提供以下功能：

- **监控**，哨兵会不断检查您的 master 实例和 replica 实例是否按预期工作。
- **通知**，当受监控的 Redis 实例出现问题时，哨兵可以通过 API 通知系统管理员或其他计算机程序。
- **自动故障转移**，如果 master 没有按预期工作，哨兵可以启动一个故障转移过程，其中一个 replica 被提升为 master，其他的副本被重新配置为使用新的 master，应用程序在连接 Redis 服务器时会被告知新的地址。
- **配置提供程序**，哨兵会充当客户端服务发现的权威来源：客户端连接到哨兵以获取当前 Redis 主节点的地址。如果发生故障转移，哨兵会报告新地址。

**Redis 2.8 开始提供稳定版本的 Redis 哨兵**。Redis 2.6 提供的第一版 Redis 哨兵已被弃用，不建议使用。

在使用 Redis 哨兵之前，您需要了解一些[基础知识](https://redis.io/topics/sentinel#fundamental-things-to-know-about-sentinel-before-deploying)：

1. 您至少需要三个哨兵实例才能进行稳健的部署。
2. 这三个哨兵实例应放置在彼此独立的计算机或虚拟机中。例如，分别位于不同的可用区域上的不同物理服务器或虚拟机上。
3. **由于 Redis 使用异步复制，无法保证在发生故障时能够保留已确认的写入。** 然而，有一些部署 哨兵的方法，可以使丢失写入的窗口限于某些时刻，当然还有其他不太安全的部署方法。
4. 如果您不在开发环境中经常进行测试，就无法确保 HA 的设置是安全的。在条件允许的情况，如果能够在生产环境中进行验证则更好。错误的配置往往都是在你难以预期和响应的时间出现（比如，凌晨 3 点你的 master 节点悄然罢工）。
5. **哨兵、Docker 或其他形式的网络地址转换或端口映射应谨慎混用**：Docker 执行端口重映射，会破坏其他哨兵进程的哨兵自动发现以及 master 的 replicas 列表。

更多信息请阅读[官方文档](https://redis.io/topics/sentinel)。

部署了 Redis 服务器和哨兵以后，`META-URL` 可以指定为 `redis[s]://[[USER]:PASSWORD@]MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`，例如：

```bash
$ ./juicefs mount redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2 ~/jfs
```

> **注意**：对于 v0.16+ 版本，URL 中提供的密码会用于连接 Redis 服务器，哨兵的密码需要用环境变量 `SENTINEL_PASSWORD` 指定。对于更早的版本，URL 中的密码会同时用于连接 Redis 服务器和哨兵，也可以通过环境变量 `SENTINEL_PASSWORD` 和 `REDIS_PASSWORD` 来覆盖。

## 数据持久性

Redis 提供了不同范围的[持久性](https://redis.io/topics/persistence)选项：

- RDB：以指定的时间间隔生成当前数据集的快照。
- AOF：记录服务器收到的每一个写操作，在服务器启动时会重放，重建原始数据集。命令使用与 Redis 协议本身相同的格式以追加写（append-only）的方式记录。当日志变得太大时，Redis 能够在后台重写日志。
- 可以在同一个实例中组合使用 AOF 和 RDB。请注意，在这种情况下，当 Redis 重新启动时，AOF 文件将用于重建原始数据集，因为它保证是最完整的。

**建议同时启用 RDB 和 AOF。** 请注意，当使用 AOF 时，您可以有不同的 fsync 策略：根本没有 fsync，每秒 fsync，每次查询 fsync。使用默认的 fsync 每秒写入策略性能仍然很棒（fsync 是使用后台线程执行的，当没有 fsync 正在进行时，主线程会努力执行写入），**但你可能丢失最近一秒钟的写入**。

**请记住备份依然是需要的**（磁盘可能会损坏，虚拟机可能会消失），Redis 对数据备份非常友好，因为您可以在数据库运行时复制 RDB 文件：RDB 一旦生成就永远不会被修改，当它被生成时，它使用一个临时名称，并且只有在新快照完成时才使用 `rename` 原子地重命名到其最终目的地。您还可以复制 AOF 文件以创建备份。

更多信息请阅读[官方文档](https://redis.io/topics/persistence)。

## 备份 Redis 数据

磁盘会损坏、云中的实例会消失，**请务必备份数据库！**

默认情况下，Redis 将数据集的快照保存在磁盘上，名为 `dump.rdb` 的二进制文件中。你可以根据需要，将 Redis 配置为当数据集至少发生 M 次变化时，每 N 秒保存一次，也可以手动调用 [`SAVE`](https://redis.io/commands/save) 或 [`BGSAVE`](https://redis.io/commands/bgsave) 命令。

Redis 对数据备份非常友好，因为您可以在数据库运行时复制 RDB 文件：RDB 一旦生成就永远不会被修改，当它被生成时，它使用一个临时名称，并且只有在新快照完成时才使用 `rename(2)` 原子地重命名到其最终目的地。

这意味着在服务器运行时复制 RDB 文件是完全安全的。以下是我们的建议：

- 在您的服务器中创建一个 cron 任务，在一个目录中创建 RDB 文件的每小时快照，并在另一个目录中创建每日快照。
- 每次 cron 脚本运行时，请务必调用 `find` 命令以确保删除太旧的快照：例如，您可以保留最近 48 小时的每小时快照，以及一至两个月的每日快照。要确保使用数据和时间信息来命名快照。
- 确保每天至少一次将 RDB 快照从运行 Redis 的实例传输至 _数据中心以外_ 或至少传输至 _物理机以外_ 。

更多信息请阅读[官方文档](https://redis.io/topics/persistence)。

---

## 推荐的 Redis 托管服务

### Amazon ElastiCache for Redis

[Amazon ElastiCache for Redis](https://aws.amazon.com/elasticache/redis) 是为云构建的完全托管的、与 Redis 兼容的内存数据存储。它提供[自动故障切换](https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/AutoFailover.html)、[自动备份](https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/backups-automatic.html)等功能以确保可用性和持久性。

> **注意**：Amazon ElastiCache for Redis 有两种类型：禁用集群模式和启用集群模式。因为 JuiceFS 使用[事务](https://redis.io/topics/transactions)来保证元数据操作的原子性，所以不能使用「启用集群模式」类型。

### Google Cloud Memorystore for Redis

[Google Cloud Memorystore for Redis](https://cloud.google.com/memorystore/docs/redis) 是针对 Google Cloud 的完全托管的 Redis 服务。通过利用高度可扩展、可用且安全的 Redis 服务，在 Google Cloud 上运行的应用程序可以实现卓越的性能，而无需管理复杂的 Redis 部署。

### Azure Cache for Redis

[Azure Cache for Redis](https://azure.microsoft.com/en-us/services/cache) 是一个完全托管的内存缓存，支持高性能和可扩展的架构。使用它来创建云或混合部署，以亚毫秒级延迟处理每秒数百万个请求——所有这些都具有托管服务的配置、安全性和可用性优势。

### 阿里云 ApsaraDB for Redis

[阿里云 ApsaraDB for Redis](https://www.alibabacloud.com/product/apsaradb-for-redis) 是一种兼容原生 Redis 协议的数据库服务。它支持混合内存和硬盘以实现数据持久性。云数据库 Redis 版提供高可用的热备架构，可扩展以满足高性能、低延迟的读写操作需求。

> **注意**：ApsaraDB for Redis 支持 3 种类型的[架构](https://www.alibabacloud.com/help/doc-detail/86132.htm)：标准、集群和读写分离。因为 JuiceFS 使用[事务](https://redis.io/topics/transactions)来保证元数据操作的原子性，所以不能使用集群类型架构。

### 腾讯云 TencentDB for Redis

[腾讯云 TencentDB for Redis](https://intl.cloud.tencent.com/product/crs) 是一种兼容 Redis 协议的缓存和存储服务。丰富多样的数据结构选项，帮助您开发不同类型的业务场景，提供主从热备份、容灾自动切换、数据备份、故障转移、实例监控、在线等一整套数据库服务缩放和数据回滚。

> **注意**：TencentDB for Redis 支持两种类型的[架构](https://intl.cloud.tencent.com/document/product/239/3205)：标准和集群。因为 JuiceFS 使用[事务](https://redis.io/topics/transactions)来保证元数据操作的原子性，所以不能使用集群类型架构。
