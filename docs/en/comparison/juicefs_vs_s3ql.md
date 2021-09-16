# JuiceFS vs. S3QL

Similar to JuiceFS, [S3QL](https://github.com/s3ql/s3ql) is also an open source network file system driven by object storage and database. All data will be split into blocks and stored in object storage services such as, Amazon S3, Backblaze B2, or OpenStack Swift, the corresponding metadata will be stored in the database.

## The same point

- All support the standard POSIX file system interface through the FUSE module, so that massive cloud storage can be mounted locally and used like local storage.
- All can provide standard file system functions: hard links, symbolic links, extended attributes, file permissions.
- All support data compression and encryption, but the algorithms used are different.

## Different points

- S3QL only supports SQLite. But JuiceFS supports more databases, such as Redis, TiKV, MySQL, PostgreSQL, and SQLite.
- S3QL has no distributed capability and **does not** support multi-host shared mounting. JuiceFS is a typical distributed file system. When using a network-based database, it supports multi-host distributed mount read and write.
- S3QL commits a data block to S3 when it has not been accessed for more than a few seconds. After a file closed or even fsynced, it is only guranteed to stay in system memory, which may result in data loss if node fails. JuiceFS ensures high data durability, uploading all blocks synchronously when a file is closed.
- S3QL provides data deduplication. Only one copy of the same data is stored, which can reduce the storage usage, but it will also increase the performance overhead of the system. JuiceFS pays more attention to performance, and it is too expensive to perform deduplication on large-scale data, so this function is temporarily not provided.
- S3QL provides remote synchronous backup of metadata. SQLite databases with metadata will be backed up asynchronously to object storage. JuiceFS mainly uses network databases such as Redis and MySQL, and does not directly provide SQLite database synchronization backup function, but JuiceFS supports metadata import and export, as well as various storage backend synchronization functions, users can easily backup metadata to objects Storage, also supports migration between different databases.

|                           | **S3QL**              | **JuiceFS**                   |
| :------------------------ | :-------------------- | :---------------------------- |
| Metadata engine           | SQLite                | Redis, MySQL, SQLite, TiKV    |
| Storage engine            | Object Storage, Local | Object Storage, WebDAV, Local |
| Operating system          | Unix-like             | Linux, macOS, Windows         |
| Compression algorithm     | LZMA, bzip2, gzip     | lz4, zstd                     |
| Encryption algorithm      | AES-256               | AES-GCM, RSA                  |
| POSIX compatible          | ✓                     | ✓                             |
| Hard link                 | ✓                     | ✓                             |
| Symbolic link             | ✓                     | ✓                             |
| Extended attributes       | ✓                     | ✓                             |
| Standard Unix permissions | ✓                     | ✓                             |
| Data block                | ✓                     | ✓                             |
| Local cache               | ✓                     | ✓                             |
| Elastic storage           | ✓                     | ✓                             |
| Metadata backup           | ✓                     | ✓                             |
| Data deduplication        | ✓                     | ✕                             |
| Immutable trees           | ✓                     | ✕                             |
| Snapshots                 | ✓                     | ✕                             |
| Share mount               | ✕                     | ✓                             |
| Hadoop SDK                | ✕                     | ✓                             |
| Kubernetes CSI Driver     | ✕                     | ✓                             |
| S3 gateway                | ✕                     | ✓                             |
| Language                  | Python                | Go                            |
| Open source license       | GPLv3                 | AGPLv3                        |
| Open source date          | 2011                  | 2021.1                        |

## Usage

This part mainly evaluates the ease of installation and use of the two products.

### Installation

During the installation process, we use Rocky Linux 8.4 operating system (kernel version 4.18.0-305.12.1.el8_4.x86_64).

#### S3QL

S3QL is developed in Python and requires python-devel 3.7 and above to be installed. In addition, at least the following dependencies must be met: fuse3-devel, gcc, pyfuse3, sqlite-devel, cryptography, defusedxml, apsw, dugong. In addition, you need to pay special attention to Python's package dependencies and location issues.

S3QL will install 12 binary programs in the system, and each program provides an independent function, as shown in the figure below.

![](../../images/s3ql-bin.jpg)

#### JuiceFS

JuiceFS is developed in Go and can be used directly by downloading the pre-compiled binary file. The JuiceFS client has only one binary program `juicefs`, just copy it to any executable path of the system, for example: `/usr/local/bin`.

### Create and Mount a file system

Both S3QL and JuiceFS use database to store metadata. S3QL only supports SQLite databases, and JuiceFS supports databases such as Redis, TiKV, MySQL, MariaDB, PostgreSQL, and SQLite.

Here we use Minio object storage created locally and use them to create a file system separately:

#### S3QL

S3QL uses `mkfs.s3ql` to create a file system:

```shell
$ mkfs.s3ql --plain --backend-options no-ssl -L s3ql s3c://127.0.0.1:9000/s3ql/
```

Mount a file system using `mount.s3ql`:

```shell
$ mount.s3ql --compress none --backend-options no-ssl s3c://127.0.0.1:9000/s3ql/ mnt-s3ql
```

S3QL needs to interactively provide the access key of the object storage API through the command line when creating and mounting a file system.

#### JuiceFS

JuiceFS uses the `format` subcommand to create a file system:

```shell
$ juicefs format --storage minio \
    --bucket http://127.0.0.1:9000/myjfs \
    --access-key minioadmin \
    --secret-key minioadmin \
    sqlite3://myjfs.db \
    myjfs
```

Mount a file system using `mount` subcommand:

```shell
$ sudo juicefs mount -d sqlite3://myjfs.db mnt-juicefs
```

JuiceFS only sets the object storage API access key when creating a file system, and the relevant information will be written into the metadata engine. After created, there is no need to repeatedly provide the object storage url, access key and other information.

## Summary

**S3QL** adopts the storage structure of object storage + SQLite, and storing the data in blocks can not only improve the read and write efficiency of the file, but also reduce the resource overhead when the file is modified. The advanced features such as snapshots, data deduplication, and data retention, as well as the default data compression and data encryption, making S3QL very suitable for individuals to store files in cloud storage at a lower cost and more securely.

**JuiceFS** supports object storage, HDFS, WebDAV, and local disks as data storage engines, and supports popular databases such as Redis, TiKV, MySQL, MariaDB, PostgreSQL, and SQLite as metadata storage engines. It provides a standard POSIX file system interface through FUSE, and a Java API, which can directly replace HDFS to provide storage for Hadoop. At the same time, it also provides [Kubernetes CSI Driver](https://github.com/juicedata/juicefs-csi-driver), which can be used as the storage layer of Kubernetes for data persistent storage. JucieFS is a file system designed for enterprise-level distributed data storage scenarios. It is widely used in various scenarios such as big data analysis, machine learning, container shared storage, data sharing, and backup.
