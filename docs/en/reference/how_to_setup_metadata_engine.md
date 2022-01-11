---
sidebar_label: How to Setup Metadata Engine
sidebar_position: 3
slug: /databases_for_metadata
---
# How to Setup Metadata Engine

By reading [JuiceFS Technical Architecture](../introduction/architecture.md) and [How JuiceFS Store Files](../reference/how_juicefs_store_files.md), you will understand that JuiceFS is designed to store data and metadata independently. Generally , the data is stored in the cloud storage based on object storage, and the metadata corresponding to the data is stored in an independent database.

## Metadata Storage Engine

Metadata and data are equally important. The metadata records the detailed information of each file, such as the name, size, permissions, location, and so on. Especially for this kind of file system where data and metadata are stored separately, the read and write performance of metadata directly determines the actual performance of the file system.

The metadata storage of JuiceFS uses a multi-engine design. In order to create an ultra-high-performance cloud-native file system, JuiceFS first supports [Redis](https://redis.io) a key-value database running in memory, which makes JuiceFS ten times more powerful than Amazon [ EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) performance, [View test results](../benchmark/benchmark.md) .

Through active interaction with community users, we found that many application scenarios do not absolutely rely on high performance. Sometimes users just want to temporarily find a convenient tool to reliably migrate data on the cloud, or simply want to mount the object storage locally for a Small-scale use. Therefore, JuiceFS has successively opened up support for more databases such as MySQL/MariaDB and SQLite (some performance comparison are recorded [here](../benchmark/metadata_engines_benchmark.md)).

**But you need to pay special attention**, in the process of using the JuiceFS file system, no matter which database you choose to store metadata, please **make sure to ensure the security of the metadata**! Once the metadata is damaged or lost, it will directly cause the corresponding data to be completely damaged or lost, and in serious cases may directly cause the entire file system to be damaged.

:::caution
No matter which database is used to store metadata, **it is important to ensure the security of metadata**. If metadata is corrupted or lost, the corresponding data will be completely corrupted or lost, or even the whole file system will be damaged. For production environments, you should always choose a database with high availability, and at the same time, it is recommended to periodically "[backup metadata](../administration/metadata_dump_load.md)" on a regular basis.
:::

## Redis

[Redis](https://redis.io/) is an open source (BSD license) memory-based key-value storage system, often used as a database, cache, and message broker.

### Create a file system

When using Redis as the metadata storage engine, the following format is usually used to access the database:

```shell
redis://username:password@host:6379/1
```

`username` was introduced after Redis 6.0.0. If you don't have a username  field you can ignore it, e.g. `redis://:password@host:6379/1` (the `:` colon in front of the password needs to be preserved).

For example, the following command will create a JuiceFS file system named `pics`, using the database No. `1` in Redis to store metadata:

```shell
$ juicefs format --storage s3 \
    ...
    "redis://:mypassword@192.168.1.6:6379/1" \
    pics
```

For security purposes, it is recommended to pass the password using the environment variable `REDIS_PASSWORD`, e.g.

```shell
export REDIS_PASSWORD=mypassword
```

Then there is no need to set a password in the metadata URL.

```shell
$ juicefs format --storage s3 \
    ...
    "redis://192.168.1.6:6379/1" \
    pics
```

### Mount a file system

```shell
sudo juicefs mount -d redis://192.168.1.6:6379/1 /mnt/jfs
```

:::tip
If you need to share the same file system on multiple servers, you must ensure that each server has access to the database where the metadata is stored.
:::

If you maintain your own Redis database, we recommend reading [Redis Best Practices](../administration/metadata/redis_best_practices.md).

## PostgreSQL

[PostgreSQL](https://www.postgresql.org/) is a powerful open source relational database with a perfect ecosystem and rich application scenarios, and it is well suited as the metadata engine of JuiceFS.

Many cloud computing platforms offer hosted PostgreSQL database services, or you can deploy one yourself by following the [Usage Wizard](https://www.postgresqltutorial.com/postgresql-getting-started/).

Other PostgreSQL-compatible databases (such as CockroachDB) can also be used as metadata engine.

### Create a file system

When using PostgreSQL as the metadata storage engine, the following format is usually used to access the database:

```shell
postgres://[<username>:<password>@]<IP or Domain name>[:5432]/<database-name>[?parameters]
```

For example:

```shell
$ juicefs format --storage s3 \
    ...
    "postgres://user:password@192.168.1.6:5432/juicefs" \
    pics
```

For security purposes, it is recommended to pass the password using an environment variable, e.g.

```shell
export $PG_PASSWD=mypassword
```

Then change the metadata URL to `"postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs"`

### Mount a file system

```shell
sudo juicefs mount -d "postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs" /mnt/jfs
```

### Troubleshooting

The JuiceFS client connects to PostgreSQL via SSL encryption by default; if an error `pq: SSL is not enabled on the server` is returned, it means that SSL is not enabled on the database; you can enable SSL encryption for PostgreSQL according to your business scenario, or you can disable it by adding a parameter to the metadata URL Validation.

```shell
$ juicefs format --storage s3 \
    ...
    "postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs?sslmode=disable" \
    pics
```

Additional parameters can be appended to the metadata URL, [click here to view](https://pkg.go.dev/github.com/lib/pq#hdr-Connection_String_Parameters).

## MySQL

[MySQL](https://www.mysql.com/) is one of the most popular open source relational databases, and is often used as the preferred database for Web applications.

### Create a file system

When using MySQL as the metadata storage engine, the following format is usually used to access the database:

```shell
mysql://<username>:<password>@(<IP or Domain name>:3306)/<database-name>
```

For example:

```shell
$ juicefs format --storage s3 \
    ...
    "mysql://user:password@(192.168.1.6:3306)/juicefs" \
    pics
```

For security purposes, it is recommended to pass the password using an environment variable, e.g.

```shell
export $MYSQL_PASSWD=mypassword
```

Then change the metadata URL to `"mysql://user:$MYSQL_PASSWD@(192.168.1.6:3306)/juicefs"`

### Mount a file system

```shell
sudo juicefs mount -d "mysql://user:$MYSQL_PASSWD@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

For more examples of MySQL database address format, [click here to view](https://github.com/Go-SQL-Driver/MySQL/#examples).

## MariaDB

[MariaDB](https://mariadb.org) is an open source branch of MySQL, maintained by the original developers of MySQL and kept open source.

Because MariaDB is highly compatible with MySQL, there is no difference in usage, the parameters and settings are exactly the same.

For example:

```shell
$ juicefs format --storage s3 \
    ...
    "mysql://user:$MYSQL_PASSWD@(192.168.1.6:3306)/juicefs" \
    pics
```

## SQLite

[SQLite](https://sqlite.org) is a widely used small, fast, single-file, reliable, and full-featured SQL database engine.

The SQLite database has only one file, which is very flexible to create and use. When using it as the JuiceFS metadata storage engine, there is no need to create a database file in advance, and you can directly create a file system:

```shell
$ juicefs format --storage s3 \
    ...
    "sqlite3://my-jfs.db" \
    pics
```

Executing the above command will automatically create a database file named `my-jfs.db` in the current directory, **please take good care of this file**！

Mount the file system:

```shell
sudo juicefs mount -d "sqlite3://my-jfs.db"
```

Please note the location of the database file, if it is not in the current directory, you need to specify the absolute path to the database file, e.g.

```shell
sudo juicefs mount -d "sqlite3:///home/herald/my-jfs.db" /mnt/jfs/
```

:::note
Since SQLite is a single-file database, usually only the host where the database is located can access it. Therefore, SQLite database is more suitable for stand-alone use. For multiple servers sharing the same file system, it is recommended to use databases such as Redis or MySQL.
:::

## TiKV

[TiKV](https://github.com/tikv/tikv) is a distributed transactional key-value database. It is originally developed by [PingCAP](https://pingcap.com) as the storage layer for their flagship product [TiDB](https://github.com/pingcap/tidb). Now TiKV is an independent open source project, and is also a granduated project of [CNCF](https://www.cncf.io/projects).

With the help of official tool `TiUP`, you can easily build a local playground for testing; refer [here](https://tikv.org/docs/5.1/concepts/tikv-in-5-minutes/) for details. In production, usually at least three hosts are required to store three data replicas; refer to the [official document](https://tikv.org/docs/5.1/deploy/install/install/) for all steps.

### Create a file system

When using TiKV as the metadata storage engine, specify parameters as the following format:

```shell
tikv://<pd_addr>[,<pd_addr>...]/<prefix>
```

The `prefix` is a user-defined string, which can be used to distinguish multiple file systems or applications when they share the same TiKV cluster. For example:

```shell
$ juicefs format --storage s3 \
    ...
    "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" \
    pics
```

### Mount a file system

```shell
sudo juicefs mount -d "tikv://192.168.1.6:6379,192.168.1.7:6379,192.168.1.8:6379/jfs" /mnt/jfs
```

## FoundationDB

Coming soon...
