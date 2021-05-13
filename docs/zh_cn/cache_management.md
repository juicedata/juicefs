# Cache Management

To improve the performance, JuiceFS supports caching in multiple levels to reduce the latency and increase throughput, including metadata cache and data cache.

## Metadata Cache

JuiceFS caches metadata in the kernel to improve the performance.

### Metadata Cache in Kernel

Three kinds of metadata can be cached in kernel: attribute, entry (file) and direntry (directory). The timeout is configurable through the [following options](command_reference.md#juicefs-mount):

```
--attr-cache value       attributes cache timeout in seconds (default: 1)
--entry-cache value      file entry cache timeout in seconds (default: 1)
--dir-entry-cache value  dir entry cache timeout in seconds (default: 1)
```

Attribute, entry and direntry are cached for 1 second by default, to speedup lookup and getattr operations.

### Consistency

If only one client is connected, the cached metadata will be invalidated automatically upon modification. No impact on consistency.

In case multiple clients, the only way to invalidate metadata cache in the kernel is waiting for timeout.

In extreme condition, it is possible that the modification made in client A is not visible to client B in a short time window.

## Data Cache

Data cache is also provided in JuiceFS to improve performance, including page cache in the kernel and local cache in client host.

### Data Cache in Kernel

Kernel will cache content of recently visited files automatically. When the file is reopened, the content can be fetched from kernel cache directly for best performance.

Reading the same file in JuiceFS repeatedly will be extremely fast, with milliseconds latency and gigabytes throughput.

Write cache in the kernel is not enabled by default. Start from [Linux kernel 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e), FUSE supports ["writeback-cache mode"](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means the `write()` syscall can often complete very fast. You could enable writeback-cache mode by `-o writeback_cache` option when run `juicefs mount` command. It's recommended enable it when write very small data (e.g. 100 bytes) frequently.

### Read Cache in Client

The client will perform prefetch and cache automatically to improve sequence read performance according to the read mode in the application.

By default, JuiceFS client will prefetch 3 blocks in parallel when read data. You can configure it through `--prefetch` option.

A few data will be cached in memory (300MiB by default) and can be configured with `--buffer-size` option. More data will be cached in the local file system. Any local file system based on HDD, SSD or memory is fine.

Local cache can be configured with the [following options](command_reference.md#juicefs-mount):

```
--cache-dir value         directory paths of local cache, use colon to separate multiple paths (default: "$HOME/.juicefs/cache" or "/var/jfsCache")
--cache-size value        size of cached objects in MiB (default: 1024)
--free-space-ratio value  min free space (ratio) (default: 0.1)
--cache-partial-only      cache only random/small read (default: false)
```

JuiceFS client will write the data downloaded from object storage (including also the data newly uploaded) into cache directory, uncompressed and no encryption. Since JuiceFS will generate a unique key for all data written to object storage, and all objects are immutable, the cache data will never expire. When cache grows over the size limit (or disk full), it will be automatically cleaned up. The current rule is compare access time, less frequent access file will be cleaned first.

Local cache will effectively improve random read performance. It is recommended to use faster speed storage and larger cache size to accelerate the application that requires high performance in random read, e.g. MySQL, Elasticsearch, ClickHouse and etc.

### Write Cache in Client

The Client will cache the data written by application in memory. It is flushed to object storage until a chunk is filled full or forced by application with close or fsync. When an application calls `fsync()` or `close()`, the client will not return until data is uploaded to object storage and metadata server is notified, ensuring data integrity. Asynchronous uploading may help to improve performance if local storage is reliable. In this case, `close()` will not be blocked while data is being uploaded to object storage, instead it will return immediately when data is written to local cache directory.

Asynchronous upload can be enabled with the following parameter:

```
--writeback  upload objects in background (default: false)
```

When there is a demand to write lots of small files in a short period, `--writeback` is recommended to improve write performance. After the job is done, remove this option and remount to disable it. For the scenario with massive random write (for example, during MySQL incremental backup), `--writeback` is also recommended.

**Warning: When `--writeback` is enabled, never delete content in `<cache-dir>/rawstaging`. Otherwise data will get lost.**

Note that when `--writeback` is enabled, the reliability of data write is somehow depending on the cache reliability. It should be used with caution when reliability is important.

`--writeback` is disabled by default.

## Frequent Asked Questions

### Why 60 GiB disk spaces are occupied while I set cache size to 50 GiB?

It is difficult to calculate the exact disk space used by cached data in local file system. Currently, JuiceFS estimates it by accumulating all sizes of cached objects with a fixed overhead (4 KiB). It may be the different than the result from `du` command.

When the free space of the file system where is low, JuiceFS will remove cached objects to avoid filling out.