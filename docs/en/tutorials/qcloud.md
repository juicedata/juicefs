---
title: Use JuiceFS on Tencent Cloud
sidebar_position: 8
slug: /clouds/qcloud
---

JuiceFS needs to be used with database and object storage together. Here we directly use Tencent Cloud's CVM cloud server, combined with cloud database and COS object storage.

## Preparation

When creating cloud computing resources, try to choose the same region, so that resources can access each other through intranet and avoid extra traffic costs by using public network.

### 1. CVM

JuiceFS has no special requirements for server hardware, and the minimum specification of CVM can use JuiceFS stably, usually you just need to choose the configuration that can meet your business.

In particular, you do not need to buy a new server or reinstall the system to use JuiceFS, JuiceFS is not business invasive and will not cause any interference with your existing systems and programs, you can install and use JuiceFS on your running server.

By default, JuiceFS takes up 1GB of hard disk space for caching, and you can adjust the size of the cache space as needed. This cache is a data buffer layer between the client and the object storage, and you can get better performance by choosing a cloud drive with better performance.

JuiceFS can be installed on all operating systems provided by Tencent Cloud CVM.

**The specifications of CVM used in this article are as follows:**

| Server Specifications |                          |
| --------------------- | ------------------------ |
| **CPU**               | 1 Core                   |
| **RAM**               | 2 GB                     |
| **Storage**           | 50 GB                    |
| **OS**                | Ubuntu Server 20.04 64-bit |
| **Location**          | Shanghai 5               |

### 2. Database

JuiceFS will store all the metadata corresponding to the data in a separate database, and the supported databases are Redis, MySQL, PostgreSQL, TiKV and SQLite.

Depending on the database type, the performance and reliability of metadata varies. For example, Redis runs entirely on memory, which provides the ultimate performance, but is difficult to operate and maintain, and has relatively low reliability. SQLite is a single-file relational database with low performance and is not suitable for large-scale data storage, but it is configuration-free and suitable for scenarios with small amounts of data storage.

If you are just evaluating the capabilities of JuiceFS, you can manually build the database for use in the CVM. When you want to use JuiceFS in a production environment, the cloud database service of Tencent Cloud is usually a better choice if you don't have a professional database operation and maintenance team.

Of course, you can also use cloud database services provided on other cloud platforms if you wish.However, in this case, you can only access the cloud database through the public network, which means that you must expose the database port to the public network, which has some security risks and requires special attention.

If you must access the database through the public network, you can enhance the security of your data by strictly limiting the IP addresses that are allowed to access the database through the whitelist feature provided by the cloud database console. On the other hand, if you cannot connect to the cloud database through the public network, then you can check the whitelist of the database.

|    Database     |                          Redis                          |                      MySQL/PostgreSQL                       |                            SQLite                            |
| :-------------: | :-----------------------------------------------------: | :----------------------------------------------------------: | :----------------------------------------------------------: |
| **Performance** |                          High                           |                            Medium                            |                             Low                              |
| **Management**  |                          High                           |                            Medium                            |                             Low                              |
| **Reliability** |                           Low                           |                            Medium                            |                             Low                              |
|  **Scenario**   | Massive data, distributed high-frequency read and write | Massive data, distributed low and medium frequency read and write | Low frequency read and write in single machine for small amount of data |

**This article uses the TencentDB for Redis, which is accessed through a VPC private network interacting with the CVM:**

| Redis version               | 5.0 community edition                      |
| --------------------------- | ------------------------------------------ |
| **Instance Specification**  | 1GB Memory Edition (standard architecture) |
| **Connection Address**      | 192.168.5.5:6379                           |
| **Available Zone**          | Shanghai 5                                 |

Note that the database connection address depends on the VPC network settings you create, and that creating a Redis instance automatically gets the address in the network segment you define.

### 3. Object Storage COS

JuiceFS stores all data in object storage, and it supports almost all object storage services. However, for the best performance, when using Tencent Cloud CVM, pairing it with Tencent Cloud COS Object Storage is usually the optimal choice. However, please note that selecting CVM and COS Bucket in the same region so that they can be accessed through Tencent Cloud's intranet not only has low latency, but also does not require additional traffic costs.

