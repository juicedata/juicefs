# Redis Best Practices

This is a guide about Redis best practices. Redis is a critical component in JuiceFS architecture. It store all the file system metadata and serve metadata operation from client. If Redis has any problem (either service unavailable or lose data), it will affect the user experience.

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
5. **Sentinel, Docker, or other forms of Network Address Translation or Port Mapping should be mixed with care**: Docker performs port remapping, breaking Sentinel auto discovery of other Sentinel processes and the list of replicas for a master. Check the section about Sentinel and Docker later in this document for more information.

Please read the [official documentation](https://redis.io/topics/sentinel) for more information.

## Data Durability

Redis provides a different range of [persistence](https://redis.io/topics/persistence) options:

- The RDB persistence performs point-in-time snapshots of your dataset at specified intervals.
- The AOF persistence logs every write operation received by the server, that will be played again at server startup, reconstructing the original dataset. Commands are logged using the same format as the Redis protocol itself, in an append-only fashion. Redis is able to rewrite the log in the background when it gets too big.
- It is possible to combine both AOF and RDB in the same instance. Notice that, in this case, when Redis restarts the AOF file will be used to reconstruct the original dataset since it is guaranteed to be the most complete.

**It's recommended enable RDB and AOF simultaneously.** Beware that when use AOF you can have different fsync policies: no fsync at all, fsync every second, fsync at every query. With the default policy of fsync every second write performances are still great (fsync is performed using a background thread and the main thread will try hard to perform writes when no fsync is in progress.) **but you can only lose one second worth of writes**.

**Remember backup is also required** (disk may break, VM may disappear). Redis is very data backup friendly since you can copy RDB files while the database is running: the RDB is never modified once produced, and while it gets produced it uses a temporary name and is renamed into its final destination atomically using `rename` only when the new snapshot is complete. You can also copy the AOF file in order to create backups.

Please read the [official documentation](https://redis.io/topics/persistence) for more information.
