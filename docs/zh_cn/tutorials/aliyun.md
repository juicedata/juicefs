---
title: 在阿里云使用 JuiceFS
sidebar_position: 7
slug: /clouds/aliyun
---

如下图所示，JuiceFS 存储由数据库和对象存储共同驱动。存入 JuiceFS 的文件会按照一定的规则被拆分成固定大小的数据块存储在对象存储中，数据对应的元数据则会存储在数据库中。

元数据完全独立存储，对文件的检索和处理并不会直接操作对象存储中的数据，而是先在数据库中操作元数据，只有当数据发生变化的时候，才会与对象存储交互。

这样的设计可以有效缩减对象存储在请求数量上的费用，同时也能让我们显著感受到 JuiceFS 带来的性能提升。

![JuiceFS-aliyun](../images/juicefs-aliyun.png)

## 准备

通过前面的架构描述，可以知道 JuiceFS 需要搭配数据库和对象存储一起使用。这里我们直接使用阿里云的 ECS 云服务器，结合云数据库和 OSS 对象存储。

在创建云计算资源时，尽量选择在相同的区域，这样可以让资源之间通过内网线路相互访问，避免使用公网线路产生额外的流量费用。

### 一、云服务器 ECS

JuiceFS 对服务器硬件没有特殊要求，一般来说，云平台上最低配的云服务器也能稳定使用 JuiceFS，通常你只需要选择能够满足自身业务的配置即可。

需要特别说明的是，你不需要为使用 JuiceFS 重新购买服务器或是重装系统，JuiceFS 没有业务入侵性，不会对你现有的系统和程序造成任何的干扰，你完全可以在正在运行的服务器上安装和使用 JuiceFS。

JuiceFS 默认会占用不超过 1GB 的硬盘空间作为缓存，可以根据需要调整缓存空间的大小。该缓存是客户端与对象存储之间的一个数据缓冲层，选择性能更好的云盘，可以获得更好的性能表现。

在操作系统方面，阿里云 ECS 提供的所有操作系统都可以安装 JuiceFS。

**本文使用的 ECS 配置如下：**

| **实例规格**     | ecs.t5-lc1m1.small       |
| ---------------- | ------------------------ |
| **CPU**          | 1 核                     |
| **内存**         | 1 GB                     |
| **存储**         | 40 GB                    |
| **操作系统**     | Ubuntu Server 20.04 64 位 |
| **地域及可用区** | 华东 2（上海）           |

### 二、云数据库

JuiceFS 会将数据对应的元数据全部存储在独立的数据库中，目前已开放支持的数据库有 Redis、MySQL、PostgreSQL、SQLite，以及 OceanBase。

根据数据库类型的不同，带来的元数据性能和可靠性表现也各不相同。比如 Redis 是完全运行在内存上的，它能提供极致的性能，但运维难度较高，可靠性相对低。而 MySQL、PostgreSQL 是关系型数据库，性能不如 Redis，但运维难度不高，可靠性也有一定的保障。SQLite 是单机单文件关系型数据库，性能较低，也不适合用于大规模数据存储，但它免配置，适合单机少量数据存储的场景。相比之下，OceanBase 是一款分布式关系型数据库，能够在提供高性能的同时，确保数据的一致性和高可靠性（RTO < 8s）。它特别适合金融、零售、电信等对事务一致性和分布式能力要求较高的场景，使 JuiceFS 在处理海量元数据时可以实现更高效率、更低延迟和更强稳定性，从而满足现代分布式存储系统对底层数据库的苛刻要求。

如果只是为了评估 JuiceFS 的功能，你可以在 ECS 云服务器手动搭建数据库使用。当你要在生产环境使用 JuiceFS 时，如果没有专业的数据库运维团队，阿里云的云数据库服务通常是更好的选择。

当然，如果你愿意，也可以使用其他云平台上提供的云数据库服务。但在这种情况下，你只能通过公网访问云数据库，也就是说，你必须向公网暴露数据库的端口，这存在极大的安全风险，最好不要这样使用。

如果必须通过公网访问数据库，可以通过云数据库控台提供的白名单功能，严格限制允许访问数据库的 IP 地址，从而提升数据的安全性。从另一个角度说，如果你通过公网无法成功连接云数据库，那么可以检查数据库的白名单，检查是不是该设置限制了你的访问。

|    数据库    |          Redis           |     MySQL、PostgreSQL      |         SQLite         |        OceanBase        |
| :----------: | :----------------------: | :------------------------: | :--------------------: | :---------------------: |
|   **性能**   |            强            |            适中            |           弱           |           强            |
| **运维门槛** |            高            |            适中            |           低           |           适中          |
|  **可靠性**  |            低            |            适中            |           低           |           高            |
| **应用场景** | 海量数据、分布式高频读写 | 海量数据、分布式中低频读写 | 少量数据单机中低频读写 | 分布式场景、强事务一致性、高可靠性要求 |

