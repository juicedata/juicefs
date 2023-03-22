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

Attribute, entry and directories are cached for 1 second by default, this speeds up lookup and getattr operations. When clients on multiple nodes are sharing the same file system, the metadata cached in kernel will only expire by TTL. So in edge cases, it's possible that metadata modifications (e.g., `chown`) on node A cannot be seen immediately on node B. Nevertheless, all nodes will eventually be able to see the changes made by A after cache expiration.

### In-memory client metadata cache

:::note
This feature requires JuiceFS >= 0.15.2
:::

When a JuiceFS client `open()` a file, the attributes of the file are automatically cached in client memory. If the [`--open-cache`](../reference/command_reference.md#mount) option is set to a value greater than 0 when mounting the file system, subsequent `getattr()` and `open()` operations will return the result from the in-memory cache immediately, as long as the cache has not expired.

When doing `read()` on a file, the chunk and slice information of the file is automatically cached in the client memory. Reading the chunk again during the cache lifetime will return the slice information from the in-memory cache immediately (check ["How JuiceFS Stores Files"](../introduction/architecture.md#how-juicefs-store-files) to know what chunk and slice are).

When the `--open-cache` option is used to set a cache time, for the same mount point, the cache will be automatically invalidated when the cached file attributes change. However, the cache cannot be automatically invalidated for different mount points, so in order to ensure strong consistency, `--open-cache` is disabled by default, and every time you open a file, the client need to directly access the metadata engine. If the file is rarely modified, or in a read-only scenario (such as AI model training), it is recommended to set `--open-cache` according to the situation to further improve the read performance.

## Data cache

To improve performance, JuiceFS also provides various caching mechanisms for data, including page cache in the kernel, local file system cache in client host, and read/write buffer in client process itself. Read requests will try the kernel page cache, the client process buffer, and the local disk cache in turn. If the data requested is not found in any level of the cache, it will be read from the object storage, and also be written into every level of the cache asynchronously to improve the performance of the next access.

![](../images/juicefs-cache.png)

### Read/Write buffer {#buffer-size}

Mount parameter [`--buffer-size`](../reference/command_reference.md#mount) controls the Read/Write buffer size for JuiceFS Client, which defaults to 300 (in MiB). Buffer size dictates both the memory data size for file read (and readahead), and memory data size for writing pending pages. Naturally, we recommend increasing `--buffer-size` when under high concurrency, to effectively improve performance.

If you wish to improve write speed, and have already increased [`--max-uploads`](../reference/command_reference.md#mount) for more upload concurrency, with no noticeable increase in upload traffic, consider also increasing `--buffer-size` so that concurrent threads may easier allocate memory for data uploads. This also works in the opposite direction: if tuning up `--buffer-size` didn't bring out an increase in upload traffic, you should probably increase `--max-uploads` as well.

The `--buffer-size` also controls the data upload size for each `flush` operation, this means for clients working in a low bandwidth environment, you may need to use a lower `--buffer-size` to avoid `flush` timeouts. Refer to ["Connection problems with object storage"](../administration/troubleshooting.md#io-error-object-storage) for troubleshooting under low internet speed.

### Kernel page cache

From JuiceFS 0.15.2 and above, kernel will build page cache for opened files. If this file is not updated (i.e. `mtime` doesn't change) afterwards, it will be read directly from the page cache to achieve the best performance.

JuiceFS Client tracks a list of recently opened files. If file is opened again, client will check if file has been modified to decide whether the kernel page cache is valid, if file is already modified, all relevant page cache is invalidated on the next open, this ensures that the client can always read the latest data.

Repeated reads of the same file in JuiceFS can be extremely fast, with latencies as low as a few microseconds and throughput up to several GiBs per second.

### Kernel writeback-cache mode

Starting with [Linux kernel 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e), FUSE supports the ["writeback-cache mode"]( https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt). In this mode, kernel will merge high frequency random small writes (10-100 bytes), which significantly improves write performance.

To enable writeback-cache mode, use the [`-o writeback_cache`](../reference/fuse_mount_options.md#writeback_cache) option when you [mount JuiceFS](../reference/command_reference.md#mount). Note that writeback-cache mode is not the same as [Client write data cache](#writeback), the former is a kernel implementation while the latter happens inside the JuiceFS Client, they are meant for different scenarios, read the corresponding section for more.

### Client read data cache {#client-read-cache}

The client will perform prefetch and cache automatically to improve sequence read performance according to the read mode in the application. Data will be cached in local file system, which can be any local storage device like HDD, SSD or even memory.

Data downloaded from object storage, as well as small data (smaller than a single block) uploaded to object storage will be cached by JuiceFS Client, without compression or encryption. To achieve better performance on application's first read, use [`juicefs warmup`](../reference/command_reference.md#warmup) to cache data in advance.

If the file system where the cache directory is located is not working properly, the JuiceFS client can immediately return an error and downgrade to direct access to object storage. This is usually true for local disk, but if the file system where the cache directory is located is abnormal and the read operation is stuck (such as some kernel-mode network file system), then JuiceFS will also get stuck together. This requires you to tune the underlying file system behavior of the cache directory to fail fast.

Below are some important options for cache configuration (see [`juicefs mount`](../reference/command_reference.md#mount) for complete reference):

* `--prefetch`

  Prefetch N blocks concurrently (default to 1), prefetch mechanism works like this: when reading a block at arbitrary position, the whole block is asynchronously scheduled for download. Prefetch often improves random read performance, but if your scenario cannot effectively utilize prefetched data (for example, reading large files randomly and sparsely), prefetch will bring read amplification, consider set to 0 to disable it.

  JuiceFS is equipped with another internal similar mechanism called "readahead": when doing sequential reads, client will download nearby blocks in advance, improving sequential performance. The concurrency of readahead is affected by the size of ["Read/Write Buffer"](#buffer-size), the larger the read-write buffer, the higher the concurrency.

* `--cache-dir`

  Cache directory, default to `/var/jfsCache` or `$HOME/.juicefs/cache`. Please read ["Cache directory"](#cache-dir) for more information.

  If you are in urgent need to free up disk space, you can manually delete data under the cache directory, which is `<cache-dir>/<UUID>/raw/`.

* `--cache-size` and `--free-space-ratio`

  Cache size (in MiB, default to 102400) and minimum ratio of free size (default 0.1). Both parameters is able to control cache size, if any of the two criteria is met, JuiceFS Client will expire cache usage using an algorithm similar to LRU, i.e. remove older and less used blocks.

  Actual cache size may exceed configured value, because it is difficult to calculate the exact disk space taken by cache. Currently, JuiceFS takes the sum of all cached objects sizes using a minimum 4 KiB size, which is often different from the result of `du`.

* `--cache-partial-only`

  Only cache small files and random small reads, do not cache whole block. This applies to conditions where object storage throughput is higher than the local cache device. Default value is false.

  There are two main read patterns, sequential read and random read. Sequential read usually demands higher throughput while random reads needs lower latency. When local disk throughput is lower than object storage, consider enable `--cache-partial-only` so that sequential reads do not cache the whole block, but rather, only small reads (like footer of Parquet / ORC file) are cached. This allows JuiceFS to take advantage of low latency provided by local disk, and high throughput provided by object storage, at the same time.

### Client write data cache {#writeback}

Enabling client write cache can improve performance when writing large amount of small files. Read this section to learn about client write cache.

Client write cache is disabled by default, data writes will be held in the [read/write buffer](#buffer-size) (in memory), and is uploaded to object storage when a chunk is filled full, or forced by application with `close()`/`fsync()` calls. To ensure data security, client will not commit file writes to the Metadata Service until data is uploaded to object storage.

You can see how the default "upload first, then commit" write process will not perform well when writing large amount of small files. After the client write cache is enabled, the write process becomes "commit first, then upload asynchronously", file writes will not be blocked by data uploads, instead it will be written to the local cache directory and committed to the metadata service, and then returned immediately. The file data in the cache directory will be asynchronously uploaded to the object storage.

If you need to use JuiceFS as a temporary storage, which doesn't require persistence and distributed access, use [`--upload-delay`](../reference/command_reference.md#mount) to delay data upload, this saves the upload process if files are deleted during the delay. Meanwhile, compared with a local disk, JuiceFS uploads files automatically when the cache directory is running out of space, which keeps the applications away from unexpected failures.

Add `--writeback` to the mount command to enable client write cache, but this mode comes with some risks and caveats:

* Disk reliability is crucial to data integrity, if write cache data suffers loss before upload is complete, file data is lost forever. Use with caution when data reliability is critical.
* Write cache data by default is stored in `/var/jfsCache/<UUID>/rawstaging/`, do not delete files under this directory or data will be lost.
* Write cache size is controlled by [`--free-space-ratio`](#client-read-cache). By default, if the write cache is not enabled, the JuiceFS client uses up to 90% of the disk space of the cache directory (the calculation rule is `(1 - <free-space-ratio>) * 100`). After the write cache is enabled, a certain percentage of disk space will be overused. The calculation rule is `(1 - (<free-space-ratio> / 2)) * 100`, that is, by default, up to 95% of the disk space of the cache directory will be used.
* Write cache and read cache share cache disk space, so they affect each other. For example, if the write cache takes up too much disk space, the size of the read cache will be limited, and vice versa.
* If local disk write speed is lower than object storage upload speed, enabling `--writeback` will only result in worse write performance.
* If the file system of the cache directory raises error, client will fallback and write synchronously to object storage, which is the same behavior as [Read Cache in Client](#client-read-cache).
* If object storage upload speed is too slow (low bandwidth), local write cache can take forever to upload, meanwhile reads from other nodes will result in timeout error (I/O error). See [Connection problems with object storage](../administration/troubleshooting.md#io-error-object-storage).

Improper usage of client write cache can easily cause problems, that's why only recommend to temporarily enable this when writing large number of small files (e.g. extracting a compressed file containing a large number of small files).

When `--writeback` is enabled, apart from checking `/var/jfsCache/<UUID>/rawstaging/` directly, you can also view upload progress using:

```shell
# Assuming mount point is /jfs
$ cd /jfs
$ cat .stats | grep "staging"
juicefs_staging_block_bytes 1621127168  # The size of the data blocks to be uploaded
juicefs_staging_block_delay_seconds 46116860185.95535
juicefs_staging_blocks 394  # The number of data blocks to be uploaded
```

### Cache directory {#cache-dir}

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
It is recommended to use a high performance dedicated disk as the cache directory, avoid using the system disk, and do not share it with other applications. Sharing not only affects the performance of each other, but may also cause errors in other applications (such as insufficient disk space left). If it is unavoidable to share, you must estimate the disk capacity required by other applications, limit the size of the cache space (see below for details), and avoid JuiceFS's read cache or write cache takes up too much space.
:::

#### RAM disk

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

#### Shared folders

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

#### Multiple cache directories

JuiceFS supports setting multiple cache directories at the same time, thus avoiding the problem of insufficient cache space by separating multiple paths using `:` (Linux, macOS) or `;` (Windows), e.g.:

```shell
sudo juicefs mount --cache-dir ~/jfscache:/mnt/jfscache:/dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

When multiple cache directories are set, or multiple devices are used as cache disks, the `--cache-size` option represents the total size of data in all cache directories. The client will use the hash strategy to evenly write data to each cache path, and cannot perform special tuning for multiple cache disks with different capacities or performances.

Therefore, it is recommended that the available space of different cache directories/cache disks be consistent, otherwise it may cause the situation that the space of a certain cache directory cannot be fully utilized. For example, `--cache-dir` is `/data1:/data2`, where `/data1` has a free space of 1GiB, `/data2` has a free space of 2GiB, `--cache-size` is 3GiB, `--free-space-ratio` is 0.1. Because the cache write strategy is to write evenly, the maximum space allocated to each cache directory is `3GiB / 2 = 1.5GiB`, resulting in a maximum of 1.5GiB cache space in the `/data2` directory instead of `2GiB * 0.9 = 1.8GiB`.
