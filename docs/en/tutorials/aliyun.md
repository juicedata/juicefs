---
title: Use JuiceFS on Alibaba Cloud
sidebar_position: 7
slug: /clouds/aliyun
description: Learn how to use JuiceFS on Alibaba Cloud.
---

As shown in the figure below, JuiceFS is driven by both the database and the object storage. The files stored in JuiceFS are split into fixed-size data blocks and stored in the object store according to certain rules, while the metadata corresponding to the data is stored in the database.

The metadata is stored completely independently. Retrieval and processing of files do not directly manipulate the data in the object storage. Instead, operations are performed first on the metadata in the database. Interaction with the object storage only occurs when data changes.

This design can effectively reduce the cost of the object storage in terms of the number of requests. It also allows users to significantly experience the performance improvement brought by JuiceFS.

![JuiceFS-arch-new](../images/juicefs-aliyun.png)

This document introduces how to use JuiceFS on Alibaba Cloud.

## Preparation

From the previous architecture description, you can know that JuiceFS needs to be used together with database and object storage. Here we directly use the Alibaba Cloud ECS cloud server, combined with cloud database and OSS object storage.

When you create cloud computing resources, try to choose in the same region, so that resources can access each other through intranet and avoid using public network to incur additional traffic costs.

### ECS

JuiceFS has no special requirements for server hardware. Generally speaking, entry-level cloud servers can also use JuiceFS stably. Typically, you just need to choose the one that can meet your own application requirements.

In particular, you do not need to buy a new server or reinstall the system to use JuiceFS. JuiceFS is not application invasive and does not cause any interference with your existing systems and programs. You can install and use JuiceFS on your running server.

By default, JuiceFS takes up 1 GB of hard disk space for caching, and you can adjust the size of the cache space as needed. This cache is a data buffer layer between the client and the object storage. You can get better performance by choosing a cloud drive with better performance.

In terms of operating system, JuiceFS can be installed on all operating systems provided by Alibaba Cloud ECS.

**The ECS specification used in this document are as follows:**

| **Instance specification** | ecs.t5-lc1m1.small         |
| -------------------------- | -------------------------- |
| **CPU**                    | 1 core                     |
| **MEMORY**                 | 1 GB                       |
| **Storage**                | 40 GB                      |
| **OS**                     | Ubuntu Server 20.04 64-bit |
| **Location**               | Shanghai                   |

### Cloud database

JuiceFS stores all the metadata corresponding to the data in a separate database, which currently supports Redis, MySQL, PostgreSQL, SQLite, and OceanBase.

Depending on the database type, the performance and reliability of metadata are different. For example, Redis runs entirely in memory. While it provides the ultimate performance, it is difficult to operate and maintain and has low reliability. SQLite is a single-file relational database with low performance and is not suitable for large-scale data storage. However, it is configuration-free and suitable for a small amount of data storage on a single machine. In contrast, OceanBase is a distributed relational database that delivers high performance while ensuring data consistency and high reliability (RTO < 8 seconds). It is particularly well-suited for scenarios in industries such as finance, retail, and telecommunications, where transactional consistency and distributed capabilities are critical. By integrating with JuiceFS, OceanBase enhances the efficiency, reduces the latency, and improves the stability of handling massive metadata, meeting the demanding requirements of modern distributed storage systems for underlying databases.

If you just want to evaluate the functionality of JuiceFS, you can build the database manually on ECS. If you want to use JuiceFS in a production environment, and you don't have a professional database operation and maintenance team, the cloud database service is usually a better choice.

You can also use cloud database services provided on other platforms if you wish. But in this case, you have to expose the database port to the public network, which may have some security risks.

If you must access the database through the public network, you can enhance the security of your data by strictly limiting the IP addresses that are allowed to access the database through the whitelist feature provided by the cloud database console.

On the other hand, if you cannot successfully connect to the cloud database through the public network, you can check the whitelist of the database.

