# 缓存管理

为了提高性能，JuiceFS 实现了多种缓存机制来降低访问的时延和提高吞吐量，包括元数据缓存、数据缓存。

## 一致性

JuiceFS 保证关闭再打开（close-to-open）一致性。意味着一旦一个文件写入完成并关闭，之后的打开和读操作保证可以访问之前写入的数据，不管是在同一台机器或者不同的机器。特别地，在同一个挂载点内，所有写入的数据都可以立即读到（不需要重新打开文件）。

当多个客户端同时使用时，内核中缓存的元数据只能通过时间失效。

极端情况下可能出现在 A 机器做了修改操作，再去 B 机器访问时，B 机器还未能看到更新的情况。

## 元数据缓存

JuiceFS 支持在内核和客户端内存（也就是 JuiceFS 进程）中缓存元数据以提升元数据的访问性能。

### 内核元数据缓存

在内核中可以缓存三种元数据：属性（attribute)、文件项（entry）和目录项（direntry），它们可以通过如下[三个参数](command_reference.md#juicefs-mount)控制缓存时间：

```
--attr-cache value       attributes cache timeout in seconds (default: 1)
--entry-cache value      file entry cache timeout in seconds (default: 1)
--dir-entry-cache value  dir entry cache timeout in seconds (default: 1)
```

默认会缓存属性、文件项和目录项，保留 1 秒，以提高 lookup 和 getattr 的性能。

### 客户端内存元数据缓存

> **注意**：此特性需要使用 0.15.0 及以上版本的 JuiceFS。

当打开（`open()`）一个文件时对应的文件属性（attribute）会被自动缓存在客户端内存。如果设置了 [`--open-cache`](command_reference.md#juicefs-mount) 选项（值需要大于 0）并且还没有到达设置的缓存超时时间，`getattr()` 以及 `open()` 请求会立即返回。

当读取（`read()`）一个文件时 chunk 和 slice 信息会被自动缓存在客户端内存（请查阅[这里](how_juicefs_store_files.md)了解什么是 chunk 和 slice）。如果再次读取同一个文件的相同 chunk，会立即返回 slice 信息。

当一个文件在一定时间内（默认为 1 小时）没有被任何进程打开过，它的所有客户端内存元数据缓存会在后台被自动删除。

## 数据缓存

JuiceFS 对数据也提供多种缓存机制来提高性能，包括内核中的页缓存和客户端所在机器的本地缓存。

### 内核中数据缓存

> **注意**：此特性需要使用 0.15.0 及以上版本的 JuiceFS。

对于已经读过的文件，内核会把它的内容自动缓存下来，下次再打开的时候，如果文件没有被更新（即 mtime 没有更新），就可以直接从内核中的缓存读获得最好的性能。

当重复读 JuiceFS 中的同一个文件时，速度会非常快，延时可低至微秒，吞吐量可以到每秒数 GiB。

当前的 JuiceFS 客户端还未启用内核的写入缓存功能。从 [Linux 内核 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e) 开始，FUSE 支持[「writeback-cache 模式」](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt)，意味着 `write()` 系统调用通常可以非常快速地完成。你可以在执行 `juicefs mount` 命令时通过 [`-o writeback_cache`](fuse_mount_options.md#writeback_cache) 选项来开启 writeback-cache 模式。当频繁写入非常小的数据（如 100 字节左右）时，建议启用此挂载选项。

### 客户端读缓存

客户端会根据应用读数据的模式，自动做预读和缓存操作以提高顺序读的性能。

默认情况下，JuiceFS 客户端会在读取数据时并发预读 1 个 block（请查阅[这里](how_juicefs_store_files.md)了解什么是 block）。你可以通过 `--prefetch` 选项配置。

数据会缓存到本地文件系统中，可以是基于硬盘、SSD 或者内存的任意本地文件系统。

本地缓存可以通过[以下选项](command_reference.md#juicefs-mount)来调整：

```
--cache-dir value         directory paths of local cache, use colon to separate multiple paths (default: "$HOME/.juicefs/cache" or "/var/jfsCache")
--cache-size value        size of cached objects in MiB (default: 1024)
--free-space-ratio value  min free space (ratio) (default: 0.1)
--cache-partial-only      cache only random/small read (default: false)
```

JuiceFS 客户端会尽可能快地把从对象存储下载的数据（包括新上传的数据）写入到缓存目录中，不做压缩和加密。**因为 JuiceFS 会为所有写入对象存储的数据生成唯一的名字，而且所有对象不会被修改，因此不用担心缓存的数据的失效问题。** 缓存在使用空间到达上限时（或者磁盘空间快满时）会自动进行清理，目前的规则是根据访问时间，优先清理不频繁访问的文件。

数据的本地缓存可以有效地提高随机读的性能，建议使用更快的存储介质和更大的缓存空间来提升对随机读性能要求高的应用的性能，比如 MySQL、Elasticsearch、ClickHouse 等。

### 客户端写缓存

客户端会把应用写的数据缓存在内存中，当一个 chunk 被写满，或者应用强制写入（`close()` 或者 `fsync()`），或者一定时间之后再写入到对象存储中。当应用调用 `fsync()` 或者 `close()` 时，客户端会等数据写入到对象存储并且通知元数据服务后才返回，以确保数据安全。在某些情况下，如果本地存储是可靠的，可以通过启用异步上传到对象的方式来提高性能。此时 `close()` 不会等待数据写入到对象存储，而是写入到本地缓存目录就返回。

异步上传模式可以通过下面的选项启用：

```
--writeback  upload objects in background (default: false)
```

当需要短时间写入大量小文件时，建议使用 `--writeback` 参数挂载以提高写入性能，写入完成之后再去掉它重新挂载。或者在有大量随机写时 (比如应用 MySQL 的增量备份时），也建议启用 `--writeback`。

> **警告**：在 `--writeback` 开启时，千万不能删除 `<cache-dir>/<UUID>/rawstaging` 中的内容，否则会导致数据丢失。

开启 `--writeback` 时，缓存本身的可靠性与数据写入的可靠性直接相关，对此要求高的场景应谨慎使用。

默认情况下 `--writeback` 不开启。

## 常见问题

### 为什么我设置了缓存容量为 50 GiB，但实际占用了 60 GiB 的空间？

对同样一批缓存数据，很难精确计算它们在不同的本地文件系统上所占用的存储空间，目前是通过累加所有被缓存对象的大小并附加固定的开销（4KiB）来估算得到的，与 `du` 得到的数值并不完全一致。

当缓存目录所在文件系统空间不足时，客户端会尽量减少缓存使用量来防止缓存盘被写满。
