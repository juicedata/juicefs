---
sidebar_label: Redis
sidebar_position: 1
slug: /redis_best_practices
---

# Redis 最佳实践

为保证元数据服务稳定，我们建议使用云平台提供的 Redis 托管服务，详情查看[「推荐的 Redis 托管服务」](#推荐的-redis-托管服务)。

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

其中 `used_memory_rss` 是 Redis 实际使用的总内存大小，这里既包含了存储在 Redis 中的数据大小（也就是上面的 `used_memory_dataset`），也包含了一些 Redis 的[系统开销](https://redis.io/commands/memory-stats)（也就是上面的 `used_memory_overhead`）。前面提到每个文件的元数据大约占用 300 字节是通过 `used_memory_dataset` 来计算的，如果你发现你的 JuiceFS 文件系统中单个文件元数据占用空间远大于 300 字节，可以尝试运行 [`juicefs gc`](../../reference/command_reference.mdx#gc) 命令来清理可能存在的冗余数据。

## 数据可用性

### 哨兵模式 {#sentinel-mode}

[Redis 哨兵](https://redis.io/docs/manual/sentinel) 是 Redis 官方的高可用解决方案，它提供以下功能：

- **监控**，哨兵会不断检查您的 master 实例和 replica 实例是否按预期工作。
- **通知**，当受监控的 Redis 实例出现问题时，哨兵可以通过 API 通知系统管理员或其他计算机程序。
- **自动故障转移**，如果 master 没有按预期工作，哨兵可以启动一个故障转移过程，其中一个 replica 被提升为 master，其他的副本被重新配置为使用新的 master，应用程序在连接 Redis 服务器时会被告知新的地址。
- **配置提供程序**，哨兵会充当客户端服务发现的权威来源：客户端连接到哨兵以获取当前 Redis 主节点的地址。如果发生故障转移，哨兵会报告新地址。

**Redis 2.8 开始提供稳定版本的 Redis 哨兵**。Redis 2.6 提供的第一版 Redis 哨兵已被弃用，不建议使用。

在使用 Redis 哨兵之前，先了解一些[基础知识](https://redis.io/docs/manual/sentinel#fundamental-things-to-know-about-sentinel-before-deploying)：

1. 您至少需要三个哨兵实例才能进行稳健的部署。
2. 这三个哨兵实例应放置在彼此独立的计算机或虚拟机中。例如，分别位于不同的可用区域上的不同物理服务器或虚拟机上。
3. **由于 Redis 使用异步复制，无法保证在发生故障时能够保留已确认的写入。** 然而，有一些部署 哨兵的方法，可以使丢失写入的窗口限于某些时刻，当然还有其他不太安全的部署方法。
4. 如果您不在开发环境中经常进行测试，就无法确保 HA 的设置是安全的。在条件允许的情况，如果能够在生产环境中进行验证则更好。错误的配置往往都是在你难以预期和响应的时间出现（比如，凌晨 3 点你的 master 节点悄然罢工）。
5. **哨兵、Docker 或其他形式的网络地址转换或端口映射应谨慎混用**：Docker 执行端口重映射，会破坏其他哨兵进程的哨兵自动发现以及 master 的 replicas 列表。

更多信息请阅读[官方文档](https://redis.io/docs/manual/sentinel)。

部署了 Redis 服务器和哨兵以后，`META-URL` 可以指定为 `redis[s]://[[USER]:PASSWORD@]MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`，例如：

```shell
juicefs mount redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2 ~/jfs
```

:::tip 提示
对于 JuiceFS v0.16 及以上版本，URL 中提供的密码会用于连接 Redis 服务器，哨兵的密码需要用环境变量 `SENTINEL_PASSWORD` 指定。对于更早的版本，URL 中的密码会同时用于连接 Redis 服务器和哨兵，也可以通过环境变量 `SENTINEL_PASSWORD` 和 `REDIS_PASSWORD` 来覆盖。
:::

自 JuiceFS v1.0.0 版本开始，支持在挂载文件系统时仅连接 Redis 的副本节点，以降低 Redis 主节点的负载。为了开启这个特性，必须以只读模式挂载 JuiceFS 文件系统（即设置 `--read-only` 挂载选项），并通过 Redis 哨兵连接元数据引擎，最后需要在元数据 URL 末尾加上 `?route-read=replica`，例如：`redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2?route-read=replica`。

需要注意由于 Redis 主节点的数据是异步复制到副本节点，因此有可能读到的元数据不是最新的。

### 集群模式 {#cluster-mode}

:::note 注意
此特性需要使用 1.0.0 及以上版本的 JuiceFS
:::

JuiceFS 同样支持集群模式的 Redis 作为元数据引擎，Redis 集群模式的 `META-URL` 为 `redis[s]://[[USER]:PASSWORD@]ADDR:PORT,[ADDR:PORT],[ADDR:PORT][/DB]`，例如：

```shell
juicefs format redis://127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002/1 myjfs
```

:::tip 提示
Redis 集群不再支持多数据库，而是将所有 keys 分散到 16384 个 hash slots 中，再将这些 hash slots 打散到多个 Redis master 节点来存储。JuiceFS 利用了 Redis 集群的 [Hash Tag](https://redis.io/docs/reference/cluster-spec/#hash-tags) 特性，通过将 `{DB}` 作为 key 的前缀来将一个文件系统中的所有 keys 都存放在同一个 hash slot，以保证集群模式下操作的事务性。另外，通过设置不同的 `DB` 可以让一个 Redis 集群同时作为多个 JuiceFS 的元数据库。
:::

## 数据持久性

Redis 提供了不同范围的[持久性](https://redis.io/docs/manual/persistence)选项：

- **RDB**：以指定的时间间隔生成当前数据集的快照。
- **AOF**：记录服务器收到的每一个写操作，在服务器启动时重建原始数据集。命令使用与 Redis 协议本身相同的格式以追加写（append-only）的方式记录。当日志变得太大时，Redis 能够在后台重写日志。
- **RDB+AOF** <Badge type="success">建议</Badge>：组合使用 AOF 和 RDB。在这种情况下，当 Redis 重新启动时，AOF 文件将用于重建原始数据集，因为它保证是最完整的。

当使用 AOF 时，您可以有不同的 fsync 策略：

1. 没有 fsync；
2. 每秒 fsync <Badge type="primary">默认</Badge>；
3. 每次查询 fsync。

默认策略「每秒 fsync」是不错的选择（fsync 是使用后台线程执行的，当没有 fsync 正在进行时，主线程会努力执行写入），**但你可能丢失最近一秒钟的写入**。

Redis 对数据备份非常友好，因为您可以在数据库运行时复制 RDB 文件，RDB 一旦生成就永远不会被修改，当它被生成时，它使用一个临时名称，并且只有在新快照完成时才使用 `rename` 原子地重命名到其最终目的地。您还可以复制 AOF 文件以创建备份。

更多信息请阅读[官方文档](https://redis.io/docs/manual/persistence)。

## 备份 Redis 数据

磁盘可能会损坏，虚拟机可能出意外，即使采用 RBD+AOF 模式，**依然需要定期备份 Redis 数据**。

默认情况下，Redis 将数据集的快照保存在磁盘上，名为 `dump.rdb` 的二进制文件中。你可以根据需要，将 Redis 配置为当数据集至少发生 M 次变化时，每 N 秒保存一次，也可以手动调用 [`SAVE`](https://redis.io/commands/save) 或 [`BGSAVE`](https://redis.io/commands/bgsave) 命令。

Redis 对数据备份非常友好，因为您可以在数据库运行时复制 RDB 文件：RDB 一旦生成就永远不会被修改，当它被生成时，它使用一个临时名称，并且只有在新快照完成时才使用 `rename(2)` 原子地重命名到其最终目的地。

这意味着在服务器运行时复制 RDB 文件是完全安全的。以下是我们的建议：

- 在您的服务器中创建一个 cron 任务，在一个目录中创建 RDB 文件的每小时快照，并在另一个目录中创建每日快照。
- 每次 cron 脚本运行时，请务必调用 `find` 命令以确保删除太旧的快照：例如，您可以保留最近 48 小时的每小时快照，以及一至两个月的每日快照。要确保使用数据和时间信息来命名快照。
- 确保每天至少一次将 RDB 快照从运行 Redis 的实例传输至 _数据中心以外_ 或至少传输至 _物理机以外_。

更多信息请阅读[官方文档](https://redis.io/docs/manual/persistence)。

## 恢复 Redis 数据

当生成 AOF 或者 RDB 备份文件以后，可以将备份文件拷贝到新 Redis 实例的 `dir` 配置对应的路径中来恢复数据，你可以通过 [`CONFIG GET dir`](https://redis.io/commands/config-get) 命令获取当前 Redis 实例的配置信息。

如果 AOF 和 RDB 同时开启，Redis 启动时会优先使用 AOF 文件来恢复数据，因为 AOF 保证是最完整的数据。

在恢复完 Redis 数据以后，可以继续通过新的 Redis 地址使用 JuiceFS 文件系统。建议运行 [`juicefs fsck`](../../reference/command_reference.mdx#fsck) 命令检查文件系统数据的完整性。

## 推荐的 Redis 托管服务

### Amazon MemoryDB for Redis

[Amazon MemoryDB for Redis](https://aws.amazon.com/memorydb) 是一种持久的内存数据库服务，可提供超快的性能。MemoryDB 与 Redis 兼容，使用 MemoryDB，你的所有数据都存储在内存中，这使你能够实现微秒级读取和数毫秒的写入延迟和高吞吐。MemoryDB 还使用多可用区事务日志跨多个可用区持久存储数据，以实现快速故障切换、数据库恢复和节点重启。

### Google Cloud Memorystore for Redis

[Google Cloud Memorystore for Redis](https://cloud.google.com/memorystore/docs/redis) 是针对 Google Cloud 的完全托管的 Redis 服务。通过利用高度可扩展、可用且安全的 Redis 服务，在 Google Cloud 上运行的应用程序可以实现卓越的性能，而无需管理复杂的 Redis 部署。

### Azure Cache for Redis

[Azure Cache for Redis](https://azure.microsoft.com/en-us/services/cache) 是一个完全托管的内存缓存，支持高性能和可扩展的架构。使用它来创建云或混合部署，以亚毫秒级延迟处理每秒数百万个请求——所有这些都具有托管服务的配置、安全性和可用性优势。

### 阿里云云数据库 Redis 版

[阿里云云数据库 Redis 版](https://www.aliyun.com/product/kvstore)是一种兼容原生 Redis 协议的数据库服务。它支持混合内存和硬盘以实现数据持久性。云数据库 Redis 版提供高可用的热备架构，可扩展以满足高性能、低延迟的读写操作需求。

### 腾讯云云数据库 Redis

[腾讯云云数据库 Redis](https://cloud.tencent.com/product/crs) 是一种兼容 Redis 协议的缓存和存储服务。丰富多样的数据结构选项，帮助您开发不同类型的业务场景，提供主从热备份、容灾自动切换、数据备份、故障转移、实例监控、在线等一整套数据库服务缩放和数据回滚。

## 使用 Redis 兼容的产品

如果想要使用 Redis 兼容产品作为元数据引擎，需要确认是否完整支持 JuiceFS 所需的以下 Redis 数据类型和命令。

### JuiceFS 使用到的 Redis 数据类型

+ [String](https://redis.io/docs/data-types/strings)
+ [Set](https://redis.io/docs/data-types/sets)
+ [Sorted Set](https://redis.io/docs/data-types/sorted-sets)
+ [Hash](https://redis.io/docs/data-types/hashes)
+ [List](https://redis.io/docs/data-types/lists)

### JuiceFS 使用到的 Redis 特性

+ [管道](https://redis.io/docs/manual/pipelining)

### JuiceFS 使用到的 Redis 命令

#### String

+ [DECRBY](https://redis.io/commands/decrby)
+ [DEL](https://redis.io/commands/del)
+ [GET](https://redis.io/commands/get)
+ [INCR](https://redis.io/commands/incr)
+ [INCRBY](https://redis.io/commands/incrby)
+ [DECR](https://redis.io/commands/decr)
+ [MGET](https://redis.io/commands/mget)
+ [MSET](https://redis.io/commands/mset)
+ [SETNX](https://redis.io/commands/setnx)
+ [SET](https://redis.io/commands/set)

#### Set

+ [SADD](https://redis.io/commands/sadd)
+ [SMEMBERS](https://redis.io/commands/smembers)
+ [SREM](https://redis.io/commands/srem)

#### Sorted Set

+ [ZADD](https://redis.io/commands/zadd)
+ [ZRANGEBYSCORE](https://redis.io/commands/zrangebyscore)
+ [ZRANGE](https://redis.io/commands/zrange)
+ [ZREM](https://redis.io/commands/zrem)
+ [ZSCORE](https://redis.io/commands/zscore)

#### Hash

+ [HDEL](https://redis.io/commands/hdel)
+ [HEXISTS](https://redis.io/commands/hexists)
+ [HGETALL](https://redis.io/commands/hgetall)
+ [HGET](https://redis.io/commands/hget)
+ [HINCRBY](https://redis.io/commands/hincrby)
+ [HKEYS](https://redis.io/commands/hkeys)
+ [HSCAN](https://redis.io/commands/hscan)
+ [HSETNX](https://redis.io/commands/hsetnx)
+ [HSET](https://redis.io/commands/hset)（需要支持设置多个 field 和 value）

#### List

+ [LLEN](https://redis.io/commands/llen)
+ [LPUSH](https://redis.io/commands/lpush)
+ [LRANGE](https://redis.io/commands/lrange)
+ [LTRIM](https://redis.io/commands/ltrim)
+ [RPUSHX](https://redis.io/commands/rpushx)
+ [RPUSH](https://redis.io/commands/rpush)
+ [SCAN](https://redis.io/commands/scan)

#### 事务

+ [EXEC](https://redis.io/commands/exec)
+ [MULTI](https://redis.io/commands/multi)
+ [WATCH](https://redis.io/commands/watch)
+ [UNWATCH](https://redis.io/commands/unwatch)

#### 连接管理

+ [PING](https://redis.io/commands/ping)

#### 服务管理

+ [CONFIG GET](https://redis.io/commands/config-get)
+ [CONFIG SET](https://redis.io/commands/config-set)
+ [DBSIZE](https://redis.io/commands/dbsize)
+ [FLUSHDB](https://redis.io/commands/flushdb)（可选）
+ [INFO](https://redis.io/commands/info)

#### 集群管理

+ [CLUSTER INFO](https://redis.io/commands/cluster-info)

#### 脚本（可选）

+ [EVALSHA](https://redis.io/commands/evalsha)
+ [SCRIPT LOAD](https://redis.io/commands/script-load)
