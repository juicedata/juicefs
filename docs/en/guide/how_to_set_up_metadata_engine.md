---
title: How to Set Up Metadata Engine
sidebar_position: 1
slug: /databases_for_metadata
description: JuiceFS supports Redis, TiKV, PostgreSQL, MySQL and other databases as metadata engines, and this article describes how to set up and use them.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

:::tip Version Tips
The environment variable `META_PASSWORD` used in this document is a new feature in JuiceFS v1.0, and not applied to old clients. Please [upgrade the clients](../administration/upgrade.md) before using it if you are using the old ones.
:::

As mentioned in [JuiceFS Technical Architecture](../introduction/architecture.md) and [How JuiceFS Store Files](../introduction/architecture.md#how-juicefs-store-files), JuiceFS is designed to store data and metadata separately. Generally, data is stored in the cloud storage based on object storage, and metadata corresponding to the data is stored in an independent database. The database that supports storing metadata is referred to "Metadata Storage Engine".

## Metadata Storage Engine

Metadata is crucially important to a file system as it contains all the detailed information of each file, such as name, size, permissions and location. Especially, for the file system where data and metadata are stored separately, the read and write performance of metadata directly determines the file system performance, and the engine that stores metadata is the most fundamental determinant of performance and reliability.

The metadata storage of JuiceFS uses a multi-engine design. In order to create an ultra-high-performance cloud-native file system, JuiceFS first supports [Redis](https://redis.io), an in-memory Key-Value database, which makes JuiceFS ten times more powerful than Amazon [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) performance. Test results can be seen [here](../benchmark/benchmark.md).

However, based on the feedback from community users, we have noticed that a high-performance file system is not urgently required in many application scenarios. Sometimes users just want to find a convenient tool to migrate data on the cloud with high reliability, or to mount the object storage locally to use on a small scale. Therefore, JuiceFS has successively opened up support for more databases such as MySQL/MariaDB and SQLite. The comparison of performance can be found [here](../benchmark/metadata_engines_benchmark.md)).

:::caution
While using the JuiceFS file system - no matter which database you choose to store metadata, please **ensure the safety of the metadata**! Once the metadata is damaged or lost, the corresponding data will accordingly be damaged or lost, and the entire file system can even be damaged in the worse cases. For production environments, you should always choose a database with high availability, and at the same time, it is recommended to ["backup metadata"](../administration/metadata_dump_load.md) periodically.
:::

## Redis

[Redis](https://redis.io) is an open source (BSD license) memory-based Key-Value storage system, often used as a database, cache, and message broker.

:::note
JuiceFS requires Redis version 4.0 and above, and using a lower version of Redis will result in an error.

To ensure metadata security, JuiceFS requires Redis' `maxmemory_policy` to be set to `noeviction`, otherwise it will try to set it to `noeviction` when starting JuiceFS, and will print a warning log if it fails to do so.
:::

### Create a file system

When using Redis as the metadata storage engine, the following format is usually used to access the database:

<Tabs>
  <TabItem value="tcp" label="TCP">

```
redis[s]://[<username>:<password>@]<host>[:<port>]/<db>
```

  </TabItem>
  <TabItem value="unix-socket" label="Unix socket">

```
unix://[<username>:<password>@]<socket-file-path>?db=<db>
```

  </TabItem>
</Tabs>

Where `[]` enclosed are optional and the rest are mandatory.

- If the [TLS](https://redis.io/docs/manual/security/encryption) feature of Redis is enabled, the protocol header needs to use `rediss://`, otherwise use `redis://`.
- `<username>` is introduced after Redis 6.0 and can be ignored if there is no username, but the `:` colon in front of the password needs to be kept, e.g. `redis://:<password>@<host>:6379/1`.
- The default port number on which Redis listens is `6379`, which can be left blank if the default port number is not changed, e.g. `redis://:<password>@<host>/1`.
- Redis supports multiple [logical databases](https://redis.io/commands/select), please replace `<db>` with the actual database number used.
- If you need to connect to Redis Sentinel, the format of the metadata URL will be slightly different, please refer to the ["Redis Best Practices"](../administration/metadata/redis_best_practices.md#high-availability) document for details.

For example, the following command will create a JuiceFS file system named `pics`, using the database No. `1` in Redis to store metadata:

```shell
juicefs format \
    --storage s3 \
    ... \
    "redis://:mypassword@192.168.1.6:6379/1" \
    pics
```

For security purposes, it is recommended to pass the password using the environment variable `META_PASSWORD` or `REDIS_PASSWORD`, e.g.

```shell
export META_PASSWORD=mypassword
```

Then there is no need to set a password in the metadata URL.

```shell
juicefs format \
    --storage s3 \
    ... \
    "redis://192.168.1.6:6379/1" \
    pics
```

:::note

1. When a Redis username or password contains special characters, you need to replace them with `%xx` by [URL encode](https://www.w3schools.com/tags/ref_urlencode.ASP), for example `@` with `%40`, or pass the password using an environment variable.
2. You can also use the standard URL syntax when passing database passwords using environment variables, e.g., `"redis://:@192.168.1.6:6379/1"` which preserves the `:` and `@` separators between the username and password.
:::

### Mount a file system

```shell
juicefs mount -d "redis://:mypassword@192.168.1.6:6379/1" /mnt/jfs
```

Passing passwords with the `META_PASSWORD` or `REDIS_PASSWORD` environment variables is also supported when mounting file systems.

```shell
export META_PASSWORD=mypassword
juicefs mount -d "redis://192.168.1.6:6379/1" /mnt/jfs
```

:::tip
If you need to share the same file system on multiple servers, you must ensure that each server has access to the database where the metadata is stored.
:::

If you maintain the Redis database on your own, it is recommended to read [Redis Best Practices](../administration/metadata/redis_best_practices.md).

## KeyDB

[KeyDB](https://keydb.dev) is an open source fork of Redis, developed to stay aligned with the Redis community. KeyDB implements multi-threading support, better memory utilization, and greater throughput on top of Redis, and also supports [Active Replication](https://github.com/JohnSully/KeyDB/wiki/Active-Replication), i.e., the Active Active feature.

:::note
Same as Redis, the Active Replication is asychronous, which may cause consistency issues. So use with caution!
:::

When being used as metadata storage engine for Juice, KeyDB is used exactly in the same way as Redis. So please refer to the [Redis](#redis) section for usage.

## PostgreSQL

[PostgreSQL](https://www.postgresql.org) is a powerful open source relational database with a perfect ecosystem and rich application scenarios, and it also works as the metadata engine of JuiceFS.

Many cloud computing platforms offer hosted PostgreSQL database services, or you can deploy one yourself by following the [Usage Wizard](https://www.postgresqltutorial.com/postgresql-getting-started).

Other PostgreSQL-compatible databases (such as CockroachDB) can also be used as metadata engine.

### Create a file system

When using PostgreSQL as the metadata storage engine, you need to create a database manually before creating the file system by following the format below:

<Tabs>
  <TabItem value="tcp" label="TCP">

```
postgres://[username][:<password>]@<host>[:5432]/<database-name>[?parameters]
```

  </TabItem>
  <TabItem value="unix-socket" label="Unix socket">

```
postgres://[username][:<password>]@/<database-name>?host=<socket-directories-path>[&parameters]
```

  </TabItem>
</Tabs>

Where `[]` enclosed are optional and the rest are mandatory.

For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "postgres://user:mypassword@192.168.1.6:5432/juicefs" \
    pics
```

A more secure approach would be to pass the database password through the environment variable `META_PASSWORD`:

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "postgres://user@192.168.1.6:5432/juicefs" \
    pics
```

:::note

1. JuiceFS uses public [schema](https://www.postgresql.org/docs/current/ddl-schemas.html) by default, if you want to use a `non-public schema`,  you need to specify `search_path` in the connection string parameter. e.g `postgres://user:mypassword@192.168.1.6:5432/juicefs?search_path=pguser1`
2. If the `public schema` is not the first hit in the `search_path` configured on the PostgreSQL server, the `search_path` parameter must be explicitly set in the connection string.
3. The `search_path` connection parameter can be set to multiple schemas natively, but currently JuiceFS only supports setting one. `postgres://user:mypassword@192.168.1.6:5432/juicefs?search_path=pguser1,public` will be considered illegal.
:::

### Mount a file system

```shell
juicefs mount -d "postgres://user:mypassword@192.168.1.6:5432/juicefs" /mnt/jfs
```

Passing password with the `META_PASSWORD` environment variable is also supported when mounting a file system.

```shell
export META_PASSWORD="mypassword"
juicefs mount -d "postgres://user@192.168.1.6:5432/juicefs" /mnt/jfs
```

### Troubleshooting

The JuiceFS client connects to PostgreSQL via SSL encryption by default. If you encountered an error saying `pq: SSL is not enabled on the server`, you need to enable SSL encryption for PostgreSQL according to your own business scenario, or you can disable it by adding a parameter to the metadata URL Validation.

```shell
juicefs format \
    --storage s3 \
    ... \
    "postgres://user@192.168.1.6:5432/juicefs?sslmode=disable" \
    pics
```

Additional parameters can be appended to the metadata URL. More details can be seen [here](https://pkg.go.dev/github.com/lib/pq#hdr-Connection_String_Parameters).

## MySQL

[MySQL](https://www.mysql.com) is one of the most popular open source relational databases, and is often preferred for web applications.

### Create a file system

When using MySQL as the metadata storage engine, you need to create a database manually before create the file system. The command with the following format is usually used to access the database:

<Tabs>
  <TabItem value="tcp" label="TCP">

```
mysql://<username>[:<password>]@(<host>:3306)/<database-name>
```

  </TabItem>
  <TabItem value="unix-socket" label="Unix socket">

```
mysql://<username>[:<password>]@unix(<socket-file-path>)/<database-name>
```

  </TabItem>
</Tabs>

:::note
Don't leave out the `()` brackets on either side of the URL.
:::

For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" \
    pics
```

A more secure approach would be to pass the database password through the environment variable `META_PASSWORD`:

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "mysql://user@(192.168.1.6:3306)/juicefs" \
    pics
```

To connect to a TLS enabled MySQL server, pass the `tls=true` parameter (or `tls=skip-verify` if using a self-signed certificate):

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs?tls=true" \
    pics
```

### Mount a file system

```shell
juicefs mount -d "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

Passing password with the `META_PASSWORD` environment variable is also supported when mounting a file system.

```shell
export META_PASSWORD="mypassword"
juicefs mount -d "mysql://user@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

To connect to a TLS enabled MySQL server, pass the `tls=true` parameter (or `tls=skip-verify` if using a self-signed certificate):

```shell
juicefs mount -d "mysql://user:mypassword@(192.168.1.6:3306)/juicefs?tls=true" /mnt/jfs
```

For more examples of MySQL database address format, please refer to [Go-MySQL-Driver](https://github.com/Go-SQL-Driver/MySQL/#examples).

## MariaDB

[MariaDB](https://mariadb.org) is an open source branch of MySQL, maintained by the original developers of MySQL.

Because MariaDB is highly compatible with MySQL, there is no difference in usage, the parameters and settings are exactly the same.

For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" \
    pics
```

```shell
juicefs mount -d "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

Passing passwords through environment variables is also the same:

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "mysql://user@(192.168.1.6:3306)/juicefs" \
    pics
```

```shell
juicefs mount -d "mysql://user@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

To connect to a TLS enabled MariaDB server, pass the `tls=true` parameter (or `tls=skip-verify` if using a self-signed certificate):

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "mysql://user@(192.168.1.6:3306)/juicefs?tls=true" \
    pics
```

```shell
juicefs mount -d "mysql://user@(192.168.1.6:3306)/juicefs?tls=true" /mnt/jfs
```

For more examples of MariaDB database address format, please refer to [Go-MySQL-Driver](https://github.com/Go-SQL-Driver/MySQL/#examples).

## SQLite

[SQLite](https://sqlite.org) is a widely used small, fast, single-file, reliable and full-featured SQL database engine.

The SQLite database has only one file, which is very flexible to create and use. When using SQLite as the JuiceFS metadata storage engine, there is no need to create a database file in advance, and you can directly create a file system:

```shell
juicefs format \
    --storage s3 \
    ... \
    "sqlite3://my-jfs.db" \
    pics
```

Executing the above command will automatically create a database file named `my-jfs.db` in the current directory. **Please keep this file properly**!

Mount the file system:

```shell
juicefs mount -d "sqlite3://my-jfs.db" /mnt/jfs/
```

Please note the location of the database file, if it is not in the current directory, you need to specify the absolute path to the database file, e.g.

```shell
juicefs mount -d "sqlite3:///home/herald/my-jfs.db" /mnt/jfs/
```

One can also add driver supported [PRAGMA Statements](https://www.sqlite.org/pragma.html) to the connection string like:

```shell
"sqlite3://my-jfs.db?cache=shared&_busy_timeout=5000"
```

For more examples of SQLite database address format, please refer to [Go-SQLite3 Driver](https://github.com/mattn/go-sqlite3#connection-string).

:::note
Since SQLite is a single-file database, usually only the host where the database is located can access it. Therefore, SQLite database is more suitable for standalone use. For multiple servers sharing the same file system, it is recommended to use databases such as Redis or MySQL.
:::

## BadgerDB

[BadgerDB](https://github.com/dgraph-io/badger) is an embedded, persistent, and standalone Key-Value database developed in pure Go. The database files are stored locally in the specified directory.

When using BadgerDB as the JuiceFS metadata storage engine, use `badger://` to specify the database path.

### Create a file system

You only need to create a file system for use, and there is no need to create a BadgerDB database in advance.

```shell
juicefs format badger://$HOME/badger-data myjfs
```

This command creates `badger-data` as a database directory in the `home` directory of the current user, which is used as metadata storage for JuiceFS.

### Mount a file system

The database path needs to be specified when mounting the file system.

```shell
juicefs mount -d badger://$HOME/badger-data /mnt/jfs
```

:::note
Since BadgerDB is a standalone database, it can only be used locally and does not support multi-host shared mounts. In addition, only one process is allowed to access BadgerDB at the same time, and `gc` and `fsck` operations cannot be performed when the file system is mounted.
:::

## TiKV

[TiKV](https://tikv.org) is a distributed transactional Key-Value database. It is originally developed by PingCAP as the storage layer for their flagship product TiDB. Now TiKV is an independent open source project, and is also a granduated project of CNCF.

By using the official tool TiUP, you can easily build a local playground for testing (refer [here](https://tikv.org/docs/latest/concepts/tikv-in-5-minutes) for details). Production environment generally requires at least three hosts to store three data replicas (refer to the [official document](https://tikv.org/docs/latest/deploy/install/install) for all deployment steps).

:::note
It's recommended to use dedicated TiKV 5.0+ cluster as the metadata engine for JuiceFS.
:::

### Create a file system

When using TiKV as the metadata storage engine, parameters needs to be specified as the following format:

```shell
tikv://<pd_addr>[,<pd_addr>...]/<prefix>
```

The `prefix` is a user-defined string, which can be used to distinguish multiple file systems or applications when they share the same TiKV cluster. For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" \
    pics
```

### Set up TLS

If you need to enable TLS, you can set the TLS configuration item by adding the query parameter after the metadata URL. Currently supported configuration items:

| Name        | Value                                                                                                                                                      |
|-------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ca`        | CA root certificate, used to connect TiKV/PD with TLS                                                                                                      |
| `cert`      | certificate file path, used to connect TiKV/PD with TLS                                                                                                    |
| `key`       | private key file path, used to connect TiKV/PD with TLS                                                                                                    |
| `verify-cn` | verify component caller's identity, [reference link](https://docs.pingcap.com/tidb/stable/enable-tls-between-components#verify-component-callers-identity) |

For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs?ca=/path/to/ca.pem&cert=/path/to/tikv-server.pem&key=/path/to/tikv-server-key.pem&verify-cn=CN1,CN2" \
    pics
```

### Mount a file system

```shell
juicefs mount -d "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" /mnt/jfs
```

## etcd

[etcd](https://etcd.io) is a small-scale key-value database with high availability and reliability, which can be used as metadata storage for JuiceFS.

### Create a file system

When using etcd as the metadata engine, the `Meta-URL` parameter needs to be specified in the following format:

```
etcd://[user:password@]<addr>[,<addr>...]/<prefix>
```

Where `user` and `password` are required when etcd enables user authentication. The `prefix` is a user-defined string. When multiple file systems or applications share an etcd cluster, setting the prefix can avoid confusion and conflict. An example is as follows:

```bash
juicefs format etcd://user:password@192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs pics
```

### Set up TLS

If you need to enable TLS, you can set the TLS configuration item by adding the query parameter after the metadata URL. Currently supported configuration items:

| Name                   | Value                 |
|------------------------|-----------------------|
| `cacert`               | CA root certificate   |
| `cert`                 | certificate file path |
| `key`                  | private key file path |
| `server-name`          | name of server        |
| `insecure-skip-verify` | 1                     |

For example:

```bash
juicefs format \
    --storage s3 \
    ... \
    "etcd://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs?cert=/path/to/ca.pem&cacert=/path/to/etcd-server.pem&key=/path/to/etcd-key.pem&server-name=etcd" \
    pics
```

### Mount a file system

```shell
juicefs mount -d "etcd://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" /mnt/jfs
```

:::note
When mounting to the background, the path to the certificate needs to use an absolute path.
:::

## FoundationDB

[FoundationDB](https://www.foundationdb.org) is a distributed database that can hold large-scale structured data on multiple clustered servers. The database system focuses on high performance, high scalability, and good fault tolerance.

### Create a file system

When using FoundationDB as the metadata engine, the `Meta-URL` parameter needs to be specified in the following format:

```uri
fdb://[config file address]?prefix=<prefix>
```

The `<cluster_file_path>` is the FoundationDB configuration file path, which is used to connect to the FoundationDB server. The `<prefix>` is a user-defined string, which can be used to distinguish multiple file systems or applications when they share the same FoundationDB cluster. For example:

```shell
juicefs format \
    --storage s3 \
    ... \
    "fdb:///etc/foundationdb/fdb.cluster?prefix=jfs" \
    pics
```

### Set up TLS

If you need to enable TLS, the general steps are as follows. For details, please refer to [official documentation](https://apple.github.io/foundationdb/tls.html).

#### Use OpenSSL to generate a CA certificate

```shell
openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout private.key -out cert.crt
cat cert.crt private.key > fdb.pem
```

#### Configure TLS

| Command-line Option    | Client Option      | Environment Variable       | Purpose                                                                    |
|------------------------|--------------------|----------------------------|----------------------------------------------------------------------------|
| `tls_certificate_file` | `TLS_cert_path`    | `FDB_TLS_CERTIFICATE_FILE` | Path to the file from which the local certificates can be loaded           |
| `tls_key_file`         | `TLS_key_path`     | `FDB_TLS_KEY_FILE`         | Path to the file from which to load the private key                        |
| `tls_verify_peers`     | `tls_verify_peers` | `FDB_TLS_VERIFY_PEERS`     | The byte-string for the verification of peer certificates and sessions     |
| `tls_password`         | `tls_password`     | `FDB_TLS_PASSWORD`         | The byte-string representing the passcode for unencrypting the private key |
| `tls_ca_file`          | `TLS_ca_path`      | `FDB_TLS_CA_FILE`          | Path to the file containing the CA certificates to trust                   |

#### Configure the server

The TLS parameters can be configured in `foundationdb.conf` or environment variables, as shown in the following configuration files (emphasis on the `[foundationdb.4500]` configuration).

```ini title="foundationdb.conf"
[fdbmonitor]
user = foundationdb
group = foundationdb

[general]
restart-delay = 60
## by default, restart-backoff = restart-delay-reset-interval = restart-delay
# initial-restart-delay = 0
# restart-backoff = 60
# restart-delay-reset-interval = 60
cluster-file = /etc/foundationdb/fdb.cluster
# delete-envvars =
# kill-on-configuration-change = true
## Default parameters for individual fdbserver processes

[fdbserver]
command = /usr/sbin/fdbserver
#public-address = auto:$ID
#listen-address = public
datadir = /var/lib/foundationdb/data/$ID
logdir = /var/log/foundationdb
# logsize = 10MiB
# maxlogssize = 100MiB
# machine-id =
# datacenter-id =
# class =
# memory = 8GiB
# storage-memory = 1GiB
# cache-memory = 2GiB
# metrics-cluster =
# metrics-prefix =

[fdbserver.4500]
Public - address = 127.0.0.1:4500: TLS
listen-address = public
tls_certificate_file = /etc/foundationdb/fdb.pem
tls_ca_file = /etc/foundationdb/cert.crt
tls_key_file = /etc/foundationdb/private.key
tls_verify_peers= Check.Valid=0

[backup_agent]
command = /usr/lib/foundationdb/backup_agent/backup_agent
logdir = /var/log/foundationdb

[backup_agent.1]
```

In addition, you need to add the suffix `:tls` after the address in `fdb.cluster`, `fdb.cluster` is as follows:

```uri title="fdb.cluster"
U6pT9Jhl:ClZfjAWM@127.0.0.1:4500:tls
```

#### Configure the client

You need to configure TLS parameters and `fdb.cluster` on the client machine, `fdbcli` is the same.

Connected by `fdbcli`:

```shell
fdbcli --tls_certificate_file=/etc/foundationdb/fdb.pem \
       --tls_ca_file=/etc/foundationdb/cert.crt \
       --tls_key_file=/etc/foundationdb/private.key \
       --tls_verify_peers=Check.Valid=0
```

Connected by API (`fdbcli` also applies):

```shell
export FDB_TLS_CERTIFICATE_FILE=/etc/foundationdb/fdb.pem \
export FDB_TLS_CA_FILE=/etc/foundationdb/cert.crt \
export FDB_TLS_KEY_FILE=/etc/foundationdb/private.key \
export FDB_TLS_VERIFY_PEERS=Check.Valid=0
```

### Mount a file system

```shell
juicefs mount -d \
    "fdb:///etc/foundationdb/fdb.cluster?prefix=jfs" \
    /mnt/jfs
```