**本文使用了[云数据 Redis 版](https://www.aliyun.com/product/kvstore)，以下连接地址只是为了演示目的编制的伪地址：**

| Redis 版本   | 5.0 社区版                             |
|--------------|----------------------------------------|
| **实例规格** | 256M 标准版 - 单副本                   |
| **连接地址** | `herald-sh-abc.redis.rds.aliyuncs.com` |
| **可用区**   | 上海                                   |

### 三、对象存储 OSS

JuiceFS 会将所有的数据都存储到对象存储中，它支持几乎所有的对象存储服务。但为了获得最佳的性能，当使用阿里云 ECS 时，搭配阿里云 OSS 对象存储通常是最优选择。不过请注意，将 ECS 和 OSS Bucket 选择在相同的地区，这样才能通过阿里云的内网线路进行访问，不但延时低，而且不需要额外的流量费用。

当然，如果你愿意，也可以使用其他云平台提供的对象存储服务，但不推荐这样做。首先，通过阿里云 ECS 访问其他云平台的对象存储要走公网线路，对象存储会产生流量费用，而且这样的访问延时相比也会更高，可能会影响 JuiceFS 的性能发挥。

阿里云 OSS 有不同的存储级别，由于 JuiceFS 需要与对象存储频繁交互，建议使用标准存储。你可以搭配 OSS 资源包使用，降低对象存储的使用成本。

### API 访问秘钥

阿里云 OSS 需要通过 API 进行访问，你需要准备访问秘钥，包括  `Access Key ID` 和 `Access Key Secret` ，[点此查看](https://help.aliyun.com/document_detail/38738.html)获取方式。

> **安全建议**：显式使用 API 访问秘钥可能导致密钥泄露，推荐为云服务器分配 [RAM 服务角色](https://help.aliyun.com/document_detail/93689.htm)。当一台 ECS 被授予 OSS 操作权限以后，无需使用 API 访问秘钥即可访问 OSS。

## 安装

当前使用的是 Ubuntu Server 20.04 64 位系统，依次执行以下命令可以安装最新版本客户端。

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

你也可以访问 [JuiceFS GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面选择其他版本。

执行命令，看到返回 `juicefs` 的命令帮助信息，代表客户端安装成功。

```shell
$ juicefs
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.15.2 (2021-07-07T05:51:36Z 4c16847)

COMMANDS:
   format   format a volume
   mount    mount a volume
   umount   unmount a volume
   gateway  S3-compatible gateway
   sync     sync between two storage
   rmr      remove directories recursively
   info     show internal information for paths or inodes
   bench    run benchmark to read/write/stat big/small files
   gc       collect any leaked objects
   fsck     Check consistency of file system
   profile  analyze access log
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump metadata into a JSON file
   load     load metadata from a previously dumped JSON file
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              disable pprof (:6060) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   Apache License 2.0
```

JuiceFS 具有良好的跨平台兼容性，同时支持在 Linux、Windows 和 macOS 上使用。本文着重介绍 JuiceFS 在 Linux 系统上的安装和使用，如果你需要了解其他系统上的安装方法，请[查阅文档](../getting-started/installation.md)。

## 创建 JuiceFS 存储

JuiceFS 客户端安装好以后，现在就可以使用前面准备好的 Redis 数据库和 OSS 对象存储来创建 JuiceFS 存储了。

严格意义上说，这一步操作应该叫做“Format a volume”，即格式化一个卷。但考虑到有很多用户可能不了解或者不关心文件系统的标准术语，所以简单起见，我们就直白的把这个过程叫做“创建 JuiceFS 存储”。

以下命令使用 JuiceFS 客户端提供的 `format` 子命令创建了一个名为 `mystor` 的存储，即文件系统：

```shell
$ juicefs format \
    --storage oss \
    --bucket https://<your-bucket-name> \
    --access-key <your-access-key-id> \
    --secret-key <your-access-key-secret> \
    redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 \
    mystor
```

**选项说明：**

- `--storage`：指定对象存储类型，[点此查看](../reference/how_to_set_up_object_storage.md#supported-object-storage) JuiceFS 支持的对象存储。
- `--bucket`：对象存储的 Bucket 域名。当使用阿里云 OSS 时，只需填写 bucket 名称即可，无需填写完整的域名，JuiceFS 会自动识别并补全地址。
- `--access-key` 和 `--secret-key`：访问对象存储 API 的秘钥对，[点此查看](https://help.aliyun.com/document_detail/38738.html)获取方式。

> Redis 6.0 身份认证需要用户名和密码两个参数，地址格式为 `redis://username:password@redis-server-url:6379/1`。目前阿里云数据库 Redis 版只提供 Reids 4.0 和 5.0 两个版本，认证身份只需要密码，在设置 Redis 服务器地址时只需留空用户名即可，例如：`redis://:password@redis-server-url:6379/1`

使用 RAM 角色绑定 ECS 时，创建 JuiceFS 存储只需指定 `--storage` 和  `--bucket` 两个选项，无需提供 API 访问秘钥。命令可以改写成：

```shell
$ juicefs format \
    --storage oss \
    --bucket https://mytest.oss-cn-shanghai.aliyuncs.com \
    redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 \
    mystor
```

看到类似下面的输出，代表文件系统创建成功了。

```shell
2021/07/13 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com:6379/1
2021/07/13 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/07/13 16:37:14.527879 juicefs[22290] <INFO>: Data uses oss://mytest/mystor/
2021/07/13 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:mystor UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:oss Bucket:https://mytest340 AccessKey:LTAI4G4v6ioGzQXy56m3XDkG SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

## 挂载 JuiceFS 存储

文件系统创建完成，对象存储相关的信息会被存入数据库，挂载时无需再输入对象存储的 Bucket 和秘钥等信息。

使用 `mount` 子命令，将文件系统挂载到 `/mnt/jfs` 目录：

```shell
sudo juicefs mount -d redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 /mnt/jfs
```

> **注意**：挂载文件系统时，只需填写 Redis 数据库地址，不需要文件系统名称。默认的缓存路径为 `/var/jfsCache`，请确保当前用户有足够的读写权限。

看到类似下面的输出，代表文件系统挂载成功。

```shell
2021/07/13 16:40:37.088847 juicefs[22307] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com/1
2021/07/13 16:40:37.101279 juicefs[22307] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:40:37.104870 juicefs[22307] <INFO>: Ping redis: 3.408807ms
2021/07/13 16:40:37.384977 juicefs[22307] <INFO>: Data use oss://mytest/mystor/
2021/07/13 16:40:37.387412 juicefs[22307] <INFO>: Disk cache (/var/jfsCache/4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b/): capacity (1024 MB), free ratio (10%), max pending pages (15)
.2021/07/13 16:40:38.410742 juicefs[22307] <INFO>: OK, mystor is ready at /mnt/jfs
```

使用 `df` 命令，可以看到文件系统的挂载情况：

```shell
$ df -Th
文件系统           类型          容量   已用  可用   已用% 挂载点
JuiceFS:mystor   fuse.juicefs  1.0P   64K  1.0P    1% /mnt/jfs
```

文件系统挂载成功以后，现在就可以像使用本地硬盘那样，在 `/mnt/jfs` 目录中存储数据了。

> **多主机共享**：JuiceFS 存储支持被多台云服务器同时挂载使用，你可以在其他 ECS 上安装 JuiceFS 客户端，然后使用 `redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1` 数据库地址挂载文件系统到每一台主机上。

## 查看文件系统状态

使用 JuiceFS 客户端的 `status` 子命令可以查看一个文件系统的基本信息和连接状态。

```shell
$ juicefs status redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1

2021/07/13 16:56:17.143503 juicefs[22415] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com:6379/1
2021/07/13 16:56:17.157972 juicefs[22415] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:56:17.161533 juicefs[22415] <INFO>: Ping redis: 3.392906ms
{
  "Setting": {
    "Name": "mystor",
    "UUID": "4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b",
    "Storage": "oss",
    "Bucket": "https://mytest",
    "AccessKey": "<your-access-key-id>",
    "BlockSize": 4096,
    "Compression": "none",
    "Shards": 0,
    "Partitions": 0,
    "Capacity": 0,
    "Inodes": 0
  },
  "Sessions": [
    {
      "Sid": 3,
      "Heartbeat": "2021-07-13T16:55:38+08:00",
      "Version": "0.15.2 (2021-07-07T05:51:36Z 4c16847)",
      "Hostname": "demo-test-sh",
      "MountPoint": "/mnt/jfs",
      "ProcessID": 22330
    }
  ]
}
```

## 卸载 JuiceFS 存储

使用 JuiceFS 客户端提供的 `umount` 命令即可卸载文件系统，比如：

```shell
sudo juicefs umount /mnt/jfs
```

> **注意**：强制卸载使用中的文件系统可能导致数据损坏或丢失，请务必谨慎操作。

## 开机自动挂载

请参考[「启动时自动挂载 JuiceFS」](../administration/mount_at_boot.md)
