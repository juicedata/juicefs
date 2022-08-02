---
sidebar_label: Quick Start (Distributed Mode)
sidebar_position: 3
---

# Quick Start Guide for Distributed Mode

The previous document ["JuiceFS Quick Start Guide for Standalone Mode "](./README.md) introduces how to create a file system that can be mounted on any host by using an "object storage" and a "SQLite" database. Thanks to the feature that the object storage is accessible by any computer with privileges on the network, we can also access the same JuiceFS file system on different computers by simply copying the SQLite database file to any computer that needs to access the storage.

However, the real-time availability of the files is not guaranteed if the file system is shared by the above approach. Since SQLite is a single file database that cannot be accessed by multiple computers at the same time, a database that supports network access is needed, such as Redis, PostgreSQL, MySQL, etc., which allows a file system to be mounted and read by multiple computers in a distributed environment.

In this document, a multi-user "cloud database" is used to replace the single-user "SQLite" database used in the previous document, aiming to implement a distributed file system that can be mounted on any computer on the network for reading and writing.

## Network Database

The meaning of "Network Database" here refers to the database that allows multiple users to access it simultaneously through the network. From this perspective, the database can be simply divided into:

1. **Standalone Database**: which is a single-file database and is usually only accessed locally, such as SQLite, Microsoft Access, etc.
2. **Network Database**: which usually has complex multi-file structures, provides network-based access interfaces and supports simultaneous access by multiple users, such as Redis, PostgreSQL, etc.

JuiceFS currently supports the following network-based databases.

- **Key-Value Database**: Redis, TiKV
- **Relational Database**: PostgreSQL, MySQL, MariaDB

Different databases have different performance and stability. For example, Redis is an in-memory key-value database with an excellent performance but a relatively weak reliability, while PostgreSQL is a relational database which is more reliable but has a less excellent performance than the in-memory database.

The document that specifically introduces how to select database will come soon.

## Cloud Database

Cloud computing platforms usually offer a wide variety of cloud database, such as Amazon RDS for various relational database versions and Amazon ElastiCache for Redis-compatible in-memory database products, which allows to create a multi-copy and highly available database cluster by a simple initial setup.

Of course, you can also build your own database on the server.

For simplicity, we take Amazon ElastiCache for Redis as an example. The most basic information of a network database consists of the following 2 items.

1. **Database Address**: the access address of the database; the cloud platform may provide different links for internal and external networks.
2. **Username and Password**: authentication information used to access the database.

## Hands-on Practice

### 1. Install Client

Install the JuiceFS client on all computers that need to mount the file system, refer to ["Installation"](installation.md) for details.

### 2. Preparing Object Storage

