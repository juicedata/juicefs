---
sidebar_label: Quick Start (Distributed)
sidebar_position: 3
---

# JuiceFS Distributed Mode Quick Start Guide

The previous document ["JuiceFS Standalone Mode Quick Start Guide"](/community/quick_start_guide) created a file system that can be mounted on any host by using a combination of an "object store" and a "SQLite" database. Thanks to the feature that the object store is accessible by any computer with privileges on the network, we can access the same JuiceFS file system on different computers by simply copying the SQLite database file to any computer that wants to access the store.

Obviously, it is feasible to share the file system by copying the SQLite database between computers, but the real-time availability of the files is not guaranteed. Since SQLite is a single file database that cannot be accessed by multiple computers at the same time, we need to use a database that supports network access, such as Redis, PostgreSQL, MySQL, etc., in order to allow a file system to be mounted and read by multiple computers in a distributed environment.

In this paper, based on the previous document, we further replace the database from a single-user "SQLite" to a multi-user "cloud database", thus realizing a distributed file system that can be mounted on any computer on the network for reading and writing.

## Web-based Database

The meaning of “Web Database" here refers to a database that allows multiple users to access it simultaneously over the network. From this perspective, the database can be simply divided into:

1. **Stand-alone Database**: Such databases are single file and usually only accessible on a single machine, such as SQLite, Microsoft Access, etc.
2. **Web-based Database**: Such databases are usually complex multi-file structures that provide nextwork-based access interfaces and support simultaneous multi-user access, such as Redis, PostgreSQL, etc.

JuiceFS currently supports the following network-based databases.

- **Key-Value Database**: Redis, TiKV
- **Relational Databases**: PostgreSQL, MySQL, MariaDB

Different databases have different performance and stability, for example, Redis is an in-memory key-value database with excellent performance but relatively weak reliability, and PostgreSQL is a relational database with less performance than in-memory, but it is more reliable.

We will write a special document about database selection.

## Cloud Database

Cloud computing platforms usually have a wide variety of cloud database offerings, such as Amazon RDS for various relational database versions and Amazon ElastiCache for Redis-compatible in-memory database products. A multi-copy, highly available database cluster can be created with a simple initial setup.

Of course, you can build your own database on the server if you wish.

For simplicity, here is an example of the AWS ElastiCache Redis version. For a web-based database, the most basic information is the following 2 items.

1. **Database Address**: the access address of the database, the cloud platform may provide different links for internal and external networks.
2. **Username and Password**: Authentication information used to access the database.

## Hands-on Practice

### 1. Install Client

Install the JuiceFS client on all computers that need to mount the file system, refer to [Install & Upgrade](installation.md) for details.

### 2. Preparing Object Storage

Here is a pseudo-sample with AWS S3 as an example, you can switch to other object storage, refer to [JuiceFS Supported Storage](../reference/how_to_setup_object_storage.md#supported-object-storage) for details

- **Bucket Endpoint**：`https://myjfs.s3.us-west-1.amazonaws.com`
- **Access Key ID**：`ABCDEFGHIJKLMNopqXYZ`
- **Access Key Secret**：`ZYXwvutsrqpoNMLkJiHgfeDCBA`

### 3. Preparing Database

The following is a pseudo-sample of the AWS ElastiCache Redis version as an example, you can switch to other types of databases, refer to [JuiceFS Supported Databases](../reference/how_to_setup_metadata_engine.md) for details.

- **Database Address**: `myjfs-sh-abc.apse1.cache.amazonaws.com:6379`
- **Database Username**: `tom`
- **Database Password**: `mypassword`

The format for using a Redis database in JuiceFS is as follows.

```
redis://<username>:<password>@<Database-IP-or-URL>:6379/1
```

:::tip
Redis versions prior to 6.0 do not have usernames, omit the `<username>` part of the URL, e.g. `redis://:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1` (please note that the colon in front of the password is a separator and needs to be preserved).
:::

### 4. Creating a file system

The following command creates a file system that supports cross-network, multi-machine simultaneous mounts, and shared reads and writes using a combination of "Object Storage" and "Redis" database.

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
Once a file system is created, the relevant information including name, object storage, access keys, etc. are recorded in the database in full. In the current example, the file system information is recorded in the Redis database, so any computer with the database address, username, and password information can mount and read the file system.
:::

### 5. Mounting the file system

Since the "data" and "metadata" of this file system are stored in a web-based cloud service, it can be mounted on any computer with a JuiceFS client installed for shared reads and writes at the same time. For example:

```shell
juicefs mount redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 mnt
```

#### Strong data consistency assurance

JuiceFS provides a "close-to-open" consistency guarantee, which means that when two or more clients read and write the same file at the same time, the changes made by client A may not be immediately visible to client B. However, once the file is closed by client A, any client re-opened it afterwards is guaranteed to see the latest data, no matter it is on the same node with A or not.

#### Increase cache size to improve performance

Since Object Storage is a network-based storage service, it will inevitably encounter access latency. To solve this problem, JuiceFS provides and enables caching mechanism by default, i.e. allocating a part of local storage as a buffer layer between data and object storage, and caching data to local storage asynchronously when reading and writing files, please refer to ["Caching"](../administration/cache_management.md) for more details.

By default, JuiceFS will set `1024MiB` cache in `$HOME/.juicefs/cache` or `/var/jfsCache` directory. Setting a larger cache space on a faster SSD can effectively improve JuiceFS's read and write performance.

You can use `-cache-dir` to adjust the location of the cache directory and `-cache-size` to adjust the size of the cache space, e.g.

```shell
juicefs mount
    --background \
    --cache-dir /mycache \
    --cache-size 512000 \
    redis://tom:mypassword@myjfs-sh-abc.apse1.cache.amazonaws.com:6379/1 mnt
```

:::note
The JuiceFS process needs permission to read and write to the `--cache-dir` directory.
:::

The above command sets the cache directory in the `/mycache` directory and specifies the cache space as `500GiB`.

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
By default, CentOS 6 does not mount the network file system at boot time, you need to run the command to enable automatic mounting support for the network file system: `sudo chkconfig --add netfs`
:::

### 6. Unmounting the file system

You can unmount the JuiceFS file system (assuming the mount point path is `mnt`) with the `juicefs umount` command.

```shell
juicefs umount mnt
```

#### Unmounting failure

If the command fails to unmount the file system after execution, the prompt is `Device or resource busy`.

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount mnt: Device or resource busy
exit status 1
```

This may happen because some programs are reading and writing files in the file system. To ensure data security, you should first troubleshoot which programs are interacting with files on the file system (e.g. via the `lsof` command) and try to end the interaction between them before re-executing the unmount command.

:::caution
The following commands may result in file corruption and loss, so be careful!
:::

While you can ensure data security, you can add the `--force` or `-f` parameter to the unmount command to force the file system to be unmounted.

```shell
juicefs umount --force mnt
```
