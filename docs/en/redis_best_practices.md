# Redis Best Practices

This is a guide about Redis best practices. Redis is a critical component in JuiceFS architecture. It stores all the file system metadata and serve metadata operation from client. If Redis has any problem (either service unavailable or lose data), it will affect the user experience.

**It's highly recommended use Redis service managed by public cloud provider if possible.** See ["Recommended Managed Redis Service"](#recommended-managed-redis-service) for more information. If you still need operate Redis by yourself in production environment, continue read following contents.

## Memory usage

The space used by the JuiceFS metadata engine is mainly related to the number of files in the file system. According to our experience, the metadata of each file occupies approximately 300 bytes of memory. Therefore, if you want to store 100 million files, approximately 30 GiB of memory is required.

You can check the specific memory usage through Redis's [`INFO memory`](https://redis.io/commands/info) command, for example:

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

Among them, `used_memory_rss` is the total memory size actually used by Redis, which includes not only the size of data stored in Redis (that is, `used_memory_dataset` above), but also some Redis [system overhead](https://redis.io/commands/memory-stats) (that is, `used_memory_overhead` above). As mentioned earlier, the metadata of each file occupies about 300 bytes and is calculated by `used_memory_dataset`. If you find that the metadata of a single file in your JuiceFS file system occupies much more than 300 bytes, you can try to run [`juicefs gc`](command_reference.md#juicefs-gc) command to clean up possible redundant data.

---

> **Note**: The following paragraphs are extracted from Redis official documentation. It may outdated, subject to latest version of the official documentation.

## High Availability

[Redis Sentinel](https://redis.io/topics/sentinel) is the official high availability solution for Redis. It provides following capabilities:

- **Monitoring**. Sentinel constantly checks if your master and replica instances are working as expected.
- **Notification**. Sentinel can notify the system administrator, or other computer programs, via an API, that something is wrong with one of the monitored Redis instances.
- **Automatic failover**. If a master is not working as expected, Sentinel can start a failover process where a replica is promoted to master, the other additional replicas are reconfigured to use the new master, and the applications using the Redis server are informed about the new address to use when connecting.
- **Configuration provider**. Sentinel acts as a source of authority for clients service discovery: clients connect to Sentinels in order to ask for the address of the current Redis master responsible for a given service. If a failover occurs, Sentinels will report the new address.

**A stable release of Redis Sentinel is shipped since Redis 2.8**. Redis Sentinel version 1, shipped with Redis 2.6, is deprecated and should not be used.

There're some [fundamental things](https://redis.io/topics/sentinel#fundamental-things-to-know-about-sentinel-before-deploying) you need to know about Redis Sentinel before using it:

1. You need at least three Sentinel instances for a robust deployment.
2. The three Sentinel instances should be placed into computers or virtual machines that are believed to fail in an independent way. So for example different physical servers or Virtual Machines executed on different availability zones.
3. **Sentinel + Redis distributed system does not guarantee that acknowledged writes are retained during failures, since Redis uses asynchronous replication.** However there are ways to deploy Sentinel that make the window to lose writes limited to certain moments, while there are other less secure ways to deploy it.
4. There is no HA setup which is safe if you don't test from time to time in development environments, or even better if you can, in production environments, if they work. You may have a misconfiguration that will become apparent only when it's too late (at 3am when your master stops working).
5. **Sentinel, Docker, or other forms of Network Address Translation or Port Mapping should be mixed with care**: Docker performs port remapping, breaking Sentinel auto discovery of other Sentinel processes and the list of replicas for a master.

Please read the [official documentation](https://redis.io/topics/sentinel) for more information.

Once Redis servers and Sentinels are deployed, the `META-URL` can be specified as `redis[s]://[[USER]:PASSWORD@]MASTER_NAME,SENTINEL_ADDR[,SENTINEL_ADDR]:SENTINEL_PORT[/DB]`, for example:

```bash
$ ./juicefs mount redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2 ~/jfs
```

> **Note**: For v0.16+, the `PASSWORD` in the URL will be used to connect Redis server, the password for Sentinel
> should be provided using environment variable `SENTINEL_PASSWORD`. For early versions, the `PASSWORD` is used for both
> Redis server and Sentinel, they can be overrode by environment variables `SENTINEL_PASSWORD` and `REDIS_PASSWORD`.

## Data Durability

Redis provides a different range of [persistence](https://redis.io/topics/persistence) options:

- The RDB persistence performs point-in-time snapshots of your dataset at specified intervals.
- The AOF persistence logs every write operation received by the server, that will be played again at server startup, reconstructing the original dataset. Commands are logged using the same format as the Redis protocol itself, in an append-only fashion. Redis is able to rewrite the log in the background when it gets too big.
- It is possible to combine both AOF and RDB in the same instance. Notice that, in this case, when Redis restarts the AOF file will be used to reconstruct the original dataset since it is guaranteed to be the most complete.

**It's recommended enable RDB and AOF simultaneously.** Beware that when use AOF you can have different fsync policies: no fsync at all, fsync every second, fsync at every query. With the default policy of fsync every second write performances are still great (fsync is performed using a background thread and the main thread will try hard to perform writes when no fsync is in progress.) **but you can only lose one second worth of writes**.

**Remember backup is also required** (disk may break, VM may disappear). Redis is very data backup friendly since you can copy RDB files while the database is running: the RDB is never modified once produced, and while it gets produced it uses a temporary name and is renamed into its final destination atomically using `rename` only when the new snapshot is complete. You can also copy the AOF file in order to create backups.

Please read the [official documentation](https://redis.io/topics/persistence) for more information.

## Backing up Redis Data

**Make Sure to Backup Your Database.** Disks break, instances in the cloud disappear, and so forth.

By default Redis saves snapshots of the dataset on disk, in a binary file called `dump.rdb`. You can configure Redis to have it save the dataset every N seconds if there are at least M changes in the dataset, or you can manually call the [`SAVE`](https://redis.io/commands/save) or [`BGSAVE`](https://redis.io/commands/bgsave) commands.

Redis is very data backup friendly since you can copy RDB files while the database is running: the RDB is never modified once produced, and while it gets produced it uses a temporary name and is renamed into its final destination atomically using `rename(2)` only when the new snapshot is complete.

This means that copying the RDB file is completely safe while the server is running. This is what we suggest:

- Create a cron job in your server creating hourly snapshots of the RDB file in one directory, and daily snapshots in a different directory.
- Every time the cron script runs, make sure to call the `find` command to make sure too old snapshots are deleted: for instance you can take hourly snapshots for the latest 48 hours, and daily snapshots for one or two months. Make sure to name the snapshots with data and time information.
- At least one time every day make sure to transfer an RDB snapshot _outside your data center_ or at least _outside the physical machine_ running your Redis instance.

Please read the [official documentation](https://redis.io/topics/persistence) for more information.

---

## Recommended Managed Redis Service

### Amazon ElastiCache for Redis

[Amazon ElastiCache for Redis](https://aws.amazon.com/elasticache/redis) is a fully managed, Redis-compatible in-memory data store built for the cloud. It provides [automatic failover](https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/AutoFailover.html), [automatic backup](https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/backups-automatic.html) features to ensure availability and durability.

> **Note**: Amazon ElastiCache for Redis has two type: cluster mode disabled and cluster mode enabled. Because JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, so you couldn't use "cluster mode enabled" type.

### Google Cloud Memorystore for Redis

[Google Cloud Memorystore for Redis](https://cloud.google.com/memorystore/docs/redis) is a fully managed Redis service for the Google Cloud. Applications running on Google Cloud can achieve extreme performance by leveraging the highly scalable, available, secure Redis service without the burden of managing complex Redis deployments.

### Azure Cache for Redis

[Azure Cache for Redis](https://azure.microsoft.com/en-us/services/cache) is a fully managed, in-memory cache that enables high-performance and scalable architectures. Use it to create cloud or hybrid deployments that handle millions of requests per second at sub-millisecond latency—all with the configuration, security, and availability benefits of a managed service.

### Alibaba Cloud ApsaraDB for Redis

[Alibaba Cloud ApsaraDB for Redis](https://www.alibabacloud.com/product/apsaradb-for-redis) is a database service that is compatible with native Redis protocols. It supports a hybrid of memory and hard disks for data persistence. ApsaraDB for Redis provides a highly available hot standby architecture and can scale to meet requirements for high-performance and low-latency read/write operations.

> **Note**: ApsaraDB for Redis supports 3 type [architectures](https://www.alibabacloud.com/help/doc-detail/86132.htm): standard, cluster and read/write splitting. Because JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, so you couldn't use cluster type architecture.

### Tencent Cloud TencentDB for Redis

[Tencent Cloud TencentDB for Redis](https://intl.cloud.tencent.com/product/crs) is a caching and storage service compatible with the Redis protocol. It features a rich variety of data structure options to help you develop different types of business scenarios, and offers a complete set of database services such as primary-secondary hot backup, automatic switchover for disaster recovery, data backup, failover, instance monitoring, online scaling and data rollback.

> **Note**: TencentDB for Redis supports 2 type [architectures](https://intl.cloud.tencent.com/document/product/239/3205): standard and cluster. Because JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, so you couldn't use cluster type architecture.