> **Hint**: The unique access address provided by Tencent Cloud COS supports both intranet and extranet access. When accessing through the intranet, COS will automatically resolve to the intranet IP, and the traffic generated at this time is all intranet traffic, which will not incur traffic costs.

Of course, if you want, you can also use object storage services provided by other cloud platforms, but it is not recommended to do so. First of all, if you access the object storage of other cloud platforms through Tencent Cloud CVM, you have to take the public network, and the object storage will incur traffic costs, and the access latency will be higher compared to this, which may affect the performance of JuiceFS.

Tencent Cloud COS has different storage levels, and since JuiceFS needs to interact with object storage frequently, it is recommended to use standard storage. You can use it with COS resource package to reduce the cost.

### API Access Secret Key

Tencent Cloud COS needs to be accessed through API, you need to prepare the access secret key, including `Access Key ID` and `Access Key Secret`, [click here to view](https://intl.cloud.tencent.com/document/product/598/32675) to get the way.

> **Security Advisory**: Explicit use of the API access secret key may lead to key compromise and it is recommended to assign [CAM Service Role](https://intl.cloud.tencent.com/document/product/598/19420) to the cloud server. Once a CVM has been granted COS operation privileges, it can access the COS without using the API access key.

## Installation

Here we are using Ubuntu Server 20.04 64-bit system, and the latest version of the client can be installed by running the following command.

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

You can also choose another version by visiting the [JuiceFS GitHub Releases](https://github.com/juicedata/juicefs/releases) page.

Execute the command and see the help message `juicefs` returned, which means the client installation is successful.

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

JuiceFS has good cross-platform compatibility and is supported on Linux, Windows and macOS. This article focuses on the installation and use of JuiceFS on Linux, if you need to know how to install it on other systems, please [check the documentation](../getting-started/installation.md).

## Creating JuiceFS

Once the JuiceFS client is installed, you can now create the JuiceFS storage using the Redis database and COS you prepared earlier.

Technically speaking, this step should be called "Format a volume". However, since many users may not understand or care about the standard file system terminology, we will simply call the process "Create JuiceFS Storage".

The following command creates a storage called `mystor`, i.e., a file system, using the `format` subcommand provided by the JuiceFS client.

```shell
$ juicefs format \
    --storage cos \
    --bucket https://<your-bucket-name> \
    --access-key <your-access-key-id> \
    --secret-key <your-access-key-secret> \
    redis://:<your-redis-password>@192.168.5.5:6379/1 \
    mystor
```

**Option description:**

- `--storage`: Specify the type of object storage.
- `---bucket`: Bucket access domain of the object store, which can be found in the COS management console.
- `--access-key` and `--secret-key`: the secret key pair for accessing the Object Storage API, [click here to view](https://intl.cloud.tencent.com/document/product/598/32675) to get it.

> Redis 6.0 authentication requires two parameters, username and password, and the address format is `redis://username:password@redis-server-url:6379/1`. Currently, the Redis version of Tencent Cloud Database only provides Reids 4.0 and 5.0, which only requires a password for authentication. When setting the Redis server address, you only need to leave the username empty, for example: `redis://:password@redis-server-url:6379/1`

Output like the following means the file system was created successfully.

```shell
2021/07/30 11:44:31.904157 juicefs[44060] <INFO>: Meta address: redis://@192.168.5.5:6379/1
2021/07/30 11:44:31.907083 juicefs[44060] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/07/30 11:44:31.907634 juicefs[44060] <INFO>: Ping redis: 474.98µs
2021/07/30 11:44:31.907850 juicefs[44060] <INFO>: Data uses cos://juice-0000000000/mystor/
2021/07/30 11:44:32.149692 juicefs[44060] <INFO>: Volume is formatted as {Name:mystor UUID:dbf05314-57af-4a2c-8ac1-19329d73170c Storage:cos Bucket:https://juice-0000000000.cos.ap-shanghai.myqcloud.com AccessKey:AKIDGLxxxxxxxxxxxxxxxxxxZ8QRBdpkOkp SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

## Mount JuiceFS

When the file system is created, the information related to the object storage is stored in the database, so there is no need to enter information such as the bucket domain and secret key when mounting.

Use the `mount` subcommand to mount the file system to the `/mnt/jfs` directory.

```shell
sudo juicefs mount -d redis://:<your-redis-password>@192.168.5.5:6379/1 /mnt/jfs
```

> **Note**: When mounting the file system, only the Redis database address is required, not the file system name. The default cache path is `/var/jfsCache`, please make sure the current user has enough read/write permissions.

Output similar to the following means that the file system was mounted successfully.

```shell
2021/07/30 11:49:56.842211 juicefs[44175] <INFO>: Meta address: redis://@192.168.5.5:6379/1
2021/07/30 11:49:56.845100 juicefs[44175] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/07/30 11:49:56.845562 juicefs[44175] <INFO>: Ping redis: 383.157µs
2021/07/30 11:49:56.846164 juicefs[44175] <INFO>: Data use cos://juice-0000000000/mystor/
2021/07/30 11:49:56.846731 juicefs[44175] <INFO>: Disk cache (/var/jfsCache/dbf05314-57af-4a2c-8ac1-19329d73170c/): capacity (1024 MB), free ratio (10%), max pending pages (15)
2021/07/30 11:49:57.354763 juicefs[44175] <INFO>: OK, mystor is ready at /mnt/jfs
```

Using the `df` command, you can see how the file system is mounted.

```shell
$ df -Th
File system      type         capacity used usable used%  mount point
JuiceFS:mystor   fuse.juicefs  1.0P     64K  1.0P    1%   /mnt/jfs
```

After the file system is successfully mounted, you can now store data in the `/mnt/jfs` directory as if you were using a local hard drive.

> **Multi-Host Sharing**: JuiceFS storage supports being mounted by multiple cloud servers at the same time. You can install the JuiceFS client on other could server and then use `redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs. com:6379/1` database address to mount the file system on each host.

## File System Status

Use the `status` subcommand of the JuiceFS client to view basic information and connection status of a file system.

```shell
$ juicefs status redis://:<your-redis-password>@192.168.5.5:6379/1

2021/07/30 11:51:17.864767 juicefs[44196] <INFO>: Meta address: redis://@192.168.5.5:6379/1
2021/07/30 11:51:17.866619 juicefs[44196] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/07/30 11:51:17.867092 juicefs[44196] <INFO>: Ping redis: 379.391µs
{
  "Setting": {
    "Name": "mystor",
    "UUID": "dbf05314-57af-4a2c-8ac1-19329d73170c",
    "Storage": "cos",
    "Bucket": "https://juice-0000000000.cos.ap-shanghai.myqcloud.com",
    "AccessKey": "AKIDGLxxxxxxxxxxxxxxxxx8QRBdpkOkp",
    "BlockSize": 4096,
    "Compression": "none",
    "Shards": 0,
    "Partitions": 0,
    "Capacity": 0,
    "Inodes": 0
  },
  "Sessions": [
    {
      "Sid": 1,
      "Heartbeat": "2021-07-30T11:49:56+08:00",
      "Version": "0.15.2 (2021-07-07T05:51:36Z 4c16847)",
      "Hostname": "VM-5-6-ubuntu",
      "MountPoint": "/mnt/jfs",
      "ProcessID": 44175
    },
    {
      "Sid": 3,
      "Heartbeat": "2021-07-30T11:50:56+08:00",
      "Version": "0.15.2 (2021-07-07T05:51:36Z 4c16847)",
      "Hostname": "VM-5-6-ubuntu",
      "MountPoint": "/mnt/jfs",
      "ProcessID": 44185
    }
  ]
}
```

## Unmount JuiceFS

The file system can be unmounted using the `umount` command provided by the JuiceFS client, e.g.

```shell
sudo juicefs umount /mnt/jfs
```

> **Note**: Forced unmount of the file system in use may result in data corruption or loss, so please be sure to proceed with caution.

## Auto-mount on boot

Please refer to ["Mount JuiceFS at Boot Time"](../administration/mount_at_boot.md) for more details.