Here is a pseudo sample with Amazon S3 as an example. You can also switch to other object storage (refer to [JuiceFS Supported Storage](../guide/how_to_set_up_object_storage.md#supported-object-storage) for details).

- **Bucket Endpoint**: `https://myjfs.s3.us-west-1.amazonaws.com`
- **Access Key ID**: `ABCDEFGHIJKLMNopqXYZ`
- **Access Key Secret**: `ZYXwvutsrqpoNMLkJiHgfeDCBA`

### 3. Preparing Database

Here is a pseudo sample with Amazon ElastiCache for Redis as an example. You can also switch to other types of databases (refer to [JuiceFS Supported Databases](../guide/how_to_set_up_metadata_engine.md) for details).

- **Database Address**: `myjfs-sh-abc.apse1.cache.amazonaws.com:6379`
- **Database Username**: `tom`
- **Database Password**: `mypassword`

The format for using a Redis database in JuiceFS is as follows.

```
redis://<username>:<password>@<Database-IP-or-URL>:6379/1
```

:::tip
Redis versions lower than 6.0 do not take username, so omit the `<username>` part in the URL, e.g. `redis://:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1` (please note that the colon in front of the password is a separator and needs to be preserved).
:::

### 4. Creating a file system

The following command creates a file system that supports cross-network, multi-machine simultaneous mounts, and shared reads and writes using an object storage and a Redis database.

```shell
juicefs format \
    --storage s3 \
    --bucket https://myjfs.s3.us-west-1.amazonaws.com \
    --access-key ABCDEFGHIJKLMNopqXYZ \
    --secret-key ZYXwvutsrqpoNMLkJiHgfeDCBA \
    redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 \
    myjfs
```

Once the file system is created, the terminal will output something like the following.

```shell
2021/12/16 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1
2021/12/16 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/12/16 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/12/16 16:37:14.527879 juicefs[22290] <INFO>: Data uses s3://myjfs/myjfs/
2021/12/16 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:myjfs UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:s3 Bucket:https://myjfs AccessKey:ABCDEFGHIJKLMNopqXYZ SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

:::info
Once a file system is created, the relevant information including name, object storage, access keys, etc. are recorded in the database. In the current example, the file system information is recorded in the Redis database, so any computer with the database address, username, and password information can mount and read the file system.
:::

### 5. Mounting the file system

Since the "data" and "metadata" of this file system are stored in cloud services, the file system can be mounted on any computer with a JuiceFS client installed for shared reads and writes at the same time. For example:

```shell
juicefs mount redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 ~/jfs
```

#### Strong data consistency guarantee

JuiceFS guarantees a "close-to-open" consistency, which means that when two or more clients read and write the same file at the same time, the changes made by client A may not be immediately visible to client B. Other client is guaranteed to see the latest data when they re-opens the file only if client A closes the file, no matter whether the file is on the same node with A or not.

#### Increase cache size to improve performance

Since object storage is a network-based storage service, it will inevitably encounter access latency. To solve this problem, JuiceFS provides and enables caching mechanism by default, i.e. allocating a part of local storage as a buffer layer between data and object storage, and caching data asynchronously to local storage when reading files. Please refer to ["Cache"](../guide/cache_management.md) for more details.

JuiceFS will set 100GiB cache in `$HOME/.juicefs/cache` or `/var/jfsCache` directory by default. Setting a larger cache space on a faster SSD can effectively improve read and write performance of JuiceFS even more .

You can use `--cache-dir` to adjust the location of the cache directory and `--cache-size` to adjust the size of the cache space, e.g.:

```shell
juicefs mount
    --background \
    --cache-dir /mycache \
    --cache-size 512000 \
    redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 \
    ~/jfs
```

:::note
The JuiceFS process needs permission to read and write to the `--cache-dir` directory.
:::

The above command sets the cache directory in the `/mycache` directory and specifies the cache space as 500GiB.

#### Auto-mount on boot

Take a Linux system as an example and assume that the client is located in the `/usr/local/bin` directory. Rename the JuiceFS client to `mount.juicefs` and copy it to the `/sbin` directory.

```shell
sudo cp /usr/local/bin/juicefs /sbin/mount.juicefs
```

Edit the `/etc/fstab` configuration file and add a new record following the rules of fstab.

```
redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1    /mnt/myjfs    juicefs    _netdev,max-uploads=50,writeback,cache-size=512000     0  0
```

:::note
By default, CentOS 6 does not mount the network file system during boot, you need to run the command `sudo chkconfig --add netfs` to enable automatically mounting.
:::

### 6. Verify the file system

After the file system is mounted, you can use the `juicefs bench` command to perform basic performance tests and functional verification of the file system to ensure that the JuiceFS file system can be accessed normally and its performance meets expectations.

:::info
The `juicefs bench` command can only complete basic performance tests. If you need a more complete evaluation of JuiceFS, please refer to ["JuiceFS Performance Evaluation Guide"](../benchmark/performance_evaluation_guide.md).
:::

```shell
juicefs bench ~/jfs
```

After running the `juicefs bench` command, N large files (1 by default) and N small files (100 by default) will be written to and read from the JuiceFS file system according to the specified concurrency (1 by default), and statistics the throughput of read and write and the latency of a single operation, as well as the latency of accessing the metadata engine.

If you encounter any problems during the verification of the file system, please refer to the ["Fault Diagnosis and Analysis"](../administration/fault_diagnosis_and_analysis.md) document for troubleshooting first.

### 7. Unmounting the file system

You can unmount the JuiceFS file system (assuming the mount point path is `~/jfs`) by the command `juicefs umount`.

```shell
juicefs umount ~/jfs
```

#### Unmounting failure

If the command fails to unmount the file system after execution, it will prompt `Device or resource busy`.

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount ~/jfs: Device or resource busy
exit status 1
```

This failure happens probably because some programs are reading or writing files in the file system when executing `unmount` command. To avoid data loss, you should first determine which processes are accessing files in the file system (e.g. via the command `lsof`) and try to release the files before re-executing the `unmount` command.

:::caution
The following command may result in file corruption and loss, so be careful to use it!
:::

You can add the option `--force` or `-f` to force the file system unmounted if you are clear about the consequence of the operation.

```shell
juicefs umount --force ~/jfs
```
