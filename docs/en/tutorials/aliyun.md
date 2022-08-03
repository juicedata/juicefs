---
sidebar_label: Use JuiceFS on Alibaba Cloud
sidebar_position: 6
slug: /clouds/aliyun
---
# Use JuiceFS on Alibaba Cloud

As shown in the figure below, JuiceFS is driven by both the database and the object storage. The files stored in JuiceFS are split into fixed-size data blocks and stored in the object store according to certain rules, while the metadata corresponding to the data is stored in the database.

The metadata is stored completely independently, and the retrieval and processing of files does not directly manipulate the data in the object storage, but first manipulates the metadata in the database, and only interacts with the object storage when the data changes.

This design can effectively reduce the cost of the object storage in terms of the number of requests, but also allows us to significantly experience the performance improvement brought by JuiceFS.

![](../images/juicefs-arch-new.png)

## Preparation

From the previous architecture description, you can know that JuiceFS needs to be used together with database and object storage. Here we directly use Alibaba Cloud ECS cloud server, combined with cloud database and OSS object storage.

When you create cloud computing resources, try to choose in the same region, so that resources can access each other through intranet and avoid using public network to incur additional traffic costs.

### 1. ECS
JuiceFS has no special requirements for server hardware, generally speaking, entry-level cloud servers can also use JuiceFS stably, usually you just need to choose the one that can meet your own business.

In particular, you do not need to buy a new server or reinstall the system to use JuiceFS, JuiceFS is not business invasive and will not cause any interference with your existing systems and programs, you can install and use JuiceFS on your running server.

By default, JuiceFS takes up 1GB of hard disk space for caching, and you can adjust the size of the cache space as needed. This cache is a data buffer layer between the client and the object storage, and you can get better performance by choosing a cloud drive with better performance.

In terms of operating system, JuiceFS can be installed on all operating systems provided by Alibaba Cloud ECS.

**The ECS specification used in this document are as follows:**

| **Instance Specification** | ecs.t5-lc1m1.small         |
| -------------------------- | -------------------------- |
| **CPU**                    | 1 core                     |
| **MEMORY**                 | 1 GB                       |
| **Storage**                | 40 GB                      |
| **OS**                     | Ubuntu Server 20.04 64-bit |
| **Location**               | Shanghai                   |

### 2. Cloud Database

JuiceFS will store all the metadata corresponding to the data in a separate database, which is currently support Redis, MySQL, PostgreSQL and SQLite.

Depending on the database type, the performance and reliability of metadata are different.  For example, Redis runs entirely on memory, which provides the ultimate performance, but is difficult to operate and maintain, and has relatively low reliability. SQLite is a single-file relational database with low performance and is not suitable for large-scale data storage, but it is configuration-free and suitable for a small amount of data storage on a single machine.

If you just want to evaluate the functionality of JuiceFS, you can build the database manually on ECS. When you want to use JucieFS in a production environment, the cloud database service is usually a better choice if you don't have a professional database operation and maintenance team.

Of course, you can also use cloud database services provided on other platforms if you wish.But in this case, you have to expose the database port to the public network, which also has some security risks.

If you must access the database through the public network, you can enhance the security of your data by strictly limiting the IP addresses that are allowed to access the database through the whitelist feature provided by the cloud database console.

On the other hand, if you cannot successfully connect to the cloud database through the public network, then you can check the whitelist of the database.

|    Database     |                          Redis                          |                      MySQL/PostgreSQL                       |                            SQLite                            |
| :-------------: | :-----------------------------------------------------: | :----------------------------------------------------------: | :----------------------------------------------------------: |
| **Performance** |                          High                           |                            Medium                            |                             Low                              |
| **Management**  |                          High                           |                            Medium                            |                             Low                              |
| **Reliability** |                           Low                           |                            Medium                            |                             Low                              |
|  **Scenario**   | Massive data, distributed high-frequency read and write | Massive data, distributed low and medium frequency read and write | Low frequency read and write in single machine for small amount of data |

