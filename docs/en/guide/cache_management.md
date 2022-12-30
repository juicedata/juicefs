---
title: Cache
sidebar_position: 3
slug: /cache_management
---

For a file system driven by a combination of object storage and database, cache is an important medium for interacting efficiently between the local client and the remote service. Read and write data can be loaded into the cache in advance or asynchronously, and then the client uploads to or prefetches from the remote service in the background. The use of caching technology can significantly reduce the latency of storage operations and increase data throughput compared to interacting with remote services directly.

JuiceFS provides various caching mechanisms including metadata caching, data read/write caching, etc.

:::tip Does my application really need cache?
Data caching will effectively improve the performance of random reads. For applications that require high random read performance (e.g. Elasticsearch, ClickHouse), it is recommended to use faster storage medium and allocate more space for cache.

Meanwhile, cache improve performance only when application needs to repeatedly read files, so if you know for sure you're in a scenario where data is only accessed once (e.g. data cleansing during ETL), you can safely turn off cache to prevent overhead.
:::

## Data consistency

JuiceFS provides a "close-to-open" consistency guarantee, which means that when two or more clients read and write the same file at the same time, the changes made by client A may not be immediately visible to client B. However, once the file is closed by client A, any client re-opened it afterwards is guaranteed to see the latest data, no matter it is on the same node with A or not.

"Close-to-open" is the minimum consistency guarantee provided by JuiceFS, and in some cases it may not be necessary to reopen the file to access the latest written data. For example:

* Multiple applications use the same JuiceFS client to access the same file (where file changes are immediately visible)
* View the latest data on different nodes with the `tail -f` command (require use Linux)

As for object storage, JuiceFS divides files into data blocks (default 4MiB), assigns unique IDs and saves them on object storage. Any modification operation of the file will generate a new data block, and the original block remains unchanged, including the cached data on the local disk. So don't worry about the consistency of the data cache, because once the file is modified, JuiceFS will read the new data block from the object storage, and will not read the data block (which will be deleted later) corresponding to the overwritten part of the file.

## Metadata cache {#metadata-cache}

JuiceFS supports caching metadata both in kernel and in client memory (i.e. JuiceFS processes) to improve metadata access performance.

### Kernel metadata cache {#kernel-metadata-cache}

