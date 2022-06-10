---
sidebar_label: Cache
sidebar_position: 5
slug: /cache_management
---
# Cache

For a file system driven by a combination of object storage and database, the cache is an important medium for efficient interaction between the local client and the remote service. Read and write data can be loaded into the cache in advance or asynchronously, and then the client interacts with the remote service in the background to perform asynchronous uploads or prefetching of data. The use of caching technology can significantly reduce the latency of storage operations and increase data throughput compared to interacting with remote services directly.

JuiceFS provides various caching mechanisms including metadata caching, data read/write caching, etc.

## Data consistency

JuiceFS provides a "close-to-open" consistency guarantee, which means that when two or more clients read and write the same file at the same time, the changes made by client A may not be immediately visible to client B. However, once the file is closed by client A, any client re-opened it afterwards is guaranteed to see the latest data, no matter it is on the same node with A or not.

"Close-to-open" is the minimum consistency guarantee provided by JuiceFS, and in some cases it may not be necessary to reopen the file to access the latest written data. For example, multiple applications using the same JuiceFS client to access the same file (where file changes are immediately visible), or to view the latest data on different nodes with the `tail -f` command.

## Read cache mechanism

When reading files in JuiceFS, there are multiple levels of caches to provide better performance for frequently accessed data. Read requests will try the kernel page cache, the pre-read buffer of the JuiceFS process, and the local disk cache in turn, and will read from the object storage only when the corresponding data is not found in the cache, and will write to all levels of caches asynchronously to ensure the performance of the next access.

![](../images/juicefs-cache.png)

## Metadata cache

JuiceFS supports caching metadata in kernel and client memory (i.e. JuiceFS processes) to improve metadata access performance.

### Metadata cache in kernel

