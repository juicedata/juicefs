---
title: 如何设置元数据引擎
sidebar_position: 2
slug: /databases_for_metadata
description: JuiceFS 支持 Redis、TiKV、PostgreSQL、MySQL 等多种数据库作为元数据引擎，本文分别介绍相应的设置和使用方法。
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

:::tip
`META_PASSWORD` 是 JuiceFS v1.0 新增功能，旧版客户端需要[升级](../administration/upgrade.md)后才能使用。
:::

JuiceFS 采用数据和元数据分离的存储架构，元数据可以存储在任意支持的数据库中，称为「元数据存储引擎」。JuiceFS 支持众多元数据存储引擎，各个数据库性能、易用性、场景均有区别，具体性能对比可参考[该文档](../benchmark/metadata_engines_benchmark.md)。

## 元数据存储用量 {#storage-usage}

元数据所需的存储空间跟文件名的长度、文件的类型和长度以及扩展属性等相关，无法准确地估计一个文件系统的元数据存空间需求。简单起见，我们可以根据没有扩展属性的单个小文件所需的存储空间来做近似：

- **键值（Key-Value）数据库**（如 Redis、TiKV）：300 字节／文件
- **关系型数据库**（如 SQLite、MySQL、PostgreSQL）：600 字节／文件

当平均文件更大（超过 64MB），或者文件被频繁修改导致有很多碎片，或者有很多扩展属性，或者平均文件名很长（超过 50 字节），都会导致需要更多的存储空间。

当你需要在两种类型的元数据引擎之间迁移时，就可以据此来估算所需的存储空间。例如，假设你希望将元数据引擎从一个关系型数据库（MySQL）迁移到键值数据库（Redis），如果当前 MySQL 的用量为 30GB，那么目标 Redis 至少需要准备 15GB 以上的内存。反之亦然。

## Redis

JuiceFS 要求使用 4.0 及以上版本的 Redis。JuiceFS 也支持使用 Redis Cluster 作为元数据引擎，但为了避免在 Redis 集群中执行跨节点事务，同一个文件系统的元数据总会坐落于单个 Redis 实例中。

