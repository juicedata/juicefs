---
title: 问题排查案例
sidebar_position: 6
---

这里收录常见问题的具体排查步骤。

## 创建文件系统（format）错误 {#format-error}

### 无法重复创建文件系统 {#create-file-system-repeatedly}

元数据引擎已经执行了 `juicefs format`，再次执行时可能无法更新之前的某些配置，将会报错：

```
cannot update volume XXX from XXX to XXX
```

这种情况需要清理元数据引擎中对应的数据，再重试。

### Redis URL 格式错误 {#invalid-redis-url}

使用的 Redis 版本小于 6.0.0 时，执行 `juicefs format` 命令如果指定了 `username` 参数，将会报错：

```
format: ERR wrong number of arguments for 'auth' command
```

只有 Redis 6.0.0 版本以后才支持指定 `username`，因此你需要省略 URL 中的 `username` 参数，例如 `redis://:password@host:6379/1`。

### Redis 哨兵（Sentinel）模式 NOAUTH 错误 {#redis-sentinel-noauth-error}

如果使用 [Redis 哨兵模式](../administration/metadata/redis_best_practices.md#sentinel-mode)时遇到以下错误：

```
sentinel: GetMasterAddrByName master="xxx" failed: NOAUTH Authentication required.
```

请确认是否为 Redis 哨兵实例[设置了密码](https://redis.io/docs/management/sentinel/#configuring-sentinel-instances-with-authentication)，如果设置了，那么需要通过 `SENTINEL_PASSWORD` 环境变量单独配置连接哨兵实例的密码，元数据引擎 URL 里的密码只会用于连接 Redis 服务器。

## 权限问题导致挂载错误 {#mount-permission-error}

使用 [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) 把宿主机上的一个目录挂载到容器中时，可能遇到下方错误：

```
docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.
```

这往往是因为使用了非 root 用户执行 `juicefs mount` 命令，进而导致 Docker 没有权限访问这个目录。这个问题有两种解决方法：

* 用 root 用户执行 `juicefs mount` 命令
* 在 FUSE 的配置文件，以及挂载命令中增加 [`allow_other`](../reference/fuse_mount_options.md#allow_other) 挂载选项。

使用普通用户执行 `juicefs mount` 命令时，可能遇到下方错误：

```
fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory
```

这个错误仅在普通用户执行挂载时出现，意味着找不到 `fusermount` 这个命令。此问题有两种解决方法：

* 用 root 用户执行 `juicefs mount` 命令
* 安装 `fuse` 包（例如 `apt-get install fuse`、`yum install fuse`）

而如果当前用户不具备 `fusermount` 命令的执行权限，则还会遇到以下错误：

```
fuse: fuse: fork/exec /usr/bin/fusermount: permission denied
```

此时可以通过下面的命令检查 `fusermount` 命令的权限：

```shell
# 只有 root 用户和 fuse 用户组的用户有权限执行
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount

# 所有用户都有权限执行
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

## 读写慢与读写失败 {#read-write-error}

### 与对象存储通信不畅（网速慢） {#io-error-object-storage}

如果无法访问对象存储，或者仅仅是网速太慢，JuiceFS 客户端也会发生读写错误。你也可以在日志中找到相应的报错。

```text
# 上传块的速度不符合预期
<INFO>: slow request: PUT chunks/0/0/1_0_4194304 (%!s(<nil>), 20.512s)

# flush 超时通常意味着对象存储上传失败
<ERROR>: flush 9902558 timeout after waited 8m0s
<ERROR>: pending slice 9902558-80: ...
```

如果是网络异常导致无法访问，或者对象存储本身出现服务异常，问题排查相对简单。但在如果是在低带宽场景下希望优化 JuiceFS 的使用体验，需要留意的事情就稍微多一些。

首先，在网速慢的时候，JuiceFS 客户端上传／下载文件容易超时（类似上方的错误日志），这种情况下可以考虑：

* 降低上传并发度，比如 [`--max-uploads=1`](../reference/command_reference.mdx#mount-data-storage-options)，避免上传超时。
* 降低读写缓冲区大小，比如 [`--buffer-size=64`](../reference/command_reference.mdx#mount-data-cache-options) 或者更小。当带宽充裕时，增大读写缓冲区能提升并发性能。但在低带宽场景下使用过大的读写缓冲区，`flush` 的上传时间会很长，因此容易超时。
* 默认 GET／PUT 请求超时时间为 60 秒，因此增大 `--get-timeout` 以及 `--put-timeout`，可以改善读写超时的情况。

此外，低带宽环境下需要慎用[「客户端写缓存」](../guide/cache.md#client-write-cache)特性。先简单介绍一下 JuiceFS 的后台任务设计：每个 JuiceFS 客户端默认都启用后台任务，后台任务中会执行碎片合并（compaction）、异步删除等工作，而如果节点网络状况太差，则会降低系统整体性能。更糟的是如果该节点还启用了客户端写缓存，则容易出现碎片合并后上传缓慢，导致其他节点无法读取该文件的危险情况：

```text
# 由于 writeback，碎片合并后的结果迟迟上传不成功，导致其他节点读取文件报错
<ERROR>: read file 14029704: input/output error
<INFO>: slow operation: read (14029704,131072,0): input/output error (0) <74.147891>
<WARNING>: fail to read sliceId 1771585458 (off:4194304, size:4194304, clen: 37746372): get chunks/0/0/1_0_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB058C0B5C3134CB80B6
```

为了避免此类问题，我们推荐在低带宽节点上禁用后台任务，也就是为挂载命令添加 [`--no-bgjob`](../reference/command_reference.mdx#mount-metadata-options) 参数。

### 警告日志：找不到对象存储块 {#warning-log-block-not-found-in-object-storage}

规模化使用 JuiceFS 时，往往会在客户端日志中看到类似以下警告：

```
<WARNING>: fail to read sliceId 1771585458 (off:4194304, size:4194304, clen: 37746372): get chunks/0/0/1_0_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB058C0B5C3134CB80B6
```

出现这一类警告时，如果并未伴随着访问异常（比如日志中出现 `input/output error`），其实不必特意关注，客户端会自行重试，往往不影响文件访问。

这行警告日志的含义是：访问 slice 出错了，因为对应的某个对象存储块不存在，对象存储返回了 `NoSuchKey` 错误。出现此类异常的可能原因有下：

* JuiceFS 客户端会异步运行碎片合并（Compaction），碎片合并完成后，文件与对象存储数据块（Block）的关系随之改变，但此时可能其他客户端正在读取该文件，因此随即报错。
* 某些客户端开启了[「写缓存」](../guide/cache.md#client-write-cache)，文件已经写入，提交到了元数据服务，但对应的对象存储 Block 却并未上传完成（比如[网速慢](#io-error-object-storage)），导致其他客户端在读取该文件时，对象存储返回数据不存在。

再次强调，如果并未出现应用端访问异常，则可安全忽略此类警告。

## 读放大 {#read-amplification}

在 JuiceFS 中，一个典型的读放大现象是：对象存储的下行流量，远大于实际读文件的速度。比方说 JuiceFS 客户端的读吞吐为 200MiB/s，但是在 S3 观察到了 2GiB/s 的下行流量。

JuiceFS 中内置了[预读](../guide/cache.md#client-read-cache)（prefetch）机制：随机读 block 的某一段，会触发整个 block 下载，这个默认开启的读优化策略，在某些场景下会带来读放大。了解这个设计以后，我们就可以开始排查了。

结合先前问题排查方法一章中介绍的[访问日志](./fault_diagnosis_and_analysis.md#access-log)知识，我们可以采集一些访问日志来分析程序的读模式，然后针对性地调整配置。下面是一个实际生产环境案例的排查过程：

```shell
# 收集一段时间的访问日志，比如 30 秒：
cat /jfs/.accesslog | grep -v "^#$" >> access.log

# 用 wc、grep 等工具简单统计发现，访问日志中大多都是 read 请求：
wc -l access.log
grep "read (" access.log | wc -l

# 选取一个文件，通过 inode 追踪其访问模式，read 的输入参数里，第一个就是 inode：
grep "read (148153116," access.log
```

采集到该文件的访问日志如下：

```
2022.09.22 08:55:21.013121 [uid:0,gid:0,pid:0] read (148153116,131072,28668010496): OK (131072) <1.309992>
2022.09.22 08:55:21.577944 [uid:0,gid:0,pid:0] read (148153116,131072,14342746112): OK (131072) <1.385073>
2022.09.22 08:55:22.098133 [uid:0,gid:0,pid:0] read (148153116,131072,35781816320): OK (131072) <1.301371>
2022.09.22 08:55:22.883285 [uid:0,gid:0,pid:0] read (148153116,131072,3570397184): OK (131072) <1.305064>
2022.09.22 08:55:23.362654 [uid:0,gid:0,pid:0] read (148153116,131072,100420673536): OK (131072) <1.264290>
2022.09.22 08:55:24.068733 [uid:0,gid:0,pid:0] read (148153116,131072,48602152960): OK (131072) <1.185206>
2022.09.22 08:55:25.351035 [uid:0,gid:0,pid:0] read (148153116,131072,60529270784): OK (131072) <1.282066>
2022.09.22 08:55:26.631518 [uid:0,gid:0,pid:0] read (148153116,131072,4255297536): OK (131072) <1.280236>
2022.09.22 08:55:27.724882 [uid:0,gid:0,pid:0] read (148153116,131072,715698176): OK (131072) <1.093108>
2022.09.22 08:55:31.049944 [uid:0,gid:0,pid:0] read (148153116,131072,8233349120): OK (131072) <1.020763>
2022.09.22 08:55:32.055613 [uid:0,gid:0,pid:0] read (148153116,131072,119523176448): OK (131072) <1.005430>
2022.09.22 08:55:32.056935 [uid:0,gid:0,pid:0] read (148153116,131072,44287774720): OK (131072) <0.001099>
2022.09.22 08:55:33.045164 [uid:0,gid:0,pid:0] read (148153116,131072,1323794432): OK (131072) <0.988074>
2022.09.22 08:55:36.502687 [uid:0,gid:0,pid:0] read (148153116,131072,47760637952): OK (131072) <1.184290>
2022.09.22 08:55:38.525879 [uid:0,gid:0,pid:0] read (148153116,131072,53434183680): OK (131072) <0.096732>
```

对着日志观察下来，发现读文件的行为大体上是「频繁随机小读」。我们尤其注意到 offset（也就是 `read` 的第三个参数）跳跃巨大，说明相邻的读操作之间跨度很大，难以利用到预读提前下载下来的数据（默认的块大小是 4MiB，换算为 4194304 字节的 offset）。也正因此，我们建议将 `--prefetch` 调整为 0（让预读并发度为 0，也就是禁用该行为），并重新挂载。这样一来，在该场景下的读放大问题得到很好的改善。

## 内存占用过高 {#memory-optimization}

如果 JuiceFS 客户端内存占用过高，考虑按照以下方向进行排查调优，但也请注意，内存优化势必不是免费的，每一项设置调整都将带来相应的开销，请在调整前做好充分的测试与验证。

* 读写缓冲区（也就是 `--buffer-size`）的大小，直接与 JuiceFS 客户端内存占用相关，因此可以通过降低读写缓冲区大小来减少内存占用，但请注意降低以后可能同时也会对读写性能造成影响。更多详见[「读写缓冲区」](../guide/cache.md#buffer-size)。
* JuiceFS 挂载客户端是一个 Go 程序，因此也可以通过降低 `GOGC`（默认 100）来令 Go 在运行时执行更为激进的垃圾回收（将带来更多 CPU 消耗，甚至直接影响性能）。详见[「Go Runtime」](https://pkg.go.dev/runtime#hdr-Environment_Variables)。
* 如果你使用自建的 Ceph RADOS 作为 JuiceFS 的数据存储，可以考虑将 glibc 替换为 [TCMalloc](https://google.github.io/tcmalloc)，后者有着更高效的内存管理实现，能在该场景下有效降低堆外内存占用。

## 卸载错误 {#unmount-error}

卸载 JuiceFS 文件系统时，如果某个文件或者目录正在被使用，那么卸载将会报错（下方假设挂载点为 `/jfs`）：

```shell
# Linux
umount: /jfs: target is busy.
        (In some cases useful info about processes that use
         the device is found by lsof(8) or fuser(1))

# macOS
Resource busy -- try 'diskutil unmount'
```

这种情况下可以：

* 用类似 `lsof /jfs` 的命令，找出该文件系统下正在使用的文件，然后按需处置对应的进程（比如强制退出），然后再次尝试卸载。
* 用 `echo 1 > /sys/fs/fuse/connections/[device-number]/abort` 强制关闭 FUSE 连接，然后再次尝试卸载。其中 `[device-number]` 也许需要你用 `lsof /jfs` 手动确认，不过本机只有一个 FUSE 挂载点的话，那么 `/sys/fs/fuse/connections` 下也只会包含一个目录，不必特意确认。
* 如果并不关心已经打开的文件，只想要尽快卸载，也可以运行 `juicefs umount --force` 来强制卸载，不过注意，强制卸载在 Linux、macOS 上的行为并不一致：
  * 对 Linux 而言，`juicefs umount --force` 意味着 `umount --lazy`，文件系统会被卸载，但已打开的文件不会关闭，而是等进程退出后再退出 FUSE 客户端。
  * 对 macOS 而言，`juicefs umount --force` 意味着 `umount -f`，文件系统会被强制卸载，已打开的文件会强制关闭。

## 开发相关问题 {#development-related-issues}

编译 JuiceFS 需要 GCC 5.4 及以上版本，版本过低可能导致类似下方报错：

```
/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1
/go/pkg/tool/linux_amd64/compile: signal: killed
```

如果编译环境与运行环境的 glibc 版本不同，会发生如下报错：

```
$ juicefs
juicefs: /lib/aarch64-linux-gnu/libc.so.6: version 'GLIBC_2.28' not found (required by juicefs)
```

这需要你在运行环境重新编译 JuiceFS 客户端，大部分 Linux 发行版都预置了 glibc，你可以用 `ldd --version` 确认其版本。
