---
sidebar_label: Redis
sidebar_position: 1
slug: /redis_best_practices
---

# Redis Best Practices

To ensure metadata service performance, we recommend use Redis service managed by public cloud provider, see [Recommended Managed Redis Service](#recommended-managed-redis-service).

## Memory usage

The space used by the JuiceFS metadata engine is mainly related to the number of files in the file system. According to our experience, the metadata of each file occupies approximately 300 bytes of memory. Therefore, if you want to store 100 million files, approximately 30 GiB of memory is required.

You can check the specific memory usage through Redis' [`INFO memory`](https://redis.io/commands/info) command, for example:

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

Among them, `used_memory_rss` is the total memory size actually used by Redis, which includes not only the size of data stored in Redis (that is, `used_memory_dataset` above) but also some Redis [system overhead](https://redis.io/commands/memory-stats) (that is, `used_memory_overhead` above). As mentioned earlier that the metadata of each file occupies about 300 bytes, this is actually calculated by `used_memory_dataset`. If you find that the metadata of a single file in your JuiceFS file system occupies much more than 300 bytes, you can try to run [`juicefs gc`](../../reference/command_reference.mdx#gc) command to clean up possible redundant data.

## High availability

### Sentinel mode {#sentinel-mode}

[Redis Sentinel](https://redis.io/docs/manual/sentinel) is the official solution to high availability for Redis. It provides following capabilities:

- **Monitoring**. Sentinel constantly checks if your master and replica instances are working as expected.
- **Notification**. Sentinel can notify the system administrator, or other computer programs, via an API, that something is wrong with one of the monitored Redis instances.
- **Automatic failover**. If a master is not working as expected, Sentinel can start a failover process where a replica is promoted to master, the other additional replicas are reconfigured to use the new master, and the applications using the Redis server are informed about the new address to use when connecting.
- **Configuration provider**. Sentinel acts as a source of authority for clients service discovery: clients connect to Sentinels in order to ask for the address of the current Redis master responsible for a given service. If a failover occurs, Sentinels will report the new address.

**A stable release of Redis Sentinel is shipped since Redis 2.8**. Redis Sentinel version 1, shipped with Redis 2.6, is deprecated and should not be used.

Before start using Redis sentinel, learn the [fundamentals](https://redis.io/docs/manual/sentinel#fundamental-things-to-know-about-sentinel-before-deploying):

1. You need at least three Sentinel instances for a robust deployment.
2. The three Sentinel instances should be placed into computers or virtual machines that are believed to fail in an independent way. So for example different physical servers or Virtual Machines executed on different availability zones.
3. **Sentinel + Redis distributed system does not guarantee that acknowledged writes are retained during failures, since Redis uses asynchronous replication.** However there are ways to deploy Sentinel that make the window to lose writes limited to certain moments, while there are other less secure ways to deploy it.
4. There is no HA setup which is safe if you don't test from time to time in development environments, or even better if you can, in production environments, if they work. You may have a misconfiguration that will become apparent only when it's too late (at 3am when your master stops working).
5. **Sentinel, Docker, or other forms of Network Address Translation or Port Mapping should be mixed with care**: Docker performs port remapping, breaking Sentinel auto discovery of other Sentinel processes and the list of replicas for a master.

Read the [official documentation](https://redis.io/docs/manual/sentinel) for more information.

Once Redis servers and Sentinels are deployed, `META-URL` can be specified as `redis[s]://[[USER]:PASSWORD@]MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`, for example:

```shell
./juicefs mount redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2 ~/jfs
```

:::tip
For JuiceFS v0.16+, the `PASSWORD` in the URL will be used to connect Redis server, and the password for Sentinel should be provided using the environment variable `SENTINEL_PASSWORD`. For early versions of JuiceFS, the `PASSWORD` is used for both Redis server and Sentinel, which can be overwritten by the environment variables `SENTINEL_PASSWORD` and `REDIS_PASSWORD`.
:::

Since JuiceFS v1.0.0, it is supported to use Redis replica when mounting file systems, to reduce the load on Redis master. In order to achieve this, you must mount the JuiceFS file system in read-only mode (that is, set the `--read-only` mount option), and connect to the metadata engine through Redis Sentinel. Finally, you need to add `?route-read=replica` to the end of the metadata URL. For example: `redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2?route-read=replica`.

It should be noted that since the data of the Redis master node is asynchronously replicated to the replica nodes, the read metadata may not be the latest.

### Cluster mode {#cluster-mode}

:::note
This feature requires JuiceFS v1.0.0 or higher
:::

JuiceFS also supports Redis Cluster as a metadata engine, the `META-URL` format is `redis[s]://[[USER]:PASSWORD@]ADDR:PORT,[ADDR:PORT],[ADDR:PORT][/DB]`. For example:

```shell
juicefs format redis://127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002/1 myjfs
```

:::tip
Redis Cluster does not support multiple databases. However, it splits the key space into 16384 hash slots, and distributes the slots to several nodes. Based on Redis Cluster's [Hash Tag](https://redis.io/docs/reference/cluster-spec/#hash-tags) feature, JuiceFS adds `{DB}` before all file system keys to ensure they will be hashed to the same hash slot, assuring that transactions can still work. Besides, one Redis Cluster can serve for multiple JuiceFS file systems as long as they use different db numbers.
:::

## Data durability

Redis provides various options for [persistence](https://redis.io/docs/manual/persistence) in different ranges:

- **RDB**: The RDB persistence performs point-in-time snapshots of your dataset at specified intervals.
- **AOF**: The AOF persistence logs every write operation received by the server, which will be played again at server startup, meaning that the original dataset will be reconstructed each time server is restarted. Commands are logged using the same format as the Redis protocol in an append-only fashion. Redis is able to rewrite logs in the background when it gets too big.
- **RDB+AOF** <Badge type="success">Recommended</Badge>: It is possible to combine AOF and RDB in the same instance. Notice that, in this case, when Redis restarts the AOF file will be used to reconstruct the original dataset since it is guaranteed to be the most complete.

When using AOF, you can have different fsync policies:

1. No fsync
2. fsync every second <Badge type="primary">Default</Badge>
3. fsync at every query

With the default policy of fsync every second write performance is good enough  (fsync is performed using a background thread and the main thread will try hard to perform writes when no fsync is in progress.), **but you may lose the writes from the last second**.

In addition, be aware that, even if the RBD+AOF mode is adopted, the disk may be damaged and the virtual machine may disappear. Thus, **Redis data needs to be backed up regularly**.

Redis is very data backup friendly since you can copy RDB files while the database is running. The RDB is never modified once produced: while RDB is produced, a temporary name is assigned to it and will be renamed into its final destination atomically using `rename` only when the new snapshot is complete. You can also copy the AOF file to create backups.

Please read the [official documentation](https://redis.io/docs/manual/persistence) for more information.

## Backing up Redis data

**Make Sure to Back up Your Database.** as Disks break, instances in the cloud disappear, and so forth.

By default Redis saves snapshots of the dataset on disk as a binary file called `dump.rdb`. You can configure Redis to save the dataset every N seconds if there are at least M changes in the dataset, or  manually call the [`SAVE`](https://redis.io/commands/save) or [`BGSAVE`](https://redis.io/commands/bgsave) commands as needed.

As we mentioned above, Redis is very data backup friendly. This means that copying the RDB file is completely safe while the server is running. The following are our suggestions:

- Create a cron job in your server, and create hourly snapshots of the RDB file in one directory, and daily snapshots in a different directory.
- Every time running the cron script, call the `find` command to check if old snapshots have been deleted: for instance you can take hourly snapshots for the latest 48 hours, and daily snapshots for one or two months. Make sure to name the snapshots with data and time information.
- Make sure to transfer an RDB snapshot _outside your data center_ or at least _outside the physical machine_ running your Redis instance at least one time every day.

Please read the [official documentation](https://redis.io/docs/manual/persistence) for more information.

## Restore Redis data

After generating the AOF or RDB backup file, you can restore the data by copying the backup file to the path corresponding to the `dir` configuration of the new Redis instance. The instance configuration information can be obtained by the [`CONFIG GET dir`](https://redis.io/commands/config-get) command.

If both AOF and RDB persistence are enabled, Redis will use the AOF file first on starting to recover the data because AOF is guaranteed to be the most complete data.

After recovering Redis data, you can continue to use the JuiceFS file system via the new Redis address. It is recommended to run [`juicefs fsck`](../../reference/command_reference.mdx#fsck) command to check the integrity of the file system data.

## Recommended Managed Redis Service

### Amazon MemoryDB for Redis

[Amazon MemoryDB for Redis](https://aws.amazon.com/memorydb) is a durable, in-memory database service that delivers ultra-fast performance. MemoryDB is compatible with Redis, with MemoryDB, all of your data is stored in memory, which enables you to achieve microsecond read and single-digit millisecond write latency and high throughput. MemoryDB also stores data durably across multiple Availability Zones (AZs) using a Multi-AZ transactional log to enable fast failover, database recovery, and node restarts.

### Google Cloud Memorystore for Redis

[Google Cloud Memorystore for Redis](https://cloud.google.com/memorystore/docs/redis) is a fully managed Redis service for the Google Cloud. Applications running on Google Cloud can achieve extreme performance by leveraging the highly scalable, available, secure Redis service without the burden of managing complex Redis deployments.

### Azure Cache for Redis

[Azure Cache for Redis](https://azure.microsoft.com/en-us/services/cache) is a fully managed, in-memory cache that enables high-performance and scalable architectures. It is used to create cloud or hybrid deployments that handle millions of requests per second at sub-millisecond latency, with the advantages of configuration, security, and availability of a managed service.

### Alibaba Cloud ApsaraDB for Redis

[Alibaba Cloud ApsaraDB for Redis](https://www.alibabacloud.com/product/apsaradb-for-redis) is a database service compatible with native Redis protocols. It supports hybrid of memory and hard disks for data persistence. ApsaraDB for Redis provides a highly available hot standby architecture and are scalable to meet requirements for high-performance and low-latency read/write operations.

### Tencent Cloud TencentDB for Redis

[Tencent Cloud TencentDB for Redis](https://intl.cloud.tencent.com/product/crs) is a caching and storage service compatible with the Redis protocol. It features a rich variety of data structure options to help you develop different types of business scenarios, and offers a complete set of database services such as primary-secondary hot backup, automatic switchover for disaster recovery, data backup, failover, instance monitoring, online scaling and data rollback.

## Use Redis compatible product as metadata engine

If you want to use a Redis compatible product as the metadata engine, you need to confirm whether the following Redis data types and commands required by JuiceFS are fully supported.

### Redis data types used by JuiceFS

+ [String](https://redis.io/docs/data-types/strings)
+ [Set](https://redis.io/docs/data-types/sets)
+ [Sorted Set](https://redis.io/docs/data-types/sorted-sets)
+ [Hash](https://redis.io/docs/data-types/hashes)
+ [List](https://redis.io/docs/data-types/lists)

### Redis features used by JuiceFS

+ [Pipelining](https://redis.io/docs/manual/pipelining)

### Redis commands used by JuiceFS

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
+ [HINCRBY](https://redis.io/commands/hincrby)
+ [HKEYS](https://redis.io/commands/hkeys)
+ [HSCAN](https://redis.io/commands/hscan)
+ [HSETNX](https://redis.io/commands/hsetnx)
+ [HSET](https://redis.io/commands/hset) (need to support setting multiple fields and values)

#### List

+ [LLEN](https://redis.io/commands/llen)
+ [LPUSH](https://redis.io/commands/lpush)
+ [LRANGE](https://redis.io/commands/lrange)
+ [LTRIM](https://redis.io/commands/ltrim)
+ [RPUSHX](https://redis.io/commands/rpushx)
+ [RPUSH](https://redis.io/commands/rpush)
+ [SCAN](https://redis.io/commands/scan)

#### Transaction

+ [EXEC](https://redis.io/commands/exec)
+ [MULTI](https://redis.io/commands/multi)
+ [WATCH](https://redis.io/commands/watch)
+ [UNWATCH](https://redis.io/commands/unwatch)

#### Connection management

+ [PING](https://redis.io/commands/ping)

#### Server management

+ [CONFIG GET](https://redis.io/commands/config-get)
+ [CONFIG SET](https://redis.io/commands/config-set)
+ [DBSIZE](https://redis.io/commands/dbsize)
+ [FLUSHDB](https://redis.io/commands/flushdb) (optional)
+ [INFO](https://redis.io/commands/info)

#### Cluster management

+ [CLUSTER INFO](https://redis.io/commands/cluster-info)

#### Scripting (optional)

+ [EVALSHA](https://redis.io/commands/evalsha)
+ [SCRIPT LOAD](https://redis.io/commands/script-load)
