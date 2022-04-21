# JuiceFS vs. S3FS

[S3FS](https://github.com/s3fs-fuse/s3fs-fuse) is an open source tool developed in C++ that mounts S3 object storage locally via FUSE for read and write access as if it were a local disk. In addition to Amazon S3, it supports all S3 API-compatible object stores.

In terms of basic functionality, S3FS and JuiceFS can both mount object storage bucket locally via FUSE and use them with a POSIX interface. However, in terms of functional details and technical implementation, they are fundamentally different.

## Functional

S3FS is a utility that makes it easy to mount object storage Bucket locally, read and write in a way that is familiar to users, for general use scenarios that are not sensitive to performance and network latency.

JuiceFS is a distributed file system with a unique approach to data management and a series of technical optimizations for high performance, reliability and security, primarily addressing the storage needs of large volumes of data.

## Architecture

S3FS is not optimized for file objects; it acts like an access channel between local and object storage, with the local mount point seeing the same content as seen on the object storage browser, which makes it easy to use cloud storage locally. However, from another angle, this simple architecture makes S3FS' retrieval and reading and writing of files require direct interaction with the object store, and network latency can have a large impact on performance and user experience.

JuiceFS uses a technical architecture that separates data and metadata, where any file is first split into data blocks according to specific rules before being uploaded to the object store, and the corresponding metadata is stored in a separate database. The advantage of this is that retrieval of files and modification of metadata such as file names can be directly interacted with the more responsive database, bypassing the network latency impact of interacting with the object store.

In addition, when it comes to large files, although S3FS can solve the problem of transferring large files by uploading them in chunks, the nature of object storage dictates that appending files requires rewriting the entire object. For large files of tens or hundreds of gigabytes or even terabytes, repeated uploads are bound to waste a lot of time and bandwidth resources.

JuiceFS avoids such problems by splitting individual files into chunks locally according to specific rules (default 4MiB) before uploading, regardless of their size. Any rewriting and appending of files eventually becomes the generation of new data blocks, greatly reducing the waste of time and bandwidth resources.

For a detailed description of the architecture of JuiceFS, please refer to [documentation](../introduction/architecture.md).

## Caching

S3FS supports disk caching, but it is not enabled by default. Local caching can be enabled by specifying a cache path with `-o use_cache`. When caching is enabled, any file reads or writes will be written to the cache before the operation is performed. S3FS detects data changes via md5 to ensure data correctness and reduce duplicate file downloads. Since all operations involved with S3FS require interaction with S3, whether or not caching is enabled has a significant impact on its application experience.

S3FS does not limit the cache space limit by default, for larger bucket may cause the cache to fill up the disk, you need to define the space reserved for disk by `-o ensure_diskfree`. In addition, S3FS does not have a cache expiration and cleanup mechanism, so users need to manually clean the cache periodically. Once the cache space is full, uncached file operations need to interact directly with the object storage, which will have some impact on handling large files.

JuiceFS uses a completely different caching approach than S3FS. First, JuiceFS guarantees data consistency. Secondly, JuiceFS defines a default disk cache usage limit of 100GiB, which can be freely adjusted by users as needed, and ensures that no more space is used when disk space falls below 10%. When the cache usage limit is reached, JuiceFS will automatically clean up to ensure that cache is always available for subsequent read and write operations.

For more on JuiceFS caching, see [documentation](../administration/cache_management.md).

## Features

|            | S3FS         | JuiceFS                            |
|------------|--------------|------------------------------------|
| Data Storage   | S3           | S3, other object storage, WebDAV, local disk |
| Metadata Storage | no           | Database                         |
| Operating System      | Linux、macOS | Linux、macOS、Windows            |
| Access Interface | POSIX-FUSE | POSIX-FUSE, HDFS API, S3 Gateway and CSI Driver |
| POSIX Compatibility | Partially compatible     | Fully compatible  |
| Local Cache   | ✓            | ✓                                |
| Shared mounts   | ✓            | ✓                                  |
| Symbol Links   | ✕            | ✓                          |
| Extended Attributes   | ✕            | ✓                          |
| Standard Unix Permissions | ✕        | ✓                          |
| Hard Links     | ✕            | ✓                                  |
| File chunking   | ✕            | ✓                                  |
| Atomic Operations   | ✕            | ✓                                  |
| Strong Consistency   | ✕            | ✓                                  |
| Data Compression   | ✕            | ✓                                  |
| Client-side Encryption  | ✕           | ✓                                  |
| Development Language   | C++          | Go                                 |
| Open Source License   | GPL v2.0     | Apache License 2.0                 |

## Additional Notes

[OSSFS](https://github.com/aliyun/ossfs), [COSFS](https://github.com/tencentyun/cosfs), [OBSFS](https://github.com/huaweicloud/huaweicloud-obs-obsfs) are all derivatives based on S3FS and have essentially the same functional features and usage as S3FS.
