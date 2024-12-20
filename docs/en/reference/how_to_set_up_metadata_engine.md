---
title: How to Set Up Metadata Engine
sidebar_position: 2
slug: /databases_for_metadata
description: JuiceFS supports Redis, TiKV, PostgreSQL, MySQL and other databases as metadata engines, and this article describes how to set up and use them.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

:::tip
`META_PASSWORD` is supported from JuiceFS v1.0. You should [upgrade](../administration/upgrade.md) if you're still using older versions.
:::

JuiceFS is a decoupled structure that separates data and metadata. Metadata can be stored in any supported database (called Metadata Engine). Many databases are supported and they all comes with different performance and intended scenarios, refer to [our docs](../benchmark/metadata_engines_benchmark.md) for comparison.

## The storage usage of metadata {#storage-usage}

The storage space required for metadata is related to the length of the file name, the type and length of the file, and extended attributes. It is difficult to accurately estimate the metadata storage space requirements of a file system. For simplicity, we can approximate based on the storage space required for a single small file without extended attributes.

- **Key-Value Database** (e.g. Redis, TiKV): 300 bytes/file
- **Relational Database** (e.g. SQLite, MySQL, PostgreSQL): 600 bytes/file

When the average file is larger (over 64MB), or the file is frequently modified and has a lot of fragments, or there are many extended attributes, or the average file name is long (over 50 bytes), more storage space is needed.

When you need to migrate between two types of metadata engines, you can use this method to estimate the required storage space. For example, if you want to migrate the metadata engine from a relational database (MySQL) to a key-value database (Redis), and the current usage of MySQL is 30GB, then the target Redis needs to prepare at least 15GB or more of memory. The reverse is also true.

## Redis

JuiceFS requires Redis version 4.0 and above. Redis Cluster is also supported, but in order to avoid transactions across different Redis instances, JuiceFS puts all metadata for one file system on a single Redis instance.

