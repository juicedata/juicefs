---
sidebar_label: Cache
sidebar_position: 5
slug: /cache_management
---
# Cache

For a file system driven by a combination of object storage and database, the cache is an important medium for efficient interaction between the local client and the remote service. Read and write data can be loaded into the cache in advance or asynchronously, and then the client interacts with the remote service in the background to perform asynchronous uploads or prefetching of data. The use of caching technology can significantly reduce the latency of storage operations and increase data throughput compared to interacting with remote services directly.

JuiceFS provides various caching mechanisms including metadata caching, data read/write caching, etc.

## Data Consistency

JuiceFS provides a "close-to-open" consistency guarantee, which means that when two or more clients read and write the same file at the same time, the changes made by client A may not be immediately visible to client B. However, once the file is closed by client A, any client re-opened it afterwards is guaranteed to see the latest data, no matter it is on the same node with A or not.

"Close-to-open" is the minimum consistency guarantee provided by JuiceFS, and in some cases it may not be necessary to reopen the file to access the latest written data. For example, multiple applications using the same JuiceFS client to access the same file (where file changes are immediately visible), or to view the latest data on different nodes with the `tail -f` command.

## Metadata Cache

JuiceFS supports caching metadata in kernel and client memory (i.e. JuiceFS processes) to improve metadata access performance.

### Metadata Cache in Kernel

