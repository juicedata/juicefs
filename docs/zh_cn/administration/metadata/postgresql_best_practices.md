---
sidebar_label: PostgreSQL
sidebar_position: 3
slug: /postgresql_best_practices
---
# PostgreSQL 最佳实践

对于数据与元数据分离存储的分布式文件系统，元数据的读写性能直接影响整个系统的工作效率，元数据的安全也直接关系着整个系统的数据安全。

在生产环境中，建议您优先选择云计算平台提供的托管型云数据库，并搭配恰当的高可用性架构。

不论自行搭建，还是采用云数据库，使用 JuiceFS 应该始终关注元数据的完整和安全。

## 通信安全

默认情况下，JuiceFS 客户端会采用 SSL 加密协议连接 PostgreSQL，如果数据库未启用 SSL 加密，则需要在元数据 URL 中需要附加 `sslmode=disable` 参数。

建议配置并始终开启数据库服务端 SSL 加密。

## 通过环境变量传递数据库信息

虽然直接在元数据 URL 中设置数据库密码简单方便，但日志或程序输出中可能会泄漏密码，为了保证数据安全，应该始终通过环境变量传递数据库密码。

环境变量名称可以自由定义，例如：

```shell
export $PG_PASSWD=mypassword
```

在元数据 URL 中通过环境变量传递数据库密码：

```shell
juicefs mount -d "postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs" /mnt/jfs
```

## 连接数控制

PostgreSQL 后端采用多进程模式，每一个连接对应后端一个进程，控制数据库的连接总数和减少数据库连接的动态创建都是非常必要的。JuiceFS 提供 4 个数据库连接相关的控制选项：

- max_open_conns：控制当前挂载点到数据库的最大连接数，默认值为 0，表示没有限制。如果设置了一个固定值，并且所有连接都被使用了，新的请求就需要等待其他请求释放数据库连接，过小的值可能会影响性能，请根据实际业务压力情况动态调整。
- max_idle_conns：控制当前挂载点到数据库的最大空闲连接数，默认值为 CPU 的逻辑核心数的两倍。如果设置的值过大，这些连接一直空闲着，可能会消耗或浪费后端的资源，引起后端连接数过高，导致其他挂载点需要新建连接时无法连接成功。
- max_idle_time：一个连接的最长空闲时间，默认值为 300 秒。如果一个连接一直未被使用，和后端数据库无任何交互，超过指定时间后，会自动断开连接，以节约后端资源。设置过小的值可能会引起频繁地创建数据据连接，影响性能。
- max_life_time：一个连接的最大生命周期，默认为 0，表示无限制。一个数据库连接会被各种请求循环复用，在服务请求的过程中会申请一些临时资源，比如内存等，可能存在清理不干净或资源碎片的情况，可以考虑设置一个合理的生命周期，达到周期并且服务完当前请求后会自动断开来优化资源使用。

可在元数据 URL 中直接传递上述控制选项：

```shell
juicefs mount -d "postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs?max_open_conns=30&max_life_time=3600" /mnt/jfs
```

请参考 Go 模块文档 [Database/SQL](https://pkg.go.dev/database/sql#SetConnMaxIdleTime) 了解更多信息。

## 定期备份

请参考官方手册 [Chapter 26. Backup and Restore](https://www.postgresql.org/docs/current/backup.html) 了解如何备份和恢复数据库。

建议制定数据库备份计划，并遵照计划定期备份 PostgreSQL 数据库，与此同时，还应该在实验环境中尝试恢复数据，确认备份是有效的。

## 使用连接池

连接池是客户端与数据库之间的中间层，由它作为中介提升连接效率，降低短连接的损耗。常用的连接池有 [PgBouncer](https://www.pgbouncer.org) 和 [Pgpool-II](https://www.pgpool.net) 。

## 高可用

PostgreSQL 官方文档 [High Availability, Load Balancing, and Replication](https://www.postgresql.org/docs/current/different-replication-solutions.html) 对比了几种常用的数据库高可用方案，请根据实际业务需要选择恰当的高可用方案。

:::note 注意
JuiceFS 使用[事务](https://www.postgresql.org/docs/current/tutorial-transactions.html)保证元数据操作的原子性。由于 PostgreSQL 尚不支持 Multi-Shard (Distributed) 分布式事务，因此请勿将多服务器分布式架构用于 JuiceFS 元数据存储。
:::
