---
title: 排查案例
---

这里收录常见问题的具体排查步骤。

## 权限问题导致挂载错误 {#mount-permission-error}

使用 [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) 把宿主机上的一个目录挂载到容器中时，可能遇到下方错误：

```
docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.
```

这往往是因为使用了非 root 用户执行了 `juicefs mount` 命令，进而导致 Docker 没有权限访问这个目录。这个问题有两种解决方法：

* 用 root 用户执行 `juicefs mount` 命令
* 在 FUSE 的配置文件，以及挂载命令中增加 [`allow_other`](../reference/fuse_mount_options.md#allow_other) 挂载选项。

使用普通用户执行 `juicefs mount` 命令时，可能遭遇下方错误：

```
fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory
```

这个错误意味着使用了非 root 用户执行 ，并且 `fusermount` 这个命令也找不到。这个问题有两种解决方法：

1. 用 root 用户执行 `juicefs mount` 命令
2. 安装 `fuse` 包（例如 `apt-get install fuse`、`yum install fuse`）

而如果当前用户不具备 `fusermount` 命令的执行权限，则还会遇到以下错误：

```
fuse: fuse: fork/exec /usr/bin/fusermount: permission denied
```

此时可以通过下面的命令检查 `fusermount` 命令的权限：

```shell
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount
```

上面的例子表示只有 root 用户和 `fuse` 用户组的用户有权限执行。另一个例子：

```shell
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

上面的例子表示所有用户都有权限执行。

## 与对象存储通信不畅（网速慢） {#io-error-object-storage}

如果无法访问对象存储，或者仅仅是网速太慢，JuiceFS 客户端也会发生读写错误。你也可以在日志中找到相应的报错。

```text {2,19}
# 上传块的速度不符合预期
<INFO>: slow request: PUT chunks/1986/1986377/1986377131_11_4194304 (%!s(<nil>), 20.512s)

# flush 超时通常意味着对象存储上传失败
<ERROR>: flush 9902558 timeout after waited 8m0s [writedata.go:604]
<ERROR>: pending slice 9902558-80: {chd:0xc0007125c0 off:0 chunkid:1986377183 cleng:12128803 soff:0 slen:12128803 writer:0xc010a92240 freezed:true done:false status:0 notify:0xc010e57d70 started:{wall:13891666526241100901 ext:5140404761832 loc:0x35177c0} lastMod:{wall:13891666526250536970 ext:5140414197911 loc:0x35177c0}} [writedata.go:607]
...
```

如果是网络异常导致无法访问，或者对象存储本身服务异常，问题排查相对简单。但在如果是在低带宽场景下希望优化 JuiceFS 的使用体验，需要留意的事情就稍微多一些。

首先，在网速慢的时候，JuiceFS 客户端上传/下载文件容易超时（类似上方的错误日志），这种情况下可以考虑：

* 降低上传并发度，比如 [`--max-uploads=1`](../reference/command_reference.md#mount)，避免上传超时。
* 降低读写缓冲区大小，比如 [`--buffer-size=64`](../reference/command_reference.md#mount) 或者更小。当带宽充裕时，增大读写缓冲区能提升并发性能。但在低带宽场景下使用过大的读写缓冲区，`flush` 的上传时间会很长，因此容易超时。
* 默认 GET / PUT 请求超时时间为 60 秒，因此增大 `--get-timeout` 以及 `--put-timeout`，可以改善读写超时的情况。

此外，低带宽环境下需要慎用[「客户端写缓存」](../guide/cache_management.md#writeback)特性。先简单介绍一下 JuiceFS 的后台任务设计：每个 JuiceFS 客户端默认都启用后台任务，后台任务中会执行碎片合并（Compaction）、异步删除等工作，而如果节点网络状况太差，则会降低系统整体性能。更糟的是如果该节点还启用了客户端写缓存，则容易出现碎片合并后上传缓慢，导致其他节点无法读取该文件的危险情况：

```text {6,8}
# 由于 writeback，碎片合并后的结果迟迟上传不成功，导致其他节点读取文件报错
<WARNING>: readworker: unexpected data block size (requested: 4194304 / received: 0)
<ERROR>: read for inode 0:14029704 failed after tried 30 times
<ERROR>: read file 14029704: input/output error
<INFO>: slow operation: read (14029704,131072,0): input/output error (0) <74.147891>
<WARNING>: fail to read chunkid 1771585458 (off:4194304, size:4194304, clen: 37746372): get chunks/1771/1771585/1771585458_1_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB058C0B5C3134CB80B6
<WARNING>: readworker: unexpected data block size (requested: 4194304 / received: 0)
<WARNING>: fail to read chunkid 1771585458 (off:0, size:4194304, clen: 37746372): get chunks/1771/1771585/1771585458_0_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB05AC30323537AD735D
```

为了避免此类问题，我们推荐在低带宽节点上禁用后台任务，也就是为挂载命令添加 [`--no-bgjob`](../reference/command_reference.md#mount) 参数。

## 读放大 {#read-amplification}

在 JuiceFS 中，一个典型的读放大现象是：对象存储的下行流量，远大于实际读文件的速度。比方说 JuiceFS 客户端的读吞吐为 200MiB/s，但是在 S3 观察到了 2GB/s 的下行流量。

JuiceFS 中内置了预读（prefetch）机制：随机读 block 的某一段，会触发整个 block 下载，这个默认开启的读优化策略，在某些场景下会带来读放大。了解这个设计以后，我们就可以开始排查了。

结合先前故障诊断和分析一章中介绍的[访问日志](./fault_diagnosis_and_analysis.md#oplog)知识，我们可以采集一些访问日志来分析程序的读模式，然后针对性地调整配置。下面是一个实际生产环境案例的排查过程：

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
2022.09.22 08:55:21.013121 [uid:0,gid:0,pid:0] read (148153116,131072,28668010496,19235): OK (131072) <1.309992>
2022.09.22 08:55:21.577944 [uid:0,gid:0,pid:0] read (148153116,131072,14342746112,19235): OK (131072) <1.385073>
2022.09.22 08:55:22.098133 [uid:0,gid:0,pid:0] read (148153116,131072,35781816320,19235): OK (131072) <1.301371>
2022.09.22 08:55:22.883285 [uid:0,gid:0,pid:0] read (148153116,131072,3570397184,19235): OK (131072) <1.305064>
2022.09.22 08:55:23.362654 [uid:0,gid:0,pid:0] read (148153116,131072,100420673536,19235): OK (131072) <1.264290>
2022.09.22 08:55:24.068733 [uid:0,gid:0,pid:0] read (148153116,131072,48602152960,19235): OK (131072) <1.185206>
2022.09.22 08:55:25.351035 [uid:0,gid:0,pid:0] read (148153116,131072,60529270784,19235): OK (131072) <1.282066>
2022.09.22 08:55:26.631518 [uid:0,gid:0,pid:0] read (148153116,131072,4255297536,19235): OK (131072) <1.280236>
2022.09.22 08:55:27.724882 [uid:0,gid:0,pid:0] read (148153116,131072,715698176,19235): OK (131072) <1.093108>
2022.09.22 08:55:31.049944 [uid:0,gid:0,pid:0] read (148153116,131072,8233349120,19233): OK (131072) <1.020763>
2022.09.22 08:55:32.055613 [uid:0,gid:0,pid:0] read (148153116,131072,119523176448,19233): OK (131072) <1.005430>
2022.09.22 08:55:32.056935 [uid:0,gid:0,pid:0] read (148153116,131072,44287774720,19233): OK (131072) <0.001099>
2022.09.22 08:55:33.045164 [uid:0,gid:0,pid:0] read (148153116,131072,1323794432,19233): OK (131072) <0.988074>
2022.09.22 08:55:36.502687 [uid:0,gid:0,pid:0] read (148153116,131072,47760637952,19235): OK (131072) <1.184290>
2022.09.22 08:55:38.525879 [uid:0,gid:0,pid:0] read (148153116,131072,53434183680,19203): OK (131072) <0.096732>
```

对着日志观察下来，发现读文件的行为大体上是「频繁随机小读」。我们尤其注意到 offset（也就是 `read` 的第三个参数）跳跃巨大，说明相邻的读操作之间跨度很大，难以利用到预读提前下载下来的数据（默认的块大小是 4MiB，换算为 4194304 字节的 offset）。也正因此，我们建议将 `--prefetch` 调整为 0（让预读并发度为 0，也就是禁用该行为），并重新挂载。这样一来，在该场景下的读放大问题得到很好的改善。

## 内存占用过高 {#memory-optimization}

如果 JuiceFS 客户端内存占用过高，考虑按照以下方向进行排查调优，但也请注意，内存优化势必不是免费的，每一项设置调整都将带来相应的开销，请在调整前做好充分的测试与验证。

* 读写缓冲区（也就是 `--buffer-size`）的大小，直接与 JuiceFS 客户端内存占用相关，因此可以通过降低读写缓冲区大小来减少内存占用，但请注意降低以后可能同时也会对读写性能造成影响。更多详见「[读写缓冲区](../guide/cache_management.md#buffer-size)」。
* JuiceFS 挂载客户端是一个 Go 程序，因此也可以通过降低 `GOGC`（默认 100）来令 Go 在运行时执行更为激进的垃圾回收（将带来更多 CPU 消耗，甚至直接影响性能）。详见「[Go Runtime](https://pkg.go.dev/runtime#hdr-Environment_Variables)」。
* 如果你使用自建的 Ceph RADOS 作为 JuiceFS 的数据存储，可以考虑将 glibc 替换为 [TCMalloc](https://google.github.io/tcmalloc)，后者有着更高效的内存管理实现，能在该场景下有效降低堆外内存占用。

## 开发相关问题

编译 JuiceFS 需要 GCC 5.4 及以上版本，版本过低可能导致类似下方报错：

```
/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1
/go/pkg/tool/linux_amd64/compile: signal: killed
```