To ensure metadata security, JuiceFS requires [`maxmemory-policy noeviction`](https://redis.io/docs/reference/eviction/), otherwise it will try to set it to `noeviction` when starting JuiceFS, and will print a warning log if it fails. Refer to [Redis Best practices](../administration/metadata/redis_best_practices.md) for more.

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
- If you need to connect to Redis Sentinel, the format will be slightly different, refer to [Redis Best Practices](../administration/metadata/redis_best_practices.md#high-availability) for details.
- If username / password contains special characters, use single quote to avoid unexpected shell interpretations, or use the `REDIS_PASSWORD` environment.

:::tip
A Redis instance can, by default, create a total of 16 logical databases, with each of these databases eligible for the creation of a singular JuiceFS file system. Thus, under ordinary circumstances, a single Redis instance may be utilized to form up to 16 JuiceFS file systems. However, it is crucial to note that the logical databases intended for use with JuiceFS must not be shared with other applications, as doing so could lead to data inconsistencies.
:::

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

### Mount a file system

If you need to share the same file system across multiple nodes, ensure that all nodes has access to the Metadata Engine.

```shell
juicefs mount -d "redis://:mypassword@192.168.1.6:6379/1" /mnt/jfs
```

Passing passwords with the `META_PASSWORD` or `REDIS_PASSWORD` environment variables is also supported.

```shell
export META_PASSWORD=mypassword
juicefs mount -d "redis://192.168.1.6:6379/1" /mnt/jfs
```

### Set up TLS

JuiceFS supports both TLS server-side encryption authentication and mTLS mutual encryption authentication connections to Redis. When connecting to Redis via TLS or mTLS, use the `rediss://` protocol header. However, when using TLS server-side encryption authentication, it is not necessary to specify the client certificate and private key.

:::note
Using Redis mTLS requires JuiceFS version 1.1.0 and above
:::

If Redis server has enabled mTLS feature, it is necessary to provide client certificate, private key, and CA certificate that issued the client certificate to connect. In JuiceFS, mTLS can be used in the following way:

```shell
juicefs format --storage s3 \
    ... \
    "rediss://192.168.1.6:6379/1?tls-cert-file=/etc/certs/client.crt&tls-key-file=/etc/certs/client.key&tls-ca-cert-file=/etc/certs/ca.crt"
    pics
```

In the code mentioned above, we use the `rediss://` protocol header to enable mTLS functionality, and then use the following options to specify the path of the client certificate:

- `tls-cert-file=<path>`: The path of the client certificate.
- `tls-key-file=<path>`: The path of the private key.
- `tls-ca-cert-file=<path>`: The path of the CA certificate. It is optional. If it is not specified, the system CA certificate will be used.
- `insecure-skip-verify=true` It can skip verifying the server certificate.

When specifying options in a URL, start with the `?` symbol and use the `&` symbol to separate multiple options, for example: `?tls-cert-file=client.crt&tls-key-file=client.key`.

In the above example, `/etc/certs` is just a directory name. Replace it with your actual certificate directory when using it, which can be a relative or absolute path.

## KeyDB

[KeyDB](https://keydb.dev) is an open source fork of Redis, developed to stay aligned with the Redis community. KeyDB implements multi-threading support, better memory utilization, and greater throughput on top of Redis, and also supports [Active Replication](https://github.com/JohnSully/KeyDB/wiki/Active-Replication), i.e., the Active Active feature.

:::note
Same as Redis, the Active Replication is asynchronous, which may cause consistency issues. So use with caution!
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
4. Special characters in the password need to be replaced by url encoding. For example, `|` needs to be replaced with `%7C`.

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

>[MariaDB](https://mariadb.org) is an open source branch of MySQL, maintained by the original developers of MySQL. With its high compatibility with MySQL, setting up the Meta engine in MariaDB uses the same parameters and configurations as MySQL.
>
>[OceanBase](https://en.oceanbase.com) is a self-developed distributed relational database designed for processing massive data and high-concurrency transactions. It features high performance, strong consistency, and high availability. OceanBase is also highly compatible with MySQL, allowing the metadata engine to be configured in the same way.

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

1. Don't leave out the `()` brackets on either side of the URL.
2. Special characters in passwords do not require url encoding

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

:::tip
BadgerDB only allows single-process access. If you need to perform operations like `gc`, `fsck`, `dump`, and `load`, you need to unmount the file system first.
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

```shell
juicefs format etcd://user:password@192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs pics
```

### Set up TLS

If you need to enable TLS, set the TLS configuration item by adding the query parameter after the metadata URL, use absolute path for certificate files to avoid file not found error.

| Name                   | Value                 |
|------------------------|-----------------------|
| `cacert`               | CA root certificate   |
| `cert`                 | certificate file path |
| `key`                  | private key file path |
| `server-name`          | name of server        |
| `insecure-skip-verify` | 1                     |

For example:

```shell
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

## FoundationDB <VersionAdd>1.1</VersionAdd>

[FoundationDB](https://www.foundationdb.org) is a distributed database that can hold large-scale structured data on multiple clustered servers. The database system focuses on high performance, high scalability, and good fault tolerance. Using FoundationDB as the metadata engine requires its client library, so by default it is not enabled in the JuiceFS released binaries. If you need to use it, please compile it yourself.

### Compile JuiceFS

First, you need to install the FoundationDB client library (refer to the [official documentation](https://apple.github.io/foundationdb/api-general.html#installing-client-binaries) for more details):

<Tabs>
  <TabItem value="debian" label="Debian and derivatives">

```shell
curl -O https://github.com/apple/foundationdb/releases/download/6.3.25/foundationdb-clients_6.3.25-1_amd64.deb
sudo dpkg -i foundationdb-clients_6.3.25-1_amd64.deb
```

  </TabItem>
  <TabItem value="centos" label="RHEL and derivatives">

```shell
curl -O https://github.com/apple/foundationdb/releases/download/6.3.25/foundationdb-clients-6.3.25-1.el7.x86_64.rpm
sudo rpm -Uvh foundationdb-clients-6.3.25-1.el7.x86_64.rpm
```

  </TabItem>
</Tabs>

Then, compile JuiceFS supporting FoundationDB:

```shell
make juicefs.fdb
```

### Create a file system

When using FoundationDB as the metadata engine, the `Meta-URL` parameter needs to be specified in the following format:

```uri
fdb://[config file address]?prefix=<prefix>
```

The `<cluster_file_path>` is the FoundationDB configuration file path, which is used to connect to the FoundationDB server. The `<prefix>` is a user-defined string, which can be used to distinguish multiple file systems or applications when they share the same FoundationDB cluster. For example:

```shell
juicefs.fdb format \
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
juicefs.fdb mount -d \
    "fdb:///etc/foundationdb/fdb.cluster?prefix=jfs" \
    /mnt/jfs
```
