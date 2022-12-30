---
title: 缓存
sidebar_position: 3
slug: /cache_management
---

对于一个由对象存储和数据库组合驱动的文件系统，缓存是本地客户端与远端服务之间高效交互的重要纽带。读写的数据可以提前或者异步载入缓存，再由客户端在后台与远端服务交互执行异步上传或预取数据。相比直接与远端服务交互，采用缓存技术可以大大降低存储操作的延时并提高数据吞吐量。

JuiceFS 提供包括元数据缓存、数据读写缓存等多种缓存机制。

:::tip 我的场景真的需要缓存吗？
数据缓存可以有效地提高随机读的性能，对于像 Elasticsearch、ClickHouse 等对随机读性能要求更高的应用，建议将缓存路径设置在速度更快的存储介质上并分配更大的缓存空间。

然而缓存能提升性能的前提是，你的应用需要反复读取同一批文件。如果你确定你的应用对数据是「读取一次，然后再也不需要」的访问模式（比如大数据的数据清洗常常就是这样），可以关闭缓存功能，省去缓存不断建立，又反复淘汰的开销。
:::

## 数据一致性 {#consistency}

JuiceFS 提供「关闭再打开（close-to-open）」一致性保证，即当两个及以上客户端同时读写相同的文件时，客户端 A 的修改在客户端 B 不一定能立即看到。但是，一旦这个文件在客户端 A 写入完成并关闭，之后在任何一个客户端重新打开该文件都可以保证能访问到最新写入的数据，不论是否在同一个节点。

「关闭再打开」是 JuiceFS 提供的最低限度一致性保证，在某些情况下可能也不需要重新打开文件才能访问到最新写入的数据：

* 多个应用程序使用同一个 JuiceFS 客户端访问相同的文件时，文件变更立即对所有进程可见。
* 在不同节点上通过 `tail -f` 命令查看最新数据（需使用 Linux 系统）

至于对象存储，JuiceFS 将文件分成一个个数据块（默认 4MiB），赋予唯一 ID 并保存在对象存储上。文件的任何修改操作都将生成新的数据块，原有块保持不变，包括本地磁盘上的缓存数据。所以不用担心数据缓存的一致性问题，因为一旦文件被修改过了，JuiceFS 会从对象存储读取新的数据块，不会再读取文件中被覆盖的部分对应的数据块（之后会被删除掉）。

## 元数据缓存 {#metadata-cache}

JuiceFS 支持在内核和客户端内存（即 JuiceFS 进程）中缓存元数据以提升元数据的访问性能。

### 内核元数据缓存 {#kernel-metadata-cache}

