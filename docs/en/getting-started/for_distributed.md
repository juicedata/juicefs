---
sidebar_position: 3
description: This article will guide you through building a distributed, shared-access JuiceFS file system using cloud-based object storage and databases.
---

# Distributed Mode

[The previous document](./standalone.md) introduces how to create a file system that can be mounted on any host using an *object storage* and an *SQLite* database. Since object storage is accessible by any computer with privileges on the network, you can also access the same JuiceFS file system on different computers by simply copying the SQLite database file to any computer that needs to access the storage.

However, this approach does not guarantee real-time file availability when the file system is shared. Since SQLite is a single file database that cannot be accessed by multiple computers at the same time, a database that supports network access is needed, such as Redis, PostgreSQL, or MySQL. This allows a file system to be mounted and read by multiple computers in a distributed environment.

In this document, a multi-user *cloud database* will be used to replace the single-user *SQLite* database used in the previous document. This aims to implement a distributed file system that can be mounted on any computer on the network for reading and writing.

## Network databases

A *network database* is one that allows multiple users to access it simultaneously through a network. From this perspective, databases can generally be classified as:

- **Standalone databases**: Single-file databases usually only accessed locally, such as SQLite and Microsoft Access.
- **Network databases**: Databases usually with complex multi-file structures, providing network-based access interfaces and supporting simultaneous access by multiple users, such as Redis and PostgreSQL.

JuiceFS currently supports the following network-based databases.

- **Key-value databases**: Redis, TiKV, etcd, and FoundationDB
- **Relational databases**: PostgreSQL, MySQL, and MariaDB

Different databases have different performance and stability. For example, Redis is an in-memory key-value database with excellent performance but relatively weak reliability, while PostgreSQL is a more reliable relational database with lower performance than in-memory databases.

A detailed guide on database selection will be available soon.

## Cloud databases

Cloud computing platforms usually offer a wide variety of cloud databases, such as Amazon RDS for various relational database versions and Amazon ElastiCache for Redis-compatible in-memory database products. These services allow to create a multi-copy and highly available database cluster by a simple initial setup.

Alternatively, you can also build your own database on the server.

For simplicity, we'll use Amazon ElastiCache for Redis as an example. The most basic information of a network database include:

- **Database address**: The database's access address, with different links for internal and external networks.
- **Username and password**: Authentication information used to access the database.

## Hands-on practice

### 1. Install the client

Install the JuiceFS client on all computers that need to mount the file system. Refer to the [Installation](installation.md) guide for details.

### 2. Prepare object storage