Three kinds of metadata can be cached in kernel: **attributes (attribute)**, **file entries (entry)** and **directory entries (direntry)**. The cache timeout can be controlled by the following [mount parameter](../reference/command_reference.md#juicefs-mount):

```
--attr-cache value       attributes cache timeout in seconds (default: 1)
--entry-cache value      file entry cache timeout in seconds (default: 1)
--dir-entry-cache value  dir entry cache timeout in seconds (default: 1)
```

JuiceFS caches attributes, file entries, and directory entries in kernel for 1 second by default to improve lookup and getattr performance. When clients on multiple nodes are using the same file system, the metadata cached in kernel will only be expired by time. That is, in an extreme case, it may happen that node A modifies the metadata of a file (e.g., `chown`) and accesses it through node B without immediately seeing the update. Of course, when the cache expires, all nodes will eventually be able to see the changes made by A.

### Metadata Cache in Client

> **Note**: This feature requires JuiceFS >= 0.15.2.

When a JuiceFS client `open()` a file, its file attributes are automatically cached in client memory. If the [`--open-cache`](../reference/command_reference.md#juicefs-mount) option is set to a value greater than 0 when mounting the file system, subsequent `getattr()` and `open()` operations will return the result from the in-memory cache immediately, as long as the cache has not timed out.

When a file is read by `read()`, the chunk and slice information of the file is automatically cached in client memory. Reading the chunk again during the cache lifetime will return the slice information from the in-memory cache immediately.

> **Hint**: You can check ["How JuiceFS Stores Files"](../reference/how_juicefs_store_files.md) to know what chunk and slice are.

By default, for any file whose metadata has been cached in memory and not accessed by any process for more than 1 hour, all its metadata cache will be automatically deleted.

## Data Cache

Data cache is also provided in JuiceFS to improve performance, including page cache in the kernel and local cache in client host.

### Data Cache in Kernel

> **Note**: This feature requires JuiceFS >= 0.15.2.

For files that have already been read, the kernel automatically caches their contents. Then if the file is opened again, and it's not changed (i.e., mtime has not been updated), it can be read directly from the kernel cache for the best performance.

Thanks to the kernel cache, repeated reads of the same file in JuiceFS can be very fast, with latencies as low as microseconds and throughputs up to several GiBs per second.

JuiceFS clients currently do not have kernel write caching enabled by default, starting with [Linux kernel 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e), FUSE supports ["writeback-cache mode"]( https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means that the `write()` system call can be done very quickly. You can set the [`-o writeback_cache`](../reference/fuse_mount_options.md#writeback_cache) option at [mount file system](../reference/command_reference.md#juicefs-mount) to enable writeback-cache mode. It is recommended to enable this mount option when very small data (e.g. around 100 bytes) needs to be written frequently.

### Read Cache in Client

The JuiceFS client automatically prefetch data into the cache based on the read pattern, thus improving sequential read performance. By default, 1 block is prefetch locally concurrently with the read data. The local cache can be set on any local file system based on HDD, SSD or memory.

> **Hint**: You can check ["How JuiceFS Stores Files"](../reference/how_juicefs_store_files.md) to learn what a block is.

The local cache can be adjusted at [mount file system](../reference/command_reference.md#juicefs-mount) with the following options.

```
--cache-dir value         directory paths of local cache, use colon to separate multiple paths (default: "$HOME/.juicefs/cache" or "/var/jfsCache")
--cache-size value        size of cached objects in MiB (default: 102400)
--free-space-ratio value  min free space (ratio) (default: 0.1)
--cache-partial-only      cache only random/small read (default: false)
```

Specifically, there are two ways if you want to store the local cache of JuiceFS in memory, one is to set `--cache-dir` to `memory` and the other is to set it to `/dev/shm/<cache-dir>`. The difference between these two approaches is that the former deletes the cache data after remounting the JuiceFS file system, while the latter retains it, and there is not much difference in performance between the two.

The JuiceFS client writes data downloaded from the object store (including new uploads less than 1 block in size) to the cache directory as fast as possible, without compression or encryption. **Because JuiceFS generates unique names for all block objects written to the object store, and all block objects are not modified, there is no need to worry about invalidating the cached data when the file content is updated.**

The cache is automatically purged when it reaches the maximum space used (i.e., the cache size is greater than or equal to `--cache-size`) or when the disk is going to be full (i.e., the disk free space ratio is less than `--free-space-ratio`), and the current rule is to prioritize purging infrequently accessed files based on access time.

Data caching can effectively improve the performance of random reads. For applications like Elasticsearch, ClickHouse, etc. that require higher random read performance, it is recommended to set the cache path on a faster storage medium and allocate more cache space.

### Write Cache in Client

When writing data, the JuiceFS client caches the data in memory until it is uploaded to the object storage when a chunk is written or when the operation is forced by `close()` or `fsync()`. When `fsync()` or `close()` is called, the client waits for data to be written to the object storage and notifies the metadata service before returning, thus ensuring data integrity.

In some cases where local storage is reliable and local storage write performance is significantly better than network writes (e.g. SSD disks), write performance can be improved by enabling asynchronous upload of data so that the `close()` operation does not wait for data to be written to the object storage, but returns as soon as the data is written to the local cache directory.

The asynchronous upload feature is disabled by default and can be enabled with the following options.

```
--writeback  upload objects in background (default: false)
```

When writing a large number of small files in a short period of time, it is recommended to mount the file system with the `--writeback` parameter to improve write performance, and consider re-mounting without the option after the write is complete to make subsequent writes more reliable. It is also recommended to enable `--writeback` for scenarios that require a lot of random writes, such as incremental backups of MySQL.

> **Warning**: When asynchronous upload is enabled, i.e. `--writeback` is specified when mounting the file system, do not delete the contents in `<cache-dir>/<UUID>/rawstaging` directory, as this will result in data loss.

When the cache disk is too full, it will pause writing data and change to uploading data directly to the object storage (i.e., the client write cache feature is turned off).

When the asynchronous upload function is enabled, the reliability of the cache itself is directly related to the reliability of data writing, and should be used with caution for scenarios requiring high data reliability.

## Frequent Asked Questions

### Why 60 GiB disk spaces are occupied while I set cache size to 50 GiB?

JuiceFS currently estimates the size of cached objects by adding up the size of all cached objects and adding a fixed overhead (4KiB), which is not exactly the same as the value obtained by the `du` command.

To prevent the cache disk from being written to full, the client will try to reduce the cache usage when the file system where the cache directory is located is running out of space.