There are three kinds of metadata which can be cached in kernel: **attribute**, **entry** and **directory**. The TTL of them can be specified by the following [mount options](../reference/command_reference.md#mount):

```
--attr-cache value       attributes cache TTL in seconds (default: 1)
--entry-cache value      file entry cache TTL in seconds (default: 1)
--dir-entry-cache value  dir entry cache TTL in seconds (default: 1)
```

Attribute, entry and direntries are cached for 1 second by default, this speeds up lookup and getattr operations. When clients on multiple nodes are sharing the same file system, the metadata cached in kernel will only expire by TTL. So in edge cases, it's possible that metadata modifications (e.g., `chown`) on node A cannot be seen immediately on node B. Nevertheless, all nodes will eventually be able to see the changes made by A after cache expiration.

### In-memory client metadata cache

:::note
This feature requires JuiceFS >= 0.15.2
:::

When a JuiceFS client `open()` a file, the attributes of the file are automatically cached in client memory. If the [`--open-cache`](../reference/command_reference.md#mount) option is set to a value greater than 0 when mounting the file system, subsequent `getattr()` and `open()` operations will return the result from the in-memory cache immediately, as long as the cache has not expired.

When doing `read()` on a file, the chunk and slice information of the file is automatically cached in the client memory. Reading the chunk again during the cache lifetime will return the slice information from the in-memory cache immediately (check ["How JuiceFS Stores Files"](../introduction/architecture.md#how-juicefs-store-files) to know what chunk and slice are).

To ensure consistency, `--open-cache` is disabled by default, and every time you open a file, the client need to directly access the metadata engine. However, if the file is rarely modified, or in a read-only scenario (such as AI model training), it is recommended to set `--open-cache` according to the situation to further improve the read performance.

## Data cache

JuiceFS provides a multi-level cache to improve the performance of frequently accessed data. Read requests will try kernel page cache, the pre-read buffer of the JuiceFS process, and the local disk cache in turn. If the data requested is not found in any level of the cache, it will be read from the object storage, and also be written into every level of the cache asynchronously to improve the performance of subsequent accesses.

![](../images/juicefs-cache.png)

### Read/Write Buffer {#buffer-size}

Mount parameter [`--buffer-size`](../reference/command_reference.md#mount) controls the Read/Write buffer size for JuiceFS Client, which defaults to 300 (in MiB). Buffer size dictates both the memory data size for file read (and readahead), and memory data size for writing pending pages. Naturally, we recommend increasing `--buffer-size` when under high concurrency, to effectively improve performance.

If you wish to improve write speed, and have already increased [`--max-uploads`](../reference/command_reference.md#mount) for more upload concurrency, with no noticeable increase in upload traffic, consider also increasing `--buffer-size` so that concurrent threads may easier allocate memory for data uploads. This also works in the opposite direction: if tuning up `--buffer-size` didn't bring out an increase in upload traffic, you should probably increase `--max-uploads` as well.

The `--buffer-size` also controls the data upload size for each `flush` operation, this means for clients working in a low bandwidth environment, you may need to use a lower `--buffer-size` to avoid `flush` timeouts.

### Kernel data cache

:::note
This feature requires JuiceFS >= 0.15.2
:::

For a file that has already been read, the kernel automatically caches its content. If this file is not updated (i.e. its `mtime` doesn't change) afterwards, it will be read directly from the kernel cache to achieve better performance.

Thanks to the kernel cache, repeated reads of the same file in JuiceFS can be extremely fast, with latencies as low as a few microseconds and throughput up to several GiBs per second.

JuiceFS clients currently do not have kernel write caching enabled by default. Starting with [Linux kernel 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e), FUSE supports ["writeback-cache mode"]( https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means that the `write()` system call can be done very quickly. You can set the [`-o writeback_cache`](../reference/fuse_mount_options.md#writeback_cache) option at [mounting file system](../reference/command_reference.md#mount) to enable writeback-cache mode. It is recommended to enable this mount option when very small data (e.g. around 100 bytes) needs to be written frequently.

### Client read data cache {#client-read-cache}

Local cache can be set up on any local file system based on HDD, SSD or memory.

Data downloaded from object storage, as well as small data (smaller than a single block) uploaded to object storage will be cached by JuiceFS Client, without compression or encryption.

Below are some important options for cache configuration (see [`juicefs mount`](../reference/command_reference.md#mount) for complete reference):

* `--prefetch`

  Prefetch N blocks concurrently (default to 1), prefetch mechanism works like this: when reading a block at arbitrary position, the whole block is asynchronously scheduled for download. Prefetch often improves random read performance, but if your scenario cannot effectively utilize prefetched data (for example, reading large files randomly and sparsely), prefetch will bring read amplification, consider set to 0 to disable it.

  JuiceFS is equipped with another internal similar mechanism called "readahead": when doing sequential reads, client will download nearby blocks in advance, improving sequential performance. The concurrency of readahead is affected by the size of ["Read/Write Buffer"](#buffer-size), the larger the read-write buffer, the higher the concurrency.

* `--cache-dir`

  Cache directory, default to `/var/jfsCache` or `$HOME/.juicefs/cache`. Please read ["Cache directory"](#cache-dir) for more information.

  If you are in urgent need to free up disk space, you can manually delete data under the cache directory, which is usually `<cache-dir>/<UUID>/raw/`.

* `--cache-size` and `--free-space-ratio`

  Cache size (in MiB, default to 102400) and minimum ratio of free size (default 0.1). Both parameters is able to control cache size, if any of the two criteria is met, JuiceFS Client will expire cache usage using an algorithm similar to LRU, i.e. remove older and less used blocks.

  Actual cache size may exceed configured value, because it is difficult to calculate the exact disk space taken by cache. Currently, JuiceFS takes the sum of all cached objects sizes using a minimum 4 KiB size, which is often different from the result of `du`.

* `--cache-partial-only`

  Only cache small files and random small reads, do not cache whole block. This applies to conditions where object storage throughput is higher than the local cache device. Default value is false.

  There are two main read patterns, sequential read and random read. Sequential read usually demands higher throughput while random reads needs lower latency. When local disk throughput is lower than object storage, consider enable `--cache-partial-only` so that sequential reads do not cache the whole block, but rather, only small reads (like footer of Parquet / ORC file) are cached. This allows JuiceFS to take advantage of low latency provided by local disk, and high throughput provided by object storage, at the same time.

### Client write data cache {#writeback}

When writing data, the JuiceFS client caches the data in memory until it is uploaded to the object storage when a chunk is written or when the operation is forced by `close()` or `fsync()`. When `fsync()` or `close()` is called, the client waits for data to be written to the object storage and notifies the metadata service before returning, thus ensuring data integrity.

When local storage is reliable and its write performance is significantly better than network writes (e.g. SSD disks), write performance can be improved by enabling asynchronous upload of data. In this way, the `close()` operation does not have to wait for data to be written into the object storage, but returns as soon as the data is written to the local cache directory.

The asynchronous upload feature is disabled by default and can be enabled with the following option.

```
--writeback  upload objects in background (default: false)
```

When writing a large number of small files in a short period of time, it is recommended to mount the file system with the `--writeback` parameter to improve write performance, and consider re-mounting without the option after the write is complete to make subsequent writes more reliable. It is also recommended to enable `--writeback` for scenarios that require a lot of random writes, such as incremental backups of MySQL.

:::caution
When asynchronous upload is enabled, i.e. `--writeback` is specified when mounting the file system, do not delete the contents in `<cache-dir>/<UUID>/rawstaging` directory, as this will result in data loss.
:::

Also, `--writeback` size is not affected by `--cache-size` or `--free-space-ratio` values and `<cache-dir>/<UUID>/rawstaging` directory will grow in size as much as needed until data is effectively uploaded to the object storage. Nevertheless, when the cache disk is almost full, it will pause writing data and change to uploading data directly to the object storage (i.e., the client write cache feature is turned off).

When the asynchronous upload function is enabled, the reliability of the cache itself is directly related to the reliability of data writing. Thus, this function should be used with caution for scenarios requiring high data reliability.

## Cache warm-up {#warmup}

JuiceFS cache warm-up is an active caching means to improve the efficiency of file reading and writing by pre-caching frequently used data locally.

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

For example, to cache the `dataset-1` directory in a file system mount point locally.

```shell
juicefs warmup /mnt/jfs/dataset-1
```

### Warm up multiple directories or files

When you need to warm up the cache of multiple directories or files at the same time, you can write all the paths in a text file. For example, create a text file named `warm.txt` with one mount point path per line.

```
/mnt/jfs/dataset-1
/mnt/jfs/dataset-2
/mnt/jfs/pics
```

Then run the warm up command.

```shell
juicefs warmup -f warm.txt
```

## Cache directory {#cache-dir}

Depending on the operating system, the default cache path for JuiceFS is as follows:

- **Linux**: `/var/jfsCache`
- **macOS**: `$HOME/.juicefs/cache`
- **Windows**: `%USERPROFILE%\.juicefs\cache`

For Linux, note that the default cache path requires administrator privileges and that normal users need to be granted to use `sudo` to set it up, e.g.:

```shell
sudo juicefs mount redis://127.0.0.1:6379/1 /mnt/myjfs
```

Alternatively, the `--cache-dir` option can be set to any storage path accessible to the current system when mounting the filesystem. For normal users who do not have permission to access the `/var` directory, the cache can be set in the user's `HOME` directory, e.g.:

```shell
juicefs mount --cache-dir ~/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

:::tip
Setting up the cache on a faster SSD disk can improve performance.
:::

### RAM disk

If a higher file read performance is required, you can set up the cache into the RAM disk. For Linux systems, check the `tmpfs` file system with the `df` command.

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

Another way to use memory for cache is set `--cache-dir` option to `memory`, this puts cache directly in client process memory, which is simpler compared to `/dev/shm`, but obviously cache will be lost after process restart, use this for tests and evaluations.

### Shared folders

Shared directories created via SMB or NFS can also be used as cache for JuiceFS. For the case where multiple devices on the LAN mount the same JuiceFS file system, using shared directories on the LAN as cache paths can effectively relieve the bandwidth pressure of duplicate caches for multiple devices.

But special attention needs to be paid. Usually, when the file system where the cache directory is located fails to work properly, the JuiceFS client can immediately return an error and downgrade to direct access to object storage. If the abnormality of the shared directory shows that the read operation is stuck (such as some network file system in kernel mode), then JuiceFS will also be stuck together. This requires you to tune the underlying file system behavior of the shared directory to achieve rapid failure.

Using SMB/CIFS as an example, mount the shared directories on the LAN by using the tools provided by the `cifs-utils` package.

```shell
sudo mount.cifs //192.168.1.18/public /mnt/jfscache
```

Using shared directories as JuiceFS caches:

```shell
sudo juicefs mount --cache-dir /mnt/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

### Multiple cache directories

JuiceFS supports setting multiple cache directories at the same time, thus avoiding the problem of insufficient cache space by separating multiple paths using `:` (Linux, macOS) or `;` (Windows), e.g.:

```shell
sudo juicefs mount --cache-dir ~/jfscache:/mnt/jfscache:/dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

When multiple cache directories are set, or multiple devices are used as cache disks, the `--cache-size` option represents the total size of data in all cache directories. The client will use the hash strategy to evenly write data to each cache path, and cannot perform special tuning for multiple cache disks with different capacities or performances.

Therefore, it is recommended that the available space of different cache directories/cache disks be consistent, otherwise it may cause the situation that the space of a certain cache directory cannot be fully utilized. For example, `--cache-dir` is `/data1:/data2`, where `/data1` has a free space of 1GiB, `/data2` has a free space of 2GiB, `--cache-size` is 3GiB, `--free-space-ratio` is 0.1. Because the cache write strategy is to write evenly, the maximum space allocated to each cache directory is `3GiB / 2 = 1.5GiB`, resulting in a maximum of 1.5GiB cache space in the `/data2` directory instead of `2GiB * 0.9 = 1.8GiB`.