Three kinds of metadata can be cached in kernel: **attributes (attribute)**, **file entries (entry)** and **directory entries (direntry)**. The cache timeout can be controlled by the following [mount options](../reference/command_reference.md#juicefs-mount):

```
--attr-cache value       attributes cache timeout in seconds (default: 1)
--entry-cache value      file entry cache timeout in seconds (default: 1)
--dir-entry-cache value  dir entry cache timeout in seconds (default: 1)
```

JuiceFS caches attributes, file entries, and directory entries in kernel for 1 second by default to improve lookup and getattr performance. When clients on multiple nodes are using the same file system, the metadata cached in kernel will only be expired by time. That is, in an extreme case, it may happen that node A modifies the metadata of a file (e.g., `chown`) and accesses it through node B without immediately seeing the update. Of course, when the cache expires, all nodes will eventually be able to see the changes made by A.

### Metadata cache in client

:::note
This feature requires JuiceFS >= 0.15.2
:::

When a JuiceFS client `open()` a file, its file attributes are automatically cached in client memory. If the [`--open-cache`](../reference/command_reference.md#juicefs-mount) option is set to a value greater than 0 when mounting the file system, subsequent `getattr()` and `open()` operations will return the result from the in-memory cache immediately, as long as the cache has not timed out.

When a file is read by `read()`, the chunk and slice information of the file is automatically cached in client memory. Reading the chunk again during the cache lifetime will return the slice information from the in-memory cache immediately.

:::tip
You can check ["How JuiceFS Stores Files"](../reference/how_juicefs_store_files.md) to know what chunk and slice are.
:::

By default, for any file whose metadata has been cached in memory and not accessed by any process for more than 1 hour, all its metadata cache will be automatically deleted.

## Data cache

Data cache is also provided in JuiceFS to improve performance, including page cache in the kernel and local cache in client host.

### Data cache in kernel

:::note
This feature requires JuiceFS >= 0.15.2
:::

For files that have already been read, the kernel automatically caches their contents. Then if the file is opened again, and it's not changed (i.e., mtime has not been updated), it can be read directly from the kernel cache for the best performance.

Thanks to the kernel cache, repeated reads of the same file in JuiceFS can be very fast, with latencies as low as microseconds and throughputs up to several GiBs per second.

JuiceFS clients currently do not have kernel write caching enabled by default, starting with [Linux kernel 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e), FUSE supports ["writeback-cache mode"]( https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means that the `write()` system call can be done very quickly. You can set the [`-o writeback_cache`](../reference/fuse_mount_options.md#writeback_cache) option at [mount file system](../reference/command_reference.md#juicefs-mount) to enable writeback-cache mode. It is recommended to enable this mount option when very small data (e.g. around 100 bytes) needs to be written frequently.

### Read cache in client

The JuiceFS client automatically prefetch data into the cache based on the read pattern, thus improving sequential read performance. By default, 1 block is prefetch locally concurrently with the read data. The local cache can be set on any local file system based on HDD, SSD or memory.

:::tip Hint
You can check ["How JuiceFS Stores Files"](../reference/how_juicefs_store_files.md) to learn what a block is.
:::

The local cache can be adjusted at [mount file system](../reference/command_reference.md#juicefs-mount) with the following options.

```
--cache-dir value         directory paths of local cache, use colon to separate multiple paths (default: "$HOME/.juicefs/cache" or "/var/jfsCache")
--cache-size value        size of cached objects in MiB (default: 102400)
--free-space-ratio value  min free space (ratio) (default: 0.1)
--cache-partial-only      cache only random/small read (default: false)
```

Specifically, there are two ways if you want to store the local cache of JuiceFS in memory, one is to set `--cache-dir` to `memory` and the other is to set it to `/dev/shm/<cache-dir>`. The difference between these two approaches is that the former deletes the cache data after remounting the JuiceFS file system, while the latter retains it, and there is not much difference in performance between the two.

The JuiceFS client writes data downloaded from the object store (including new uploads less than 1 block in size) to the cache directory as fast as possible, without compression or encryption. **Because JuiceFS generates unique names for all block objects written to the object store, and all block objects are not modified, there is no need to worry about invalidating the cached data when the file content is updated.**

Data caching can effectively improve the performance of random reads. For applications like Elasticsearch, ClickHouse, etc. that require higher random read performance, it is recommended to set the cache path on a faster storage medium and allocate more cache space.

#### Cache life cycle

When the cache usage space reaches the upper limit (that is, the cache size is greater than or equal to `--cache-size`) or the disk will be full (that is, the free disk space ratio is less than `--free-space-ratio`), the JuiceFS client will automatically clean the cache.

JuiceFS allocates 100GiB of cache space by default, but this does not mean that you have to use more than that capacity on disk. This value represents the maximum capacity that a JuiceFS client may use if the disk capacity allows. When the remaining space on the disk is less than 100GiB, ensure that the remaining space is not less than 10% by default.

For example, if you set `--cache-dir` to a partition with a capacity of 50GiB, then if `--cache-size` is set to 100GiB, the cache capacity of JuiceFS will always remain around 45GiB, which means that more than 10% of the partition is reserved for free space.

When the cache is full, the JuiceFS client cleans the cache using LRU-like algorithm, i.e., it tries to clean the older and less-used cache.

### Write cache in client

When writing data, the JuiceFS client caches the data in memory until it is uploaded to the object storage when a chunk is written or when the operation is forced by `close()` or `fsync()`. When `fsync()` or `close()` is called, the client waits for data to be written to the object storage and notifies the metadata service before returning, thus ensuring data integrity.

In some cases where local storage is reliable and local storage write performance is significantly better than network writes (e.g. SSD disks), write performance can be improved by enabling asynchronous upload of data so that the `close()` operation does not wait for data to be written to the object storage, but returns as soon as the data is written to the local cache directory.

The asynchronous upload feature is disabled by default and can be enabled with the following options.

```
--writeback  upload objects in background (default: false)
```

When writing a large number of small files in a short period of time, it is recommended to mount the file system with the `--writeback` parameter to improve write performance, and consider re-mounting without the option after the write is complete to make subsequent writes more reliable. It is also recommended to enable `--writeback` for scenarios that require a lot of random writes, such as incremental backups of MySQL.

:::caution
When asynchronous upload is enabled, i.e. `--writeback` is specified when mounting the file system, do not delete the contents in `<cache-dir>/<UUID>/rawstaging` directory, as this will result in data loss.
:::

When the cache disk is too full, it will pause writing data and change to uploading data directly to the object storage (i.e., the client write cache feature is turned off).

When the asynchronous upload function is enabled, the reliability of the cache itself is directly related to the reliability of data writing, and should be used with caution for scenarios requiring high data reliability.

## Cache warm-up

JuiceFS cache warm-up is an active caching means to improve the efficiency of file reading and writing by pre-caching locally the data used in high frequency.

Use the `warmup` subcommand to warm up the cache.

```shell
juicefs warmup [command options] [PATH ...]
```

Command options:

- `--file` or `-f`: file containing a list of paths
- `--threads` or `-p`: number of concurrent workers (default: 50)
- `--background` or `-b`: run in background

:::tip
Only files in the mounted file system can be warmed up, i.e. the path to be warmed up must be on the local mount point.
:::

### Warm up a directory

For example, to cache the `dataset-1` directory in a filesystem mount point locally.

```shell
juicefs warmup /mnt/jfs/dataset-1
```

### Warm up multiple directories or files

When you need to warm up the cache of multiple directories or files at the same time, you can write all the paths to a text file. For example, create a text file named `warm.txt` with one path per line in the mount point.

```
/mnt/jfs/dataset-1
/mnt/jfs/dataset-2
/mnt/jfs/pics
```

Then perform the warm up command.

```shell
juicefs warmup -f warm.txt
```

## Cache directory

Depending on the operating system, the default cache path for JuiceFS is as follows:

- **Linux**: `/var/jfsCache`
- **macOS**: `$HOME/.juicefs/cache`
- **Windows**: `%USERPROFILE%\.juicefs\cache`

For Linux, note that the default cache path requires administrator privileges and that normal users need to have the right to use `sudo` to set it up successfully, e.g.:

```shell
sudo juicefs mount redis://127.0.0.1:6379/1 /mnt/myjfs
```

Alternatively, the `--cache-dir` option can be set on any storage path accessible to the current system when mounting the filesystem. For normal users who do not have access to the `/var` directory, the cache can be set in the user's `HOME` directory, e.g.:

```shell
juicefs mount --cache-dir ~/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

:::tip
Setting the cache on a faster SSD disk can improve performance.
:::

### RAM disk

If you have higher requirements for file read performance, you can set the cache to the RAM disk. For Linux systems, the `tmpfs` type file system can be viewed with the `df` command.

```shell
$ df -Th | grep tmpfs
tmpfs          tmpfs     362M  2.0M  360M    1% /run
tmpfs          tmpfs     3.8G     0  3.8G    0% /dev/shm
tmpfs          tmpfs     5.0M  4.0K  5.0M    1% /run/lock
```

Where `/dev/shm` is a typical memory disk that can be used as a cache path for JuiceFS, it is typically half the capacity of memory and can be manually adjusted as needed, for example, to 32GB.

```shell
sudo mount -o size=32000M -o remount /dev/shm
```

Then, using that path as a cache, mount the filesystem.

```shell
juicefs mount --cache-dir /dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

### Shared folders

Shared directories created via SMB or NFS can also be used as cache for JuiceFS. For the case where multiple devices on the LAN mount the same JuiceFS file system, using shared directories on the LAN as cache paths can effectively relieve the bandwidth pressure of duplicate caches for multiple devices.

Using SMB/CIFS as an example, mount the shared directories on the LAN using the tools provided by the `cifs-utils` package.

```shell
sudo mount.cifs //192.168.1.18/public /mnt/jfscache
```

Using shared directories as JuiceFS caches:

```shell
sudo juicefs mount --cache-dir /mnt/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

### Multiple cache directories

JuiceFS supports setting multiple cache directories at the same time, thus solving the problem of insufficient cache space by splitting multiple paths using `:` (Linux, macOS) or `;` (Windows), e.g.:

```shell
sudo juicefs mount --cache-dir ~/jfscache:/mnt/jfscache:/dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

When multiple cache paths are set, the client will write data evenly to each cache path using hash policy.

:::note
When multiple cache directories are set, the `--cache-size` option represents the total size of data in all cache directories. It is recommended that the available space of different cache directories be consistent, otherwise, the space of a cache directory may not be fully utilized.

For example, `--cache-dir` is `/data1:/data2`, where `/data1` has a free space of 1GiB, `/data2` has a free space of 2GiB, `--cache-size` is 3GiB, `--free-space-ratio` is 0.1. Because the cache's write strategy is to write evenly, the maximum space allocated to each cache directory is `3GiB / 2 = 1.5GiB`, resulting in a maximum of 1.5GiB cache space in the `/data2` directory instead of `2GiB * 0.9 = 1.8GiB`.
:::

## FAQ

### Why 60 GiB disk spaces are occupied while I set cache size to 50 GiB?

JuiceFS currently estimates the size of cached objects by adding up the size of all cached objects and adding a fixed overhead (4KiB), which is not exactly the same as the value obtained by the `du` command.

To prevent the cache disk from being written to full, the client will try to reduce the cache usage when the file system where the cache directory is located is running out of space.