为了保证元数据安全，JuiceFS 需要 [`maxmemory-policy noeviction`](https://redis.io/docs/reference/eviction/)，否则在启动 JuiceFS 的时候将会尝试将其设置为 `noeviction`，如果设置失败将会打印告警日志。更多可以参考 [Redis 最佳实践](../administration/metadata/redis_best_practices.md)。

### 创建文件系统

使用 Redis 作为元数据存储引擎时，通常使用以下格式访问数据库：

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

其中，`[]` 括起来的是可选项，其它部分为必选项。

- 如果开启了 Redis 的 [TLS](https://redis.io/docs/manual/security/encryption) 特性，协议头需要使用 `rediss://`，否则使用 `redis://`。
- `<username>` 是 Redis 6.0 之后引入的，如果没有用户名可以忽略，但密码前面的 `:` 冒号需要保留，如 `redis://:<password>@<host>:6379/1`。
- Redis 监听的默认端口号为 `6379`，如果没有改变默认端口号可以不用填写，如 `redis://:<password>@<host>/1`，否则需要显式指定端口号。
- Redis 支持多个[逻辑数据库](https://redis.io/commands/select)，请将 `<db>` 替换为实际使用的数据库编号。
- 如果需要连接 Redis 哨兵（Sentinel），元数据 URL 的格式会稍有不同，具体请参考[「Redis 最佳实践」](../administration/metadata/redis_best_practices.md#数据可用性)。
- 如果 Redis 的用户名或者密码中包含特殊字符，需要使用单引号进行封闭，避免 shell 进行解释。或者使用环境变量 `REDIS_PASSWORD` 进行传递。

:::tip 提示
一个 Redis 实例默认可以创建 16 个逻辑数据库，而一个逻辑数据库可以创建一个 JuiceFS 文件系统。也就是说，在默认情况下，你可以使用一个 Redis 实例创建 16 个 JuiceFS 文件系统。需要注意，用于 JuiceFS 的逻辑数据库不要和其他应用共享，否则可能会造成数据混乱。
:::

例如，创建名为 `pics` 的文件系统，使用 Redis 的 `1` 号数据库存储元数据：

```shell
juicefs format \
    --storage s3 \
    ... \
    "redis://:mypassword@192.168.1.6:6379/1" \
    pics
```

安全起见，建议使用环境变量 `META_PASSWORD` 或 `REDIS_PASSWORD` 传递数据库密码，例如：

```shell
export META_PASSWORD=mypassword
```

然后就无需在元数据 URL 中设置密码了：

```shell
juicefs format \
    --storage s3 \
    ... \
    "redis://192.168.1.6:6379/1" \
    pics
```

### 挂载文件系统

如果需要在多台服务器上共享同一个文件系统，必须确保每台服务器都能访问到存储元数据的数据库。

```shell
juicefs mount -d "redis://:mypassword@192.168.1.6:6379/1" /mnt/jfs
```

挂载文件系统也支持用 `META_PASSWORD` 或 `REDIS_PASSWORD` 环境变量传递密码：

```shell
export META_PASSWORD=mypassword
juicefs mount -d "redis://192.168.1.6:6379/1" /mnt/jfs
```

### 设置 TLS

JuiceFS 同时支持 Redis 的 TLS 单向加密认证和 mTLS 双向加密认证连接。通过 TLS 或 mTLS 连接到 Redis 时均使用 `rediss://` 协议头，但是在使用 TLS 单向加密认证时，不需要指定客户端证书和私钥。

:::note
对 Redis mTLS 功能的支持需要使用 1.1.0 及以上版本的 JuiceFS
:::

当通过 mTLS 连接 Redis 时，需要提供客户端证书和私钥，以及签发客户端证书的 CA 证书进行连接。在 JuiceFS 中，可以通过以下方式设置 mTLS 需要的客户端证书：

```shell
juicefs format --storage s3 \
    ... \
    "rediss://192.168.1.6:6379/1?tls-cert-file=/etc/certs/client.crt&tls-key-file=/etc/certs/client.key&tls-ca-cert-file=/etc/certs/ca.crt"
    pics
```

上面的示例代码使用 `rediss://` 协议头来开启 mTLS 功能，然后使用以下选项来指定客户端证书的路径：

- `tls-cert-file=<path>` 指定客户端证书的路径
- `tls-key-file=<path>` 指定客户端密钥的路径
- `tls-ca-cert-file=<path>` 指定签发客户端证书的 CA 证书路径，它是可选的，如果不指定，客户端会使用系统默认的 CA 证书进行验证。
- `insecure-skip-verify=true` 可以用来跳过对服务端证书的验证

在 URL 指定选项时，以 `?` 符号开头，使用 `&` 符号来分隔多个选项，例如：`?tls-cert-file=client.crt&tls-key-file=client.key`。

上例中的 `/etc/certs` 只是一个目录，实际使用时请替换为你的证书目录，可以使用相对路径或绝对路径。

## KeyDB

[KeyDB](https://keydb.dev) 是 Redis 的开源分支，在开发上保持与 Redis 主线对齐。KeyDB 在 Redis 的基础上实现了多线程支持、更好的内存利用率和更大的吞吐量，另外还支持 [Active Replication](https://github.com/JohnSully/KeyDB/wiki/Active-Replication)，即 Active Active（双活）功能。

:::note 注意
KeyDB 的数据复制是异步的，使用 Active Active（双活）功能可能导致数据一致性问题，请务必充分验证、谨慎使用！
:::

在用于 JuiceFS 元数据存储时，KeyDB 与 Redis 的用法完全一致，这里不再赘述，请参考 [Redis](#redis) 部分使用。

## PostgreSQL

[PostgreSQL](https://www.postgresql.org) 是功能强大的开源关系型数据库，有完善的生态和丰富的应用场景，也可以用来作为 JuiceFS 的元数据引擎。

许多云计算平台都提供托管的 PostgreSQL 数据库服务，也可以按照[使用向导](https://www.postgresqltutorial.com/postgresql-getting-started)自己部署一个。

其他跟 PostgreSQL 协议兼容的数据库（比如 CockroachDB 等) 也可以这样使用。

### 创建文件系统

使用 PostgreSQL 作为元数据引擎时，需要提前手动创建数据库，使用如下的格式来指定参数：

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

其中，`[]` 括起来的是可选项，其它部分为必选项。

例如：

```shell
juicefs format \
    --storage s3 \
    ... \
    "postgres://user:mypassword@192.168.1.6:5432/juicefs" \
    pics
```

更安全的做法是可以通过环境变量 `META_PASSWORD` 传递数据库密码：

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "postgres://user@192.168.1.6:5432/juicefs" \
    pics
```

:::note 说明

1. JuiceFS 默认使用的 public [schema](https://www.postgresql.org/docs/current/ddl-schemas.html) ，如果要使用非 `public schema`，需要在连接字符串中指定 `search_path` 参数，例如 `postgres://user:mypassword@192.168.1.6:5432/juicefs?search_path=pguser1`
2. 如果 `public schema` 并非是 PostgreSQL 服务端配置的 `search_path` 中第一个命中的，则必须在连接字符串中明确设置 `search_path` 参数
3. `search_path` 连接参数原生可以设置为多个 schema，但是目前 JuiceFS 仅支持设置一个。`postgres://user:mypassword@192.168.1.6:5432/juicefs?search_path=pguser1,public` 将被认为不合法
4. 密码中的特殊字符需要进行 url 编码，例如 `|` 需要编码为`%7C`。

:::

### 挂载文件系统

```shell
juicefs mount -d "postgres://user:mypassword@192.168.1.6:5432/juicefs" /mnt/jfs
```

挂载文件系统也支持用 `META_PASSWORD` 环境变量传递密码：

```shell
export META_PASSWORD="mypassword"
juicefs mount -d "postgres://user@192.168.1.6:5432/juicefs" /mnt/jfs
```

### 故障排除

JuiceFS 客户端默认采用 SSL 加密连接 PostgreSQL，如果连接时报错  `pq: SSL is not enabled on the server` 说明数据库没有启用 SSL。可以根据业务场景为 PostgreSQL 启用 SSL 加密，也可以在元数据 URL 中添加参数禁用加密验证：

```shell
juicefs format \
    --storage s3 \
    ... \
    "postgres://user@192.168.1.6:5432/juicefs?sslmode=disable" \
    pics
```

元数据 URL 中还可以附加更多参数，[查看详情](https://pkg.go.dev/github.com/lib/pq#hdr-Connection_String_Parameters)。

## MySQL

[MySQL](https://www.mysql.com) 是受欢迎的开源关系型数据库之一，常被作为 Web 应用程序的首选数据库。

>[MariaDB](https://mariadb.org) 是 MySQL 的一个开源分支，由 MySQL 原始开发者维护并保持开源，与 MySQL 高度兼容，在设置元数据引擎方法上也没有任何差别。
>
>[OceanBase](https://www.oceanbase.com)是一款自主研发的分布式关系型数据库，专为处理海量数据和高并发事务而设计，具备高性能、强一致性和高可用性的特点。同时，OceanBase 与 MySQL 高度兼容，在设置元数据引擎方法上也没有任何差别。

### 创建文件系统

使用 MySQL 作为元数据存储引擎时，需要提前手动创建数据库，通常使用以下格式访问数据库：

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

:::note 注意

1. 不要漏掉 URL 两边的 `()` 括号
2. 密码中的特殊字符不需要进行 url 编码

:::

例如：

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" \
    pics
```

更安全的做法是可以通过环境变量 `META_PASSWORD` 传递数据库密码：

```shell
export META_PASSWORD="mypassword"
juicefs format \
    --storage s3 \
    ... \
    "mysql://user@(192.168.1.6:3306)/juicefs" \
    pics
```

要连接到启用 TLS 的 MySQL 服务器，请传递 `tls=true` 参数（或 `tls=skip-verify` 如果使用自签名证书）：

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs?tls=true" \
    pics
```

要启用 JuiceFS 到 MySQL 服务器建立连接的超时控制，请传递 `timeout=5s` 参数（时间可自定义）：

```shell
juicefs format \
    --storage s3 \
    ... \
    "mysql://user:mypassword@(192.168.1.6:3306)/juicefs?timeout=5s" \
    pics
```

:::note 注意

设置建立连接超时，在 JuiceFS 和 MySQL 间出现网络故障场景时，能明确控制对 JuiceFS 文件系统进行读写的阻塞时间，从而可控的对网络故障进行响应。

:::

### 挂载文件系统

```shell
juicefs mount -d "mysql://user:mypassword@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

挂载文件系统也支持用 `META_PASSWORD` 环境变量传递密码：

```shell
export META_PASSWORD="mypassword"
juicefs mount -d "mysql://user@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

要连接到启用 TLS 的 MySQL 服务器，请传递 `tls=true` 参数（或 `tls=skip-verify` 如果使用自签名证书）：

```shell
juicefs mount -d "mysql://user:mypassword@(192.168.1.6:3306)/juicefs?tls=true" /mnt/jfs
```

更多 MySQL 数据库的地址格式示例，[点此查看](https://github.com/Go-SQL-Driver/MySQL/#examples)。

## SQLite

[SQLite](https://sqlite.org) 是全球广泛使用的小巧、快速、单文件、可靠、全功能的单文件 SQL 数据库引擎。

SQLite 数据库只有一个文件，创建和使用都非常灵活，用它作为 JuiceFS 元数据存储引擎时无需提前创建数据库文件，可以直接创建文件系统：

```shell
juicefs format \
    --storage s3 \
    ... \
    "sqlite3://my-jfs.db" \
    pics
```

以上命令会在当前目录创建名为 `my-jfs.db` 的数据库文件，请 **务必妥善保管** 这个数据库文件！

挂载文件系统：

```shell
juicefs mount -d "sqlite3://my-jfs.db" /mnt/jfs/
```

请注意数据库文件的位置，如果不在当前目录，则需要指定数据库文件的绝对路径，比如：

```shell
juicefs mount -d "sqlite3:///home/herald/my-jfs.db" /mnt/jfs/
```

也可以在连接字符串中添加参数来支持 [PRAGMA 语句](https://www.sqlite.org/pragma.html)：

```shell
"sqlite3://my-jfs.db?cache=shared&_busy_timeout=5000"
```

更多 SQLite 数据库的地址格式示例，请参考 [Go-SQLite3 Driver](https://github.com/mattn/go-sqlite3#connection-string)。

:::note 注意
由于 SQLite 是一款单文件数据库，在不做特殊共享设置的情况下，只有数据库所在的主机可以访问它。对于多台服务器共享同一文件系统的情况，需要使用 Redis 或 MySQL 等数据库。
:::

## BadgerDB

[BadgerDB](https://github.com/dgraph-io/badger) 是一个 Go 语言开发的嵌入式、持久化的单机 Key-Value 数据库，它的数据库文件存储在本地你指定的目录中。

使用 BadgerDB 作为 JuiceFS 元数据存储引擎时，使用 `badger://` 协议头指定数据库路径。

### 创建文件系统

无需提前创建 BadgerDB 数据库，直接创建文件系统即可：

```shell
juicefs format badger://$HOME/badger-data myjfs
```

上述命令在当前用户的 `home` 目录创建 `badger-data` 作为数据库目录，并以此作为 JuiceFS 的元数据存储。

### 挂载文件系统

挂载文件系统时需要指定数据库路径：

```shell
juicefs mount -d badger://$HOME/badger-data /mnt/jfs
```

:::tip 提示
BadgerDB 只允许单进程访问，如果需要执行 `gc`、`fsck`、`dump`、`load` 等操作，需要先卸载文件系统。
:::

## TiKV

[TiKV](https://tikv.org) 是一个分布式事务型的键值数据库，最初作为 PingCAP 旗舰产品 TiDB 的存储层而研发，现已独立开源并从 CNCF 毕业。

TiKV 的测试环境搭建非常简单，使用官方提供的 TiUP 工具即可实现一键部署，具体可参见[这里](https://tikv.org/docs/latest/concepts/tikv-in-5-minutes)。生产环境一般需要至少三个节点来存储三份数据副本，部署步骤可以参考[官方文档](https://tikv.org/docs/latest/deploy/install/install)。

:::note 注意
建议使用独立部署的 TiKV 5.0+ 集群作为 JuiceFS 的元数据引擎
:::

### 创建文件系统

使用 TiKV 作为元数据引擎时，需要使用如下格式来指定参数：

```shell
tikv://<pd_addr>[,<pd_addr>...]/<prefix>
```

其中 `prefix` 是一个用户自定义的字符串，当多个文件系统或者应用共用一个 TiKV 集群时，设置前缀可以避免混淆和冲突。示例如下：

```shell
juicefs format \
    --storage s3 \
    ... \
    "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" \
    pics
```

### 设置 TLS

如果需要开启 TLS，可以通过在元数据 URL 后以添加 query 参数的形式设置 TLS 的配置项，目前支持的配置项：

| 配置项      | 值                                                                                                                                                                                                |
|-------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `ca`        | CA 根证书，用于用 TLS 连接 TiKV/PD                                                                                                                                                                |
| `cert`      | 证书文件路径，用于用 TLS 连接 TiKV/PD                                                                                                                                                             |
| `key`       | 私钥文件路径，用于用 TLS 连接 TiKV/PD                                                                                                                                                             |
| `verify-cn` | 证书通用名称，用于验证调用者身份，[详情](https://docs.pingcap.com/zh/tidb/stable/enable-tls-between-components#%E8%AE%A4%E8%AF%81%E7%BB%84%E4%BB%B6%E8%B0%83%E7%94%A8%E8%80%85%E8%BA%AB%E4%BB%BD) |

例子：

```shell
juicefs format \
    --storage s3 \
    ... \
    "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs?ca=/path/to/ca.pem&cert=/path/to/tikv-server.pem&key=/path/to/tikv-server-key.pem&verify-cn=CN1,CN2" \
    pics
```

### 挂载文件系统

```shell
juicefs mount -d "tikv://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" /mnt/jfs
```

## etcd

[etcd](https://etcd.io) 是一个高可用高可靠的小规模键值数据库，可以用作 JuiceFS 的元数据存储。

### 创建文件系统

使用 etcd 作为元数据引擎时，需要使用如下格式来指定 `Meta-URL` 参数：

```
etcd://[user:password@]<addr>[,<addr>...]/<prefix>
```

其中 `user` 和 `password` 是当 etcd 开启了用户认证时需要。`prefix` 是一个用户自定义的字符串，当多个文件系统或者应用共用一个 etcd 集群时，设置前缀可以避免混淆和冲突。示例如下：

```shell
juicefs format etcd://user:password@192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs pics
```

### 设置 TLS

如果需要开启 TLS，可以通过在元数据 URL 后以添加 query 参数的形式设置 TLS 的配置项，注意证书文件请使用绝对路径，避免后台挂载时找不到文件。

| 配置项               | 值           |
|----------------------|--------------|
| cacert               | CA 根证书    |
| cert                 | 证书文件路径 |
| key                  | 私钥文件路径 |
| server-name          | 服务器名称   |
| insecure-skip-verify | 1            |

例子：

```shell
juicefs format \
    --storage s3 \
    ... \
    "etcd://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs?cert=/path/to/ca.pem&cacert=/path/to/etcd-server.pem&key=/path/to/etcd-key.pem&server-name=etcd" \
    pics
```

### 挂载文件系统

```shell
juicefs mount -d "etcd://192.168.1.6:2379,192.168.1.7:2379,192.168.1.8:2379/jfs" /mnt/jfs
```

## FoundationDB <VersionAdd>1.1</VersionAdd>

[FoundationDB](https://www.foundationdb.org) 是一个能在多集群服务器上存放大规模结构化数据的分布式数据库。该数据库系统专注于高性能、高可扩展性，且具有不错的容错能力。由于对接 FoundationDB 需要先安装其客户端库，因此 JuiceFS 的发布版本默认不支持，使用前需要自行编译。

### 编译 JuiceFS

首先安装 FoundationDB 客户端（参考[官方文档](https://apple.github.io/foundationdb/api-general.html#installing-client-binaries)）：

<Tabs>
  <TabItem value="debian" label="Debian 及衍生版本">

```shell
curl -O https://github.com/apple/foundationdb/releases/download/6.3.25/foundationdb-clients_6.3.25-1_amd64.deb
sudo dpkg -i foundationdb-clients_6.3.25-1_amd64.deb
```

  </TabItem>
  <TabItem value="centos" label="RHEL 及衍生版本">

```shell
curl -O https://github.com/apple/foundationdb/releases/download/6.3.25/foundationdb-clients-6.3.25-1.el7.x86_64.rpm
sudo rpm -Uvh foundationdb-clients-6.3.25-1.el7.x86_64.rpm
```

  </TabItem>
</Tabs>

然后编译支持 FoundationDB 的 JuiceFS：

```shell
make juicefs.fdb
```

### 创建文件系统

使用 FoundationDB 作为元数据引擎时，需要使用如下格式来指定 `Meta-URL` 参数：

```uri
fdb://<cluster_file_path>?prefix=<prefix>
```

其中 `<cluster_file_path>` 为 FoundationDB 的配置文件路径，用来连接 FoundationDB 服务端。`<prefix>` 是一个用户自定义的字符串，当多个文件系统或者应用共用一个 FoundationDB 集群时，设置前缀可以避免混淆和冲突。示例如下：

```shell
juicefs.fdb format \
    --storage s3 \
    ... \
    "fdb:///etc/foundationdb/fdb.cluster?prefix=jfs" \
    pics
```

### 设置 TLS

如果需要开启 TLS，大体步骤如下，详细信息请参考[官方文档](https://apple.github.io/foundationdb/tls.html)。

#### 使用 OpenSSL 生成 CA 证书

```shell
openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout private.key -out cert.crt
cat cert.crt private.key > fdb.pem
```

#### 配置 TLS

| 命令行选项             | 客户端选项         | 环境变量                   | 目的                               |
|------------------------|--------------------|----------------------------|------------------------------------|
| `tls_certificate_file` | `TLS_cert_path`    | `FDB_TLS_CERTIFICATE_FILE` | 可以从中加载本地证书的文件的路径   |
| `tls_key_file`         | `TLS_key_path`     | `FDB_TLS_KEY_FILE`         | 从中加载私钥的文件的路径           |
| `tls_verify_peers`     | `TLS_verify_peers` | `FDB_TLS_VERIFY_PEERS`     | 用于验证对等证书和会话的字节字符串 |
| `tls_password`         | `TLS_password`     | `FDB_TLS_PASSWORD`         | 表示用于解密私钥的密码的字节字符串 |
| `tls_ca_file`          | `TLS_ca_path`      | `FDB_TLS_CA_FILE`          | 包含要信任的 CA 证书的文件的路径   |

#### 配置服务端 TLS

可以在 `foundationdb.conf` 或者环境变量中配置 TLS 参数，配置文件如下（重点在 `[foundationdb.4500]` 配置中）。

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
public-address = 127.0.0.1:4500:tls
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

除此之外还需将 `fdb.cluster` 中的地址加上 `:tls` 后缀，`fdb.cluster` 示例如下：

```uri title="fdb.cluster"
u6pT9Jhl:ClZfjAWM@127.0.0.1:4500:tls
```

#### 配置客户端

在客户端机器上需要配置 TLS 参数以及 `fdb.cluster`，`fdbcli` 同理。

通过 `fdbcli` 连接时：

```shell
fdbcli --tls_certificate_file=/etc/foundationdb/fdb.pem \
       --tls_ca_file=/etc/foundationdb/cert.crt \
       --tls_key_file=/etc/foundationdb/private.key \
       --tls_verify_peers=Check.Valid=0
```

通过 API 连接时（`fdbcli` 也适用）：

```shell
export FDB_TLS_CERTIFICATE_FILE=/etc/foundationdb/fdb.pem \
export FDB_TLS_CA_FILE=/etc/foundationdb/cert.crt \
export FDB_TLS_KEY_FILE=/etc/foundationdb/private.key \
export FDB_TLS_VERIFY_PEERS=Check.Valid=0
```

### 挂载文件系统

```shell
juicefs.fdb mount -d \
    "fdb:///etc/foundationdb/fdb.cluster?prefix=jfs" \
    /mnt/jfs
```
