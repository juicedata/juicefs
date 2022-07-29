---
slug: /comparison/juicefs_vs_s3fs
---

# JuiceFS vs. S3FS

[S3FS](https://github.com/s3fs-fuse/s3fs-fuse) is an open source tool developed in C++ that mounts S3 object storage locally via FUSE for read and write access as a local disk. In addition to Amazon S3, it supports all S3 API-compatible object stores.

In terms of basic functionality, S3FS and JuiceFS can both mount object storage bucket locally via FUSE and use them through POSIX interfaces. However, in terms of functional details and technical implementation, they are essentially different.

## Product positioning

S3FS is a utility that allows users to mount object storage buckets locally and read and write in a way that the users used to. It targets the general use scenarios that are not sensitive to performance and network latency.

JuiceFS is a distributed file system with a unique approach to data management and a series of technical optimizations for high performance, reliability and security, which primarily addresses the storage needs of large volumes of data.

## Architecture

S3FS does not do special optimization for files. It acts like an access channel between local and object storage, allowing the same content to be seen on the local mount point and the object storage browser, which makes it easy to use cloud storage locally. On the other hand, with this simple architecture, retrieving, reading and writing of files with S3FS require direct interaction with the object store, and network latency can impact strongly on performance and user experience.

JuiceFS uses a technical architecture that separates data and metadata, where any file is first split into data blocks according to specific rules before being uploaded to the object storage, and the corresponding metadata is stored in a separated database. The advantage of this is that retrieval of files and modification of metadata such as file names can directly interact with the database with a faster response, bypassing the network latency impact of interacting with the object store.

In addition, when processing large files, although S3FS can solve the problem of transferring large files by uploading them in chunks, the nature of object storage dictates that appending files requires rewriting the entire object. For large files of tens or hundreds of gigabytes or even terabytes, repeated uploads waste a lot of time and bandwidth resources.

JuiceFS avoids such problems by splitting individual files into chunks locally according to specific rules (default 4MiB) before uploading, regardless of their size. The rewriting and appending operations will eventually become new data blocks instead of modifying already generated data blocks, which greatly reduces the waste of time and bandwidth resources.

For a detailed description of the architecture of JuiceFS, please refer to [documentation](../../introduction/architecture.md).

## Caching

S3FS supports disk caching, but it is disabled by default. Local caching can be enabled by specifying a cache path with `-o use_cache`. When caching is enabled, any file reads or writes will be written to the cache before the operation is actually performed. S3FS detects data changes via MD5 to ensure data correctness and reduce duplicate file downloads. Since all operations involved with S3FS require interactions with S3, whether the cache is enabled or not impacts significantly on its application experience.

S3FS does not limit the cache capacity by default, which may cause the cache to fill up the disk when working with large buckets. You need to define the reserved disk space by `-o ensure_diskfree`. In addition, S3FS does not have a cache expiration and cleanup mechanism, so users need to manually clean up the cache periodically. Once the cache space is full, uncached file operations need to interact directly with the object storage, which will impact large file handling.

JuiceFS uses a completely different caching approach than S3FS. First, JuiceFS guarantees data consistency. Secondly, JuiceFS defines a default disk cache usage limit of 100GiB, which can be freely adjusted by users as needed, and by default ensures that no more space is used when disk free space falls below 10%. When the cache usage limit reaches the upper limit, JuiceFS will automatically do cleanup using an LRU-like algorithm to ensure that cache is always available for subsequent read and write operations.

For more on JuiceFS caching, see [documentation](../../guide/cache_management.md).

## Features

|                           | S3FS                                                           | JuiceFS                                      |
|---------------------------|----------------------------------------------------------------|----------------------------------------------|
| Data Storage              | S3                                                             | S3, other object storage, WebDAV, local disk |
| Metadata Storage          | No                                                             | Database                                     |
| Operating System          | Linux, macOS                                                   | Linux, macOS, Windows                        |
| Access Interface          | POSIX                                                          | POSIX, HDFS API, S3 Gateway and CSI Driver   |
| POSIX Compatibility       | Partially compatible                                           | Fully compatible                             |
| Shared Mounts             | Supports but does not guarantee data integrity and consistency | Guarantee strong consistency                 |
| Local Cache               | ✓                                                              | ✓                                            |
| Symbol Links              | ✓                                                              | ✓                                            |
| Standard Unix Permissions | ✓                                                              | ✓                                            |
| Strong Consistency        | ✕                                                              | ✓                                            |
| Extended Attributes       | ✕                                                              | ✓                                            |
| Hard Links                | ✕                                                              | ✓                                            |
| File Chunking             | ✕                                                              | ✓                                            |
| Atomic Operations         | ✕                                                              | ✓                                            |
| Data Compression          | ✕                                                              | ✓                                            |
| Client-side Encryption    | ✕                                                              | ✓                                            |
| Development Language      | C++                                                            | Go                                           |
| Open Source License       | GPL v2.0                                                       | Apache License 2.0                           |

## Additional notes

[OSSFS](https://github.com/aliyun/ossfs), [COSFS](https://github.com/tencentyun/cosfs), [OBSFS](https://github.com/huaweicloud/huaweicloud-obs-obsfs) are all derivatives based on S3FS and have essentially the same functional features and usage as S3FS.