|    Database     |                          Redis                          |                      MySQL/PostgreSQL                       |                            SQLite                            |                          OceanBase                          |
| :-------------: | :-----------------------------------------------------: | :----------------------------------------------------------: | :----------------------------------------------------------: | :----------------------------------------------------------: |
| **Performance** |                          High                           |                            Medium                            |                             Low                              |                          High                           |
| **Management**  |                          High                           |                            Medium                            |                             Low                              |                            Medium                            |
| **Reliability** |                           Low                           |                            Medium                            |                             Low                              |                          High                           |
|  **Scenario**   | Massive data, distributed high-frequency reads and writes | Massive data, distributed low- and medium-frequency reads and writes | Low-frequency reads and writes in single machine for small amounts of data | Distributed scenarios, strong transaction consistency, and high reliability requirements |

**This document uses [ApsaraDB for Redis](https://www.alibabacloud.com/product/apsaradb-for-redis), and the following pseudo address is compiled for demonstration purposes only:**

| Redis version              | 5.0 Community Edition                  |
|----------------------------|----------------------------------------|
| **Instance specification** | 256M Standard master-replica instances |
| **Connection address**     | `herald-sh-abc.redis.rds.aliyuncs.com` |
| **Available zone**         | Shanghai                               |

### Object Storage OSS

JuiceFS stores all data in object storage, which supports almost all object storage services. However, to get the best performance, when using Alibaba Cloud ECS, OSS object storage is usually the optimal choice. However, you must choose ECS and OSS buckets in the same region so that they can be accessed through intranet. This has low latency and does not require additional traffic costs.

You can also use object storage services provided by other cloud platforms if you wish, but this is not recommended. This is because accessing object storage from other cloud platforms through ECS needs the public network, and object storage will incur traffic costs. In addition, the access latency will be higher compared to this, which may affect the performance of JuiceFS.

Alibaba Cloud OSS has different storage levels. Since JuiceFS needs to interact with object storage frequently, it is recommended to use standard tier. You can use it with OSS resource pack to reduce the cost of using object storage.

### API access secret key

Alibaba Cloud OSS needs to be accessed through an API. You need to prepare an access key pair, including an AccessKey ID and an AccessKey secret. [Click here](https://www.alibabacloud.com/help/doc-detail/125558.htm) to see how to obtain the access key pair.

> **Security advisory**: Explicit use of the API access secret key may lead to key compromise. It is recommended to assign a [RAM role](https://www.alibabacloud.com/help/doc-detail/110376.htm) to the cloud server. Once an ECS is granted access to the OSS, the API access key is no longer required to access the OSS.

## Installation

We are currently using Ubuntu Server 20.04 64-bit, so you can download the latest version of the client by running the following command:

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

Alternatively, you can choose another version by visiting the [JuiceFS GitHub Releases](https://github.com/juicedata/juicefs/releases) page.

Execute the command, and you will see the help message returned by JuiceFS. This means that the client installation was successful.

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

JuiceFS has good cross-platform compatibility and supports Linux, Windows, and macOS. This document focuses on installing and using JuiceFS on Linux. For installation instructions on other systems, [check this document](../getting-started/installation.md).

## Create JuiceFS storage

Once the JuiceFS client is installed, you can create the JuiceFS storage using the Redis database and OSS object storage that you prepared earlier.

Technically speaking, this step should be called "Format a volume." However, given that many users may not understand or care about the standard file system terminology, we will refer to the process simply as "Create JuiceFS storage."

The following command creates a storage named `mystor`, which is a file system, using the `format` subcommand provided by the JuiceFS client:

```shell
$ juicefs format \
    --storage oss \
    --bucket https://<your-bucket-name> \
    --access-key <your-access-key-id> \
    --secret-key <your-access-key-secret> \
    redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 \
    mystor
```

**Option description:**

- `--storage`: Specifies the type of object storage. [Click here](../reference/how_to_set_up_object_storage.md) to view the object storage services supported by JuiceFS.
- `--bucket`: Bucket domain name of the object storage. When using OSS, just fill in the bucket name. There is no need to fill in the full domain name. JuiceFS will automatically identify and fill in the complete address.
- `--access-key` and `--secret-key`: The secret key pair to access the object storage API. [Click here](https://www.alibabacloud.com/help/doc-detail/125558.htm) for instructions on obtaining these keys.

> Redis 6.0 authentication requires username and password parameters in the format of `redis://username:password@redis-server-url:6379/1`. Currently, Alibaba Cloud Redis only provides Reids 4.0 and 5.0 versions, which require only a password for authentication. When setting the Redis server address, leave the username empty, like this: `redis://:password@redis-server-url:6379/1`.

When you are using the RAM role to bind to the ECS, you can create JuiceFS storage by specifying `--storage` and `--bucket` without providing the API access key. The command can be rewritten as follows:

```shell
$ juicefs format \
    --storage oss \
    --bucket https://mytest.oss-cn-shanghai.aliyuncs.com \
    redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 \
    mystor
```

A successful creation of the file system will yield output similar to the following:

```shell
2021/07/13 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com:6379/1
2021/07/13 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/07/13 16:37:14.527879 juicefs[22290] <INFO>: Data uses oss://mytest/mystor/
2021/07/13 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:mystor UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:oss Bucket:https://mytest340 AccessKey:LTAI4G4v6ioGzQXy56m3XDkG SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

## Mount JuiceFS

When the file system is created, the information related to the object storage is stored in the database. Therefore, you do not need to enter information such as the bucket domain and secret key when mounting.

Use the `mount` subcommand to mount the file system to the `/mnt/jfs` directory.

```shell
sudo juicefs mount -d redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 /mnt/jfs
```

> **Note**: When mounting the file system, only the Redis database address is required; the file system name is not necessary. The default cache path is `/var/jfsCache`. Make sure the current user has sufficient read/write permissions.

Output similar to the following means that the file system was mounted successfully:

```shell
2021/07/13 16:40:37.088847 juicefs[22307] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com/1
2021/07/13 16:40:37.101279 juicefs[22307] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:40:37.104870 juicefs[22307] <INFO>: Ping redis: 3.408807ms
2021/07/13 16:40:37.384977 juicefs[22307] <INFO>: Data use oss://mytest/mystor/
2021/07/13 16:40:37.387412 juicefs[22307] <INFO>: Disk cache (/var/jfsCache/4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b/): capacity (1024 MB), free ratio (10%), max pending pages (15)
.2021/07/13 16:40:38.410742 juicefs[22307] <INFO>: OK, mystor is ready at /mnt/jfs
```

You can use the `df` command to see how the file system is mounted:

```shell
$ df -Th
File system      type         capacity used usable used%  mount point
JuiceFS:mystor   fuse.juicefs  1.0P     64K  1.0P    1%   /mnt/jfs
```

After the file system is successfully mounted, you can store data in the `/mnt/jfs` directory as if you were using a local hard drive.

> **Multi-host sharing**: JuiceFS storage supports being mounted by multiple cloud servers at the same time. You can install the JuiceFS client on other could servers and then use the `redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs. com:6379/1` database address to mount the file system on each host.

## File system status

Use the `status` subcommand of the JuiceFS client to view basic information and connection status of a file system.

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

## Unmount JuiceFS

You can unmount the file system using the `umount` command provided by the JuiceFS client, for example:

```shell
sudo juicefs umount /mnt/jfs
```

> **Note**: Forcelly unmounting a file system in use may result in data corruption or loss. Therefore, proceed with caution.

## Auto-mount on boot

For details on auto-mounting JuiceFS at boot time, see [Mount JuiceFS at Boot Time](../administration/mount_at_boot.md).