内核中可以缓存三种元数据：**属性（attribute)**、**文件项（entry）**和**目录项（direntry）**，可以通过以下[挂载参数](../reference/command_reference.md#mount)控制缓存时间：

```
--attr-cache value       属性缓存时长，单位秒 (默认值: 1)
--entry-cache value      文件项缓存时长，单位秒 (默认值: 1)
--dir-entry-cache value  目录项缓存时长，单位秒 (默认值: 1)
```

JuiceFS 默认会在内核中缓存属性、文件项和目录项，缓存时长 1 秒，以提高 lookup 和 getattr 的性能。当多个节点的客户端同时使用同一个文件系统时，内核中缓存的元数据只能通过时间失效。也就是说，极端情况下可能出现节点 A 修改了某个文件的元数据（如 `chown`），通过节点 B 访问未能立即看到更新的情况。当然，等缓存过期后，所有节点最终都能看到 A 所做的修改。

### 客户端内存元数据缓存

:::note 注意
此特性需要使用 0.15.2 及以上版本的 JuiceFS
:::

JuiceFS 客户端在 `open()` 操作即打开一个文件时，其文件属性（attribute）会被自动缓存在客户端内存中。如果在挂载文件系统时设置了 [`--open-cache`](../reference/command_reference.md#mount) 选项且值大于 0，只要缓存尚未超时失效，随后执行的 `getattr()` 和 `open()` 操作会从内存缓存中立即返回结果。

执行 `read()` 操作即读取一个文件时，文件的 chunk 和 slice 信息会被自动缓存在客户端内存。在缓存有效期内，再次读取 chunk 会从内存缓存中立即返回 slice 信息（查阅[「JuiceFS 如何存储文件」](../introduction/architecture.md#how-juicefs-store-files)以了解 chunk 和 slice 是什么）。

为保强一致性，`--open-cache` 默认关闭，每次打开文件都需直接访问元数据引擎。但如果文件很少发生修改，或者只读场景下（例如 AI 模型训练），则推荐根据情况设置 `--open-cache`，进一步提高读性能。

## 数据缓存

当访问 JuiceFS 中的文件时，会有多级缓存给经常访问的数据提供更好的性能，读请求会依次尝试内核分页缓存、JuiceFS 进程的预读缓冲区、本地磁盘缓存，当缓存中没找到对应数据时才会从对象存储读取，并且会异步写入各级缓存保证下一次访问的性能。

![](../images/juicefs-cache.png)

### 读写缓冲区 {#buffer-size}

挂载参数 [`--buffer-size`](../reference/command_reference.md#mount) 控制着 JuiceFS 的读写缓冲区大小，默认 300（单位 MiB）。读写缓冲区的大小决定了读取文件以及预读（readahead）的内存数据量，同时也控制着写缓存（pending page）的大小。因此在面对高并发读写场景的时候，我们推荐对 `--buffer-size` 进行相应的扩容，能有效提升性能。

如果你希望增加写入速度，通过调整 [`--max-uploads`](../reference/command_reference.md#mount) 增大了上传并发度，但并没有观察到上行带宽用量有明显增加，那么此时可能就需要相应地调大 `--buffer-size`，让并发线程更容易申请到内存来工作。这个排查原理反之亦然：如果增大 `--buffer-size` 却没有观察到上行带宽占用提升，也可以考虑增大 `--max-uploads` 来提升上传并发度。

可想而知，`--buffer-size` 也控制着每次 `flush` 操作的上传数据量大小，因此如果客户端处在一个低带宽的网络环境下，可能反而需要降低 `--buffer-size` 来避免 `flush` 超时。

### 内核数据缓存

:::note 注意
此特性需要使用 0.15.2 及以上版本的 JuiceFS
:::

对于已经读过的文件，内核会把它的内容自动缓存下来，随后再打开该文件，如果文件没有被更新（即 mtime 没有更新），就可以直接从内核中的缓存读取该文件，从而获得最好的性能。

得益于内核缓存，重复读取 JuiceFS 中相同文件的速度会非常快，延时可低至微秒，吞吐量可以到每秒数 GiB。

JuiceFS 客户端目前还未默认启用内核的写入缓存功能，从 [Linux 内核 3.15](https://github.com/torvalds/linux/commit/4d99ff8f12e) 开始，FUSE 支持[「writeback-cache 模式」](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt)，这意味着可以非常快速地完成 `write()` 系统调用。你可以在[挂载文件系统](../reference/command_reference.md#mount)时设置 [`-o writeback_cache`](../reference/fuse_mount_options.md#writeback_cache) 选项开启 writeback-cache 模式。当需要频繁写入非常小的数据（如 100 字节左右）时，建议启用此挂载选项。

### 客户端读缓存 {#client-read-cache}

本地缓存可以设置在基于机械硬盘、SSD 或内存的任意本地文件系统。

JuiceFS 客户端会把从对象存储下载的数据，以及新上传的小于 1 个 block 大小的数据写入到缓存目录中，不做压缩和加密。

以下是缓存配置的关键参数（完整参数列表见 [`juicefs mount`](../reference/command_reference.md#mount)）：

* `--prefetch`

  并发预读 N 个块（默认 1）。所谓预读（prefetch），就是随机读取文件任意一小段，都会触发对应的整个对象存储块异步完整下载。预读往往能改善随机读性能，但如果你的场景的文件访问模式无法利用到预读数据（比如 offset 跨度极大的大文件随机访问），预读会带来比较明显的读放大，可以考虑设为 0 以禁用预读特性。

  JuiceFS 还内置着另一种类似的预读机制：在顺序读时，会提前下载临近的对象存储块，这在 JuiceFS 内称为 readahead 机制，能有效提高顺序读性能。Readahead 的并发度受[「读写缓冲区」](#buffer-size)的大小影响，读写缓冲区越大并发度越高。

* `--cache-dir`

  缓存目录，默认为 `/var/jfsCache` 或 `$HOME/.juicefs/cache`。请阅读[「缓存位置」](#cache-dir)了解更多信息。

  如果急需释放磁盘空间，你可以手动清理缓存目录下的文件，缓存路径通常为 `<cache-dir>/<UUID>/raw/`。

* `--cache-size` 与 `--free-space-ratio`

  缓存空间大小（单位 MiB，默认 102400）与缓存盘的最少剩余空间占比（默认 0.1）。这两个参数任意一个达到阈值，均会自动触发缓存淘汰，使用的是类似于 LRU 的策略，即尽量清理较早且较少使用的缓存。

  实际缓存数据占用空间大小可能会略微超过设置值，这是因为对同样一批缓存数据，很难精确计算它们在不同的本地文件系统上所占用的存储空间，JuiceFS 累加所有被缓存对象大小时会按照 4KiB 的最小值来计算，因此与 `du` 得到的数值往往不一致。

* `--cache-partial-only`

  只缓存小文件和随机读的部分，适合对象存储的吞吐比缓存盘还高的情况。默认为 false。

  读一般有两种模式，连续读和随机读。对于连续读，一般需要较高的吞吐。对于随机读，一般需要较低的时延。当本地磁盘的吞吐反而比不上对象存储时，可以考虑启用 `--cache-partial-only`，这样一来，连续读虽然会将一整个对象块读取下来，但并不会被缓存。而随机读（例如读 Parquet 或者 ORC 文件的 footer）所读取的字节数比较小，不会读取整个对象块，此类读取就会被缓存。充分地利用了本地磁盘低时延和网络高吞吐的优势。

### 客户端写缓存 {#writeback}

写入数据时，JuiceFS 客户端会把数据缓存在内存，直到当一个 chunk 被写满或通过 `close()` 或 `fsync()` 强制操作时，数据才会被上传到对象存储。在调用 `fsync()` 或 `close()` 时，客户端会等数据写入对象存储并通知元数据服务后才会返回，从而确保数据完整。

在某些情况下，如果本地存储是可靠的，且本地存储的写入性能明显优于网络写入（如 SSD 盘），可以通过启用异步上传数据的方式提高写入性能，这样一来 `close()` 操作不会等待数据写入到对象存储，而是在数据写入本地缓存目录就返回。

异步上传功能默认关闭，可以通过以下选项启用：

```
--writeback  后台异步上传对象 (默认: false)
```

当需要短时间写入大量小文件时，建议使用 `--writeback` 参数挂载文件系统以提高写入性能，写入完成之后可考虑取消该选项重新挂载以使后续的写入数据获得更高的可靠性。另外，像 MySQL 的增量备份等需要大量随机写操作的场景时也建议启用 `--writeback`。

:::caution 警告
当启用了异步上传，即挂载文件系统时指定了 `--writeback` 时，千万不要删除 `<cache-dir>/<UUID>/rawstaging` 目录中的内容，否则会导致数据丢失。
:::

当缓存磁盘将被写满时，会暂停写入数据，改为直接上传数据到对象存储（即关闭客户端写缓存功能）。

启用异步上传功能时，缓存本身的可靠性与数据写入的可靠性直接相关，对数据可靠性要求高的场景应谨慎使用。

## 缓存预热 {#warmup}

JuiceFS 缓存预热是一种主动缓存手段，它可以将高频使用的数据预先缓存到本地，从而提升文件的读写效率。

使用 `warmup` 子命令预热缓存：

```
juicefs warmup [command options] [PATH ...]
```

可用选项：

- `--file` 或 `-f`：通过文件批量指定预热路径
- `--threads` 或 `-p`：并发线程，默认 50 个线程。
- `--background` 或 `-b`：后台运行

:::tip 提示
只能预热已经挂载的文件系统中的文件，即预热的路径必须在本地挂载点上。
:::

### 预热一个目录

例如，将文件系统挂载点中的 `dataset-1` 目录缓存到本地：

```shell
juicefs warmup /mnt/jfs/dataset-1
```

### 预热多个目录或文件

当需要同时预热多个目录或文件的缓存时，可以将所有路径写入一个文本文件。例如，创建一个名为 `warm.txt` 的文本文件，每行一个挂载点中的路径：

```
/mnt/jfs/dataset-1
/mnt/jfs/dataset-2
/mnt/jfs/pics
```

通过文件批量指定预热路径：

```shell
juicefs warmup -f warm.txt
```

## 缓存位置 {#cache-dir}

取决于操作系统，JuiceFS 的默认缓存路径如下：

- **Linux**：`/var/jfsCache`
- **macOS**：`$HOME/.juicefs/cache`
- **Windows**：`%USERPROFILE%\.juicefs\cache`

对于 Linux 系统，要注意默认缓存路径要求管理员权限，普通用户需要有权使用 `sudo` 才能设置成功，例如：

```shell
sudo juicefs mount redis://127.0.0.1:6379/1 /mnt/myjfs
```

另外，可以在挂载文件系统时通过 `--cache-dir` 选项设置在当前系统可以访问的任何存储路径上。对于没有访问 `/var` 目录权限的普通用户，可以把缓存设置在用户的 `HOME` 目录中，例如：

```shell
juicefs mount --cache-dir ~/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

:::tip 提示
将缓存设置在速度更快的 SSD 磁盘可以有效提升性能。
:::

### 内存盘

如果对文件的读性能有更高要求，可以把缓存设置在内存盘上。对于 Linux 系统，通过 `df` 命令查看 `tmpfs` 类型的文件系统：

```shell
$ df -Th | grep tmpfs
文件系统         类型      容量   已用  可用   已用% 挂载点
tmpfs          tmpfs     362M  2.0M  360M    1% /run
tmpfs          tmpfs     3.8G     0  3.8G    0% /dev/shm
tmpfs          tmpfs     5.0M  4.0K  5.0M    1% /run/lock
```

其中 `/dev/shm` 是典型的内存盘，可以作为 JuiceFS 的缓存路径使用，它的容量一般是内存的一半，可以根据需要手动调整容量，例如，将缓存盘的容量调整为 32GB：

```shell
sudo mount -o size=32000M -o remount /dev/shm
```

然后使用该路径作为缓存，挂载文件系统：

```shell
juicefs mount --cache-dir /dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

除此之外，还可以将 `--cache-dir` 选项设置为 `memory` 来直接使用进程内存作为缓存，与 `/dev/shm` 相比，好处是简单不依赖外部设备，但相应地也无法持久化，一般在测试评估的时候使用。

### 共享目录

SMB、NFS 等共享目录也可以用作 JuiceFS 的缓存，对于局域网有多个设备挂载了相同 JuiceFS 文件系统的情况，将局域网中的共享目录作为缓存路径，可以有效缓解多个设备重复预热缓存的带宽压力。

但是需要特别注意，通常情况下当缓存目录所在的文件系统无法正常工作时 JuiceFS 客户都能立刻返回错误，并降级成直接访问对象存储。如果共享目录异常时体现为读操作卡死（如某些内核态的网络文件系统），那么 JuiceFS 也会随之一起卡住，这就要求你对共享目录底层的文件系统行为进行调优，做到快速失败。

以 SMB/CIFS 共享为例，使用 `cifs-utils` 包提供的工具挂载局域网中的共享目录：

```shell
sudo mount.cifs //192.168.1.18/public /mnt/jfscache
```

将共享目录作为 JuiceFS 缓存：

```shell
sudo juicefs mount --cache-dir /mnt/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

### 多缓存目录

JuiceFS 支持同时设置多个缓存目录，从而解决缓存空间不足的问题，使用 `:`（Linux、macOS）或 `;`（Windows）字符分隔多个路径，例如：

```shell
sudo juicefs mount --cache-dir ~/jfscache:/mnt/jfscache:/dev/shm/jfscache redis://127.0.0.1:6379/1 /mnt/myjfs
```

当设置了多个缓存目录，或者使用多块设备作为缓存盘，`--cache-size` 选项表示所有缓存目录中的数据总大小。客户端会采用哈希策略向各个缓存路径中均匀地写入数据，无法对多块容量或性能不同的缓存盘进行特殊调优。

因此建议不同缓存目录／缓存盘的可用空间保持一致，否则可能造成不能充分利用某个缓存目录空间的情况。例如 `--cache-dir` 为 `/data1:/data2`，其中 `/data1` 的可用空间为 1GiB，`/data2` 的可用空间为 2GiB，`--cache-size` 为 3GiB，`--free-space-ratio` 为 0.1。因为缓存的写入策略是均匀写入，所以分配给每个缓存目录的最大空间是 `3GiB / 2 = 1.5GiB`，会造成 `/data2` 目录的缓存空间最大为 1.5GiB，而不是 `2GiB * 0.9 = 1.8GiB`。
