# JuiceFS Quick Start Guide

[![license](https://img.shields.io/badge/license-AGPL%20V3-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA)

![JuiceFS LOGO](../images/juicefs-logo.png)

JuiceFS is a high-performance [POSIX](https://en.wikipedia.org/wiki/POSIX) file system released under GNU Affero General Public License v3.0. It is specially optimized for the cloud-native environment. Using the JuiceFS file system to store data, the data itself will be persisted in object storage (e.g. AWS S3), and the metadata corresponding to the data will be persisted in high-performance databases such as Redis. 

JuiceFS can simply and conveniently connect massive cloud storage directly to big data, machine learning, artificial intelligence, and various application platforms that have been put into production environment, without modifying the code, you can use massive cloud storage as efficiently as using local storage. 



## Highlighted Features

1. **Fully POSIX-compatible**: Use like a local file system, seamlessly docking with existing applications, no business intrusion.
2. **Fully Hadoop-compatible**: JuiceFS [Hadoop Java SDK](hadoop_java_sdk.md) is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.
3. **S3-compatible**:  JuiceFS [S3 Gateway](s3_gateway.md) provides S3-compatible interface.
4. **Cloud Native**: JuiceFS provides [Kubernetes CSI driver](how_to_use_on_kubernetes.md) to help people who want to use JuiceFS in Kubernetes.
5. **Sharing**: JuiceFS is a shared file storage that can be read and written by thousands clients.
6. **Strong Consistency**: The confirmed modification will be immediately visible on all servers mounted with the same file system .
7. **Outstanding Performance**: The latency can be as low as a few milliseconds and the throughput can be expanded to nearly unlimited. 
8. **Data Encryption**: Supports data encryption in transit and at rest, read [the guide](encrypt.md) for more information.
9. **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
10. **Data Compression**: JuiceFS supports use [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data.

## Architecture

JuiceFS file system consists of three parts: 

1. **JuiceFS Client**: Coordinate the implementation of object storage and metadata storage engines, as well as file system interfaces such as POSIX, Hadoop, Kubernetes, and S3 gateway.
2. **Data Storage**: Store the data itself, support local disk and object storage.
3. **Metadata Engine**: Store the metadata corresponding to the data, support multiple engines such as Redis.

![](../images/juicefs-arch-new.png)

As a file system, JuiceFS will process data and its corresponding metadata separately, the data will be stored in the object storage, and the metadata will be stored in the metadata engine.

In terms of **data storage**, JuiceFS supports almost all public cloud object storage services, as well as privatized object storage such as OpenStack Swift, Ceph, and MinIO.

In terms of **metadata storage**, JuiceFS adopts a multi-engine design, and currently supports [Redis](https://redis.io/) as a metadata engine, and it will also implement TiKV, PostgreSQL, MariaDB, MySQL , Oracle and more engines.

In terms of the implementation of **file system interface**:

- With **FUSE**, the JuiceFS file system can be mounted to the server in a POSIX compatible manner, and the massive cloud storage can be used directly as local storage.
- With **Hadoop Java SDK**, the JuiceFS file system can directly replace HDFS, providing Hadoop with low-cost mass storage.
- With **Kubernetes CSI driver**, the JuiceFS file system can directly provide mass storage for Kubernetes.
- Through **S3 Gateway**, applications that use S3 as the storage layer can be directly accessed, and tools such as AWS CLI, s3cmd, and MinIO client can be used to access the JuiceFS file system.

## How JuiceFS stores files

The `file system` acts as a medium for interaction between the user and the hard drive, which allows files to be stored on the hard drive properly. As you know, Windows commonly used file systems are FAT32, NTFS, Linux commonly used file systems are Ext4, XFS, BTRFS, etc., each file system has its own unique way of organizing and managing files, which determines the file system Features such as storage capacity and performance.

As a file system, JuiceFS is no exception. Its strong consistency and high performance are inseparable from its unique file management mode.

Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS will format the data and store it in object storage (cloud storage), and store the metadata corresponding to the data in databases such as Redis. .

Any file stored in JuiceFS will be split into fixed-size **"Chunk"**, and the default upper limit is 64 MiB. Each Chunk is composed of one or more **"Slice"**. The length of the slice is not fixed, depending on the way the file is written. Each slice will be further split into fixed-size **"Block"**, which is 4 MiB by default. Finally, these blocks will be stored in the object storage. At the same time, JuiceFS will store the metadata information of Chunk, Slice, and Block of each file in metadata engines such as Redis.

![JuiceFS storage format](../images/juicefs-storage-format-new.png)

Files in JuiceFS are eventually split into Chunks, Slices and Blocks and stored in object storage. Therefore, you will find that the source file stored in JuiceFS cannot be found in the object storage browser. There is only one chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic, this is the secret of the high-performance operation of the JuiceFS file system!

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

## Quick Start

To create a JuiceFS file system, you need the following 3 preparations:

1. Redis database for metadata storage
2. Object storage is used to store data blocks
3.  JuiceFS Client

### 1. Redis Database

You can easily buy cloud Redis databases in various configurations on the cloud computing platform, but if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a Redis database instance on your local computer:

```shell
$ sudo docker run -d --name redis \
	-v redis-data:/data \
	-p 6379:6379 \
	--restart unless-stopped \
	redis redis-server --appendonly yes
```

After the container is successfully created, you can use `redis://127.0.0.1:6379` to access the redis database.

> **Note**: The above command persists redis data in the `redis-data` data volume of docker, and you can modify the storage location of data persistence as needed.

> **Security Tips**: The redis database instance created by the above command does not enable authentication and exposes the host's `6379` port. If you want to access this database via the Internet, it is strongly recommended to refer to [Redis official documentation](https: //redis.io/topics/security) Enable protected mode.

### 2. Object Storage

Like Redis databases, almost all public cloud computing platforms provide object storage services. Because JuiceFS supports object storage services on almost all platforms, you can choose freely according to your personal preferences. You can check our [Object Storage Support List and Setting Guide](), which lists all the object storage services currently supported by JuiceFS and how to use them.

Of course, if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a MinIO object storage instance on your local computer:

```shell
$ sudo docker run -d --name minio \
	-v $PWD/minio-data:/data \
	-p 9000:9000 \
	--restart unless-stopped \
	minio/minio server /data
```

After the container is successfully created, use `http://127.0.0.1:9000` to access the MinIO management interface. The initial Access Key and Secret Key of the root user are both `minioadmin`.

> **Note**: The above command maps the data path of MinIO object storage to the `minio-data` folder in the current directory, and you can modify the location of the data persistent storage as needed.

### 3. JuiceFS Client

JuiceFS supports Linux, Windows, and MacOS. You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest). Please refer to the actual system and Select the corresponding version of the architecture.

Take the x86-based Linux system as an example, download the compressed package containing `linux-amd64` in the file name:

```shell
$ wget https://github.com/juicedata/juicefs/releases/download/v0.12.1/juicefs-0.12.1-linux-amd64.tar.gz
```

Unzip and install:

```shell
$ tar -zxf juicefs-0.12.1-linux-amd64.tar.gz
$ sudo install juicefs /usr/local/bin
```

### 4. Create JuiceFS file system 

When creating a JuiceFS file system, you need to specify both the Redis database used to store metadata and the object storage used to store actual data.

The following command will create a JuiceFS file system named `pics`, use the database `1` in Redis to store metadata, and use the `pics` bucket created in MinIO to store actual data:

```shell
$ juicefs format \
	--storage minio \
	--bucket http://127.0.0.1:9000/pics \
	--access-key minioadmin \
	--secret-key minioadmin \
	redis://127.0.0.1:6379/1 \
	pics
```

After executing the command, you will see output similar to the following, indicating that the JuiceFS file system was created successfully.

```shell
2021/04/29 23:01:18.352256 juicefs[34223] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:01:18.354252 juicefs[34223] <INFO>: Ping redis: 132.185µs
2021/04/29 23:01:18.354758 juicefs[34223] <INFO>: Data uses 127.0.0.1:9000/pics/
2021/04/29 23:01:18.361674 juicefs[34223] <INFO>: Volume is formatted as {Name:pics UUID:9c0fab76-efd0-43fd-a81e-ae0916e2fc90 Storage:minio Bucket:http://127.0.0.1:9000/pics AccessKey:minioadmin SecretKey:removed BlockSize:4096 Compression:none Partitions:0 EncryptKey:}
```

> **Note**: You can create as many JuiceFS file systems as you need. But it should be noted that only one file system can be created in each Redis database. For example, when you want to create another file system named `memory`, you have to use another database in Redis, such as No.2, which is `redis://127.0.0.1:6379/2`.

### 5. Mount JuiceFS file system

After the JuiceFS file system is created, you can mount it on the operating system and use it. The following command mounts the `pics` file system to the `/mnt/jfs` directory.

```shell
$ sudo juicefs mount -d redis://127.0.0.1:6379/1 /mnt/jfs
```
> **Note**: When mounting the JuiceFS file system, there is no need to explicitly specify the name of the file system, just fill in the correct Redis server address and database number.

After executing the command, you will see output similar to the following, indicating that the JuiceFS file system has been successfully mounted on the system.

```shell
2021/04/29 23:22:25.838419 juicefs[37999] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:22:25.839184 juicefs[37999] <INFO>: Ping redis: 67.625µs
2021/04/29 23:22:25.839399 juicefs[37999] <INFO>: Data use 127.0.0.1:9000/pics/
2021/04/29 23:22:25.839554 juicefs[37999] <INFO>: Cache: /var/jfsCache/9c0fab76-efd0-43fd-a81e-ae0916e2fc90 capacity: 1024 MB
2021/04/29 23:22:26.340509 juicefs[37999] <INFO>: OK, pics is ready at /mnt/jfs
```

After the mounting is complete, you can access files in the `/mnt/jfs` directory. You can execute the `df` command to view the JuiceFS file system's mounting status:

```shell
$ df -Th
Filesystem     Type          Size    Used   Avail    Use%    Mounted on
JuiceFS:pics   fuse.juicefs  1.0P     64K    1.0P     1%     /mnt/jfs
```

> **Note**: By default, the cache of JuiceFS is located in the `/var/jfsCache` directory. In order to obtain the read and write permissions of this directory, the `sudo` command is used here to mount the JuiceFS file system with administrator privileges. When ordinary users read and write `/mnt/jfs`, please assign them the appropriate permissions.

## Windows

### 1. Requirement

JuiceFS supports creating and mounting file systems in Windows. But you need to install [WinFsp] (http://www.secfs.net/winfsp/) to be able to mount the JuiceFS file system.

> **[WinFsp](https://github.com/billziss-gh/winfsp)** is an open source Windows file system agent, it provides a FUSE emulation layer, so that the JuiceFS client can mount the file system to Windows.

### 2. Install JuiceFS on Windows 

You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest), take Windows 10 system as an example, the download file name contains `windows-amd64`, decompress and `juicefs.exe` is JuiceFS client.

For ease of use, you can create a folder named `juicefs` in the root directory of `C:\`, and extract the `juicefs.exe` into this folder. Then add the path `C:\juicefs` to the environment variables of the system. After restarting the system to make the settings take effect, you can directly use the system's built-in `Command Prompt` or `PowerShell` to execute the `juicefs` command.

![Windows ENV path](../images/windows-path.png)

### 3. Mount JuiceFS file system

It is assumed that you have prepared object storage, Redis database, and created JuiceFS file system. If you have not prepared these necessary resources, please refer to the previous section of [Quick Start](#Quick Start).

Suppose that the MinIO object storage and Redis database are deployed on the Linux host with the IP address of `192.168.1.8` in LAN, and then the following commands are executed to create a JuiceFS file system named `music`.

```shell
$ juicefs format --storage minio --bucket http://192.168.1.8:9000/music --access-key minioadmin --secret-key minioadmin redis://192.168.1.8:6379/1 music
```

> **Note**: JuiceFS client on Windows is a command line program, you need to use it in `Command Prompt`, `PowerShell` or `Windows Terminal`.

Execute the following command to mount the `music` file system to `Z` drive:

```power
> juicefs.exe mount redis://192.168.1.6:6379/1 Z:
```

![](../images/juicefs-on-windows.png)

As shown in the figure above, JuiceFS client will mount the file system as a network drive as the specified system drive letter. You can change to another drive letter according to actual needs, but be careful not to use the drive letter that is already occupied.

## macOS

### 1. Requirement

JuiceFS supports creating and mounting file systems in macOS. But you need to install [macFUSE](https://osxfuse.github.io/) before you can mount the JuiceFS file system.

> [macFUSE](https://github.com/osxfuse/osxfuse) is an open source file system enhancement tool that allows macOS to mount third-party file systems, allowing JuiceFS client to mount the file system to macOS.

### 2. Install JuiceFS on macOS 

You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest), download the compressed package containing `darwin-amd64` in the file name, for example:

```shell
$ curl -fsSL https://github.com/juicedata/juicefs/releases/download/v0.12.1/juicefs-0.12.1-darwin-amd64.tar.gz -o juicefs-0.12.1-darwin-amd64.tar.gz
```

Unzip and install:

```shell
$ tar -zxf juicefs-0.12.1-darwin-amd64.tar.gz
$ sudo install juicefs /usr/local/bin
```

### 3. Mount JuiceFS file system

It is assumed that you have prepared object storage, Redis database, and created JuiceFS file system. If you have not prepared these necessary resources, please refer to the previous section of [Quick Start](#Quick Start).

Suppose that the MinIO object storage and Redis database are deployed on the Linux host with the IP address of `192.168.1.8` in LAN, and then the following commands are executed to create a JuiceFS file system named `music`.

```shell
$ juicefs format --storage minio --bucket http://192.168.1.8:9000/music --access-key minioadmin --secret-key minioadmin redis://192.168.1.8:6379/1 music
```

Execute the following command to mount the `music` file system to the `~/music` folder in the current user's home directory.

```shell
$ juicefs mount redis://192.168.1.8:6379/1 ~/music
```

> **Tip**: In this guide, Windows and macOS mount the same file system. JuiceFS supports thousands of clients to mount the same file system at the same time, providing convenient mass data sharing capabilities.

## POSIX Compatibility 

JuiceFS passed all of the 8813 tests in latest [pjdfstest](https://github.com/pjd/pjdfstest).

```
All tests successful.

Test Summary Report
-------------------
/root/soft/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1323 Failed: 0)
  TODO passed:   693, 697, 708-709, 714-715, 729, 733
Files=235, Tests=8813, 233 wallclock secs ( 2.77 usr  0.38 sys +  2.57 cusr  3.93 csys =  9.65 CPU)
Result: PASS
```

Besides the things covered by pjdfstest, JuiceFS provides:

- Close-to-open consistency. Once a file is closed, the following open and read can see the data written before close. Within same mount point, read can see all data written before it.
- Rename and all other metadata operations are atomic guaranteed by Redis transaction.
- Open files remain accessible after unlink from same mount point.
- Mmap is supported (tested with FSx).
- Fallocate with punch hole support.
- Extended attributes (xattr).
- BSD locks (flock).
- POSIX record locks (fcntl).

## Performance Benchmark

### Basic benchmark

JuiceFS provides a subcommand to run a few basic benchmarks to understand how it works in your environment:

```
$ ./juicefs bench /jfs
Written a big file (1024.00 MiB): (113.67 MiB/s)
Read a big file (1024.00 MiB): (127.12 MiB/s)
Written 100 small files (102.40 KiB): 151.7 files/s, 6.6 ms for each file
Read 100 small files (102.40 KiB): 692.1 files/s, 1.4 ms for each file
Stated 100 files: 584.2 files/s, 1.7 ms for each file
FUSE operation: 19333, avg: 0.3 ms
Update meta: 436, avg: 1.4 ms
Put object: 356, avg: 4.8 ms
Get object first byte: 308, avg: 0.2 ms
Delete object: 356, avg: 0.2 ms
Used: 23.4s, CPU: 69.1%, MEM: 147.0 MiB
```

### Throughput

Performed a sequential read/write benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio), here is the result:

[![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)](../images/sequential-read-write-benchmark.svg)

It shows JuiceFS can provide 10X more throughput than the other two, read [more details](fio.md).

### Metadata IOPS

Performed a simple mdtest benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior), here is the result:

[![Metadata Benchmark](../images/metadata-benchmark.svg)](../images/metadata-benchmark.svg)

It shows JuiceFS can provide significantly more metadata IOPS than the other two, read [more details](../en/mdtest.md).

### Analyze performance

There is a virtual file called `.accesslog` in the root of JuiceFS to show all the operations and the time they takes, for example:

```
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

The last number on each line is the time (in seconds) current operation takes. You can use this directly to debug and analyze performance issues, or try `./juicefs profile /jfs` to monitor real time statistics. Please run `./juicefs profile -h` or refer to [here](operations_profiling.md) to learn more about this subcommand.

## Usage Tracking

JuiceFS by default collects **anonymous** usage data. It only collects core metrics (e.g. version number), no user or any sensitive data will be collected. You could review related code [here](https://github.com/juicedata/juicefs/blob/main/pkg/usage/usage.go).

These data help us understand how the community is using this project. You could disable reporting easily by command line option `--no-usage-report`:

```
$ ./juicefs mount --no-usage-report
```

## License

JuiceFS is open-sourced under GNU AGPL v3.0, see [LICENSE](https://github.com/juicedata/juicefs/blob/main/LICENSE).

## Credits

The design of JuiceFS was inspired by [Google File System](https://research.google/pubs/pub51), [HDFS](https://hadoop.apache.org/) and [MooseFS](https://moosefs.com/), thanks to their great work.