**This article uses the [ApsaraDB for Redis](https://www.alibabacloud.com/product/apsaradb-for-redis), and the following is pseudo address compiled for demonstration purposes only:**

| Redis Version               | 5.0 Community Edition                  |
| --------------------------- | -------------------------------------- |
| **Instance Specification**  | 256M Standard master-replica instances |
| **Connection Address**      | herald-sh-abc.redis.rds.aliyuncs.com   |
| **Available Zone**          | Shanghai                               |

### 3. Object Storage OSS

JuiceFS will store all the data in object storage, which supports almost all object storage services. However, to get the best performance, when using Alibaba Cloud ECS, with OSS object storage is usually the optimal choice. However, please note that choosing ECS and OSS Bucket in the same region so that they can be accessed through intranet not only has low latency, but also does not require additional traffic costs.

Of course, you can also use object storage services provided by other cloud platforms if you wish, but this is not recommended. First of all, accessing object storage from other cloud platforms through ECS has to take public network, and object storage will incur traffic costs, and the access latency will be higher compared to this, which may affect the performance of JuiceFS.

Alibaba Cloud OSS has different storage levels, and since JuiceFS needs to interact with object storage frequently, it is recommended to use standard tier. You can use it with OSS resource pack to reduce the cost of using object storage.

### API access secret key

Alibaba Cloud OSS needs to be accessed through API, you need to prepare the access secret key, including `Access Key ID` and `Access Key Secret`, [click here](https://www.alibabacloud.com/help/doc-detail/125558.htm) to get the way.

> **Security Advisory**: Explicit use of the API access secret key may lead to key compromise, it is recommended to assign [RAM Role](https://www.alibabacloud.com/help/doc-detail/110376.htm) to the cloud server. Once an ECS has been granted access to the OSS, the API access key is not required to access the OSS.

## Installation

We currently using Ubuntu Server 20.04 64-bit, so you can download the latest version of the client by running the following commands. You can also choose another version by visiting the [JuiceFS GitHub Releases](https://github.com/juicedata/juicefs/releases) page.

```shell
JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
```

```shell
wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
```

After downloading, unzip the program into the `juice` folder.

```shell
mkdir juice && tar -zxvf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" -C juice
```

Install the JuiceFS client to `/usr/local/bin` :

```shell
sudo install juice/juicefs /usr/local/bin
```

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
   --no-agent              Disable pprof (:6060) and gops (:6070) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   Apache License 2.0
```

JuiceFS has good cross-platform compatibility and is supported on Linux, Windows and macOS. This article focuses on the installation and use of JuiceFS on Linux, if you need to know how to install it on other systems, please [check the documentation](../getting-started/installation.md).

## Creating JuiceFS

Once the JuiceFS client is installed, you can now create the JuiceFS storage using the Redis database and OSS object storage that you prepared earlier.

Technically speaking, this step should be called "Format a volume". However, given that many users may not understand or care about the standard file system terminology, we will simply call the process "Create a JuiceFS Storage".

The following command creates a storage called `mystor`, i.e., a file system, using the `format` subcommand provided by the JuiceFS client.

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

- `--storage`: Specify the type of object storage, [click here to view](../guide/how_to_set_up_object_storage.md) object storage services supported by JuiceFS.
- `--bucket`: Bucket domain name of the object storage. When using OSS, just fill in the bucket name, no need to fill in the full domain name, JuiceFS will automatically identify and fill in the full address.
- `--access-key` and `--secret-key`: the secret key pair to access the object storage API, [click here](https://www.alibabacloud.com/help/doc-detail/125558.htm) to get the way.

> Redis 6.0 authentication requires username and password parameters in the format of `redis://username:password@redis-server-url:6379/1`. Currently, Alibaba Cloud Redis only provides Reids 4.0 and 5.0 versions, which require only a password for authentication, and just leave the username empty when setting the Redis server address, for example: `redis://:password@redis-server-url:6379/1`.

When using the RAM role to bind to the ECS, the JucieFS storage can be created by specifying `--storage` and `--bucket` without providing the API access key. The command can be rewritten as follows:

```shell
$ juicefs format \
	--storage oss \
	--bucket https://mytest.oss-cn-shanghai.aliyuncs.com \
	redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 \
	mystor
```

Output like the following means the file system was created successfully.

```shell
2021/07/13 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com:6379/1
2021/07/13 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/07/13 16:37:14.527879 juicefs[22290] <INFO>: Data uses oss://mytest/mystor/
2021/07/13 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:mystor UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:oss Bucket:https://mytest340 AccessKey:LTAI4G4v6ioGzQXy56m3XDkG SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

## Mount JuiceFS

When the file system is created, the information related to the object storage is stored in the database, so there is no need to enter information such as the bucket domain and secret key when mounting.

Use the `mount` subcommand to mount the file system to the `/mnt/jfs` directory.

```shell
sudo juicefs mount -d redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1 /mnt/jfs
```

> **Note**: When mounting the file system, only the Redis database address is required, not the file system name. The default cache path is `/var/jfsCache`, please make sure the current user has enough read/write permissions.

Output similar to the following means that the file system was mounted successfully.

```shell
2021/07/13 16:40:37.088847 juicefs[22307] <INFO>: Meta address: redis://@herald-sh-abc.redis.rds.aliyuncs.com/1
2021/07/13 16:40:37.101279 juicefs[22307] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/07/13 16:40:37.104870 juicefs[22307] <INFO>: Ping redis: 3.408807ms
2021/07/13 16:40:37.384977 juicefs[22307] <INFO>: Data use oss://mytest/mystor/
2021/07/13 16:40:37.387412 juicefs[22307] <INFO>: Disk cache (/var/jfsCache/4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b/): capacity (1024 MB), free ratio (10%), max pending pages (15)
.2021/07/13 16:40:38.410742 juicefs[22307] <INFO>: OK, mystor is ready at /mnt/jfs
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

The file system can be unmounted using the `umount` command provided by the JuiceFS client, e.g.

```shell
sudo juicefs umount /mnt/jfs
```

> **Note**: Forced unmount of the file system in use may result in data corruption or loss, so please be sure to proceed with caution.

## Auto-mount on Boot

If you don't want to manually remount JuiceFS storage on reboot, you can set up automatic mounting of the file system.

First, you need to rename the `juicefs` client to `mount.juicefs` and copy it to the `/sbin/` directory.

```shell
sudo cp juice/juicefs /sbin/mount.juicefs
```

Edit the `/etc/fstab` configuration file and add a new record.

```shell
redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1    /mnt/jfs       juicefs     _netdev,cache-size=20480     0  0
```

The mount option `cache-size=20480` means to allocate 20GB local disk space for JuiceFS cache, please decide the allocated cache size according to your actual ECS hard disk capacity. In general, allocating more cache space for JuiceFS will result in better performance.

You can adjust the FUSE mount options in the above configuration as needed, for more information please [check the documentation](../reference/fuse_mount_options.md).

> **Note**: Please replace the Redis address, mount point, and mount options in the above configuration file with your actual information.