Here is a pseudo sample with Amazon S3 as an example. You can also use other object storage services. Refer to [JuiceFS Supported Storage](../reference/how_to_set_up_object_storage.md#supported-object-storage) for details.

- **Bucket Endpoint**: `https://myjfs.s3.us-west-1.amazonaws.com`
- **Access Key ID**: `ABCDEFGHIJKLMNopqXYZ`
- **Access Key Secret**: `ZYXwvutsrqpoNMLkJiHgfeDCBA`

### 3. Prepare the database

Here is a pseudo sample with Amazon ElastiCache for Redis as an example. You can also use other types of databases. Refer to [JuiceFS Supported Databases](../reference/how_to_set_up_metadata_engine.md) for details.

- **Database address**: `myjfs-sh-abc.apse1.cache.amazonaws.com:6379`
- **Database username**: `tom`
- **Database password**: `mypassword`

The format for using a Redis database in JuiceFS is as follows:

```
redis://<username>:<password>@<Database-IP-or-URL>:6379/1
```

:::tip
Redis versions lower than 6.0 do not have a username, so omit the `<username>` part in the URL. For example: `redis://:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1` (note that the colon before the password is a separator and must be included).
:::

### 4. Create a file system

To create a file system that supports cross-network, multi-server simultaneous mounts with shared read/write access using object storage and a Redis database, run:

```shell
juicefs format \
    --storage s3 \
    --bucket https://myjfs.s3.us-west-1.amazonaws.com \
    --access-key ABCDEFGHIJKLMNopqXYZ \
    --secret-key ZYXwvutsrqpoNMLkJiHgfeDCBA \
    redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 \
    myjfs
```

Once the file system is created, you'll see an output similar to:

```shell
2021/12/16 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1
2021/12/16 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/12/16 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/12/16 16:37:14.527879 juicefs[22290] <INFO>: Data uses s3://myjfs/myjfs/
2021/12/16 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:myjfs UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:s3 Bucket:https://myjfs AccessKey:ABCDEFGHIJKLMNopqXYZ SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

:::info
Once the file system is created, all relevant information, including its name, object storage details, and access keys, are stored in the database. In this example, the file system information is stored in Redis, so any computer with the database address, username, and password information can mount and read the file system.
:::

### 5. Mount the file system

Since the file system's *data* and *metadata* are stored in cloud services, it can be mounted on any computer with a JuiceFS client installed for shared reads and writes at the same time. For example:

```shell
juicefs mount redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 ~/jfs
```

#### Strong data consistency guarantee

JuiceFS ensures *close-to-open* consistency. When multiple clients are reading and writing to the same file, changes made by client A may not be immediately visible to client B. Once client A closes the file, any other client, no matter whether the file is on the same node with A, is guaranteed to see the latest data upon reopening the file.

#### Increase cache size to improve performance

Since object storage is a network-based service, access latency is inevitable. To mitigate this, JuiceFS offers a caching mechanism, enabled by default. This allocates a portion of local storage as a buffer layer between your data and the object storage, asynchronously caching data to local storage when files are accessed. For more details, refer to [Cache](../guide/cache.md).

JuiceFS sets 100GiB cache in the `$HOME/.juicefs/cache` or `/var/jfsCache` directory by default. Setting a larger cache space on a faster SSD can effectively improve read and write performance of JuiceFS.

You can use `--cache-dir` to adjust the location of the cache directory and `--cache-size` to adjust the size of the cache space. For example:

```shell
juicefs mount
    --background \
    --cache-dir /mycache \
    --cache-size 512000 \
    redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 \
    ~/jfs
```

:::note
The JuiceFS process needs read and write permissions for the `--cache-dir` directory.
:::

The above command sets the cache directory in the `/mycache` directory and specifies the cache space as 500GiB.

#### Auto-mount on boot

In a Linux environment, you can set up automatic mounting when mounting a file system via the `--update-fstab` option, which adds the necessary options to mount JuiceFS to `/etc/fstab`. For example:

:::note
This feature requires JuiceFS version 1.1.0 or above.
:::

```bash
$ sudo juicefs mount --update-fstab --max-uploads=50 --writeback --cache-size 204800 redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 <MOUNTPOINT>
$ grep <MOUNTPOINT> /etc/fstab
redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 <MOUNTPOINT> juicefs _netdev,max-uploads=50,writeback,cache-size=204800 0 0
$ ls -l /sbin/mount.juicefs
lrwxrwxrwx 1 root root 29 Aug 11 16:43 /sbin/mount.juicefs -> /usr/local/bin/juicefs
```

Refer to [Mount JuiceFS at Boot Time](../administration/mount_at_boot.md) for more details.

### 6. Verify the file system

After the file system is mounted, you can use the `juicefs bench` command to perform basic performance tests and functional verification of the file system to ensure that the JuiceFS file system can be accessed normally and its performance meets expectations.

:::info
The `juicefs bench` command can only complete basic performance tests. If you need a more comprehensive evaluation of JuiceFS, refer to [JuiceFS Performance Evaluation Guide](../benchmark/performance_evaluation_guide.md).
:::

```shell
juicefs bench ~/jfs
```

This command writes and reads a specified number of large (1 by default) and small (100 by default) files to and from the JuiceFS file system according to the specified concurrency (1 by default). The command then measures the throughput and latency of read and write operations, as well as the latency of metadata engine access.

If you encounter any problems during the verification of the file system, refer to the [Fault Diagnosis and Analysis](../administration/fault_diagnosis_and_analysis.md) document for troubleshooting.

### 7. Unmount the file system

You can unmount the JuiceFS file system (assuming the mount point path is `~/jfs`) by the command `juicefs umount`.

```shell
juicefs umount ~/jfs
```

If the command fails to unmount the file system after execution, it will prompt `Device or resource busy`.

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount ~/jfs: Device or resource busy
exit status 1
```

This failure happens probably because some programs are reading or writing files in the file system when executing the `unmount` command. To avoid data loss,first determine which processes are accessing files in the file system (for example, via the `lsof` command) and try to release the files before re-executing the `unmount` command.

:::caution
The following command may result in file corruption and loss. Proceed with caution!
:::

You can force unmounting by adding the `--force` or `-f` option if you're sure of the consequences:

```shell
juicefs umount --force ~/jfs
```
