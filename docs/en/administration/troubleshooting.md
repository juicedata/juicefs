---
title: Troubleshooting Cases
sidebar_position: 6
---

Debugging process for some frequently encountered JuiceFS problems.

## Volume format error {#format-error}

### Error creating an already formatted volume {#create-file-system-repeatedly}

If `juicefs format` has been run on the metadata engine, executing `juicefs format` command again might result in the following error:

```
cannot update volume XXX from XXX to XXX
```

In this case, clean up the metadata engine, and try again.

### Invalid Redis URL {#invalid-redis-url}

When using Redis below 6.0.0, `juicefs format` will fail when `username` is specified:

```
format: ERR wrong number of arguments for 'auth' command
```

Username is supported in Redis 6.0.0 and above, you'll need to omit the `username` from the Redis URL, e.g. `redis://:password@host:6379/1`.

### Redis Sentinel mode NOAUTH error {#redis-sentinel-noauth-error}

If you encounter the following error when using [Redis Sentinel mode](../administration/metadata/redis_best_practices.md#sentinel-mode):

```
sentinel: GetMasterAddrByName master="xxx" failed: NOAUTH Authentication required.
```

Please confirm whether [the password is set](https://redis.io/docs/management/sentinel/#configuring-sentinel-instances-with-authentication) for the Redis Sentinel instance, if it is set, then you need to pass the `SENTINEL_PASSWORD` environment variable configures the password to connect to the Sentinel instance separately, and the password in the metadata engine URL will only be used to connect to the Redis server.

## Mount errors due to permission issue {#mount-permission-error}

When using [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) to mount a directory on the host machine into a container, you may encounter the following error:

```
docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.
```

This is usually due to the `juicefs mount` command being executed with a non-root user, thus Docker daemon doesn't have permission to access this directory. You can deal with this using one of below methods:

* Execute `juicefs mount` command with root user
* Add [`allow_other`](../reference/fuse_mount_options.md#allow_other) option to both FUSE config file, and mount command.

When executing `juicefs mount` command with a non-root user, you may see:

```
fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory
```

This only occurs when a non-root user is trying to mount file system, meaning `fusermount` is not found, there are two solutions to this problem:

* Execute `juicefs mount` command with root user
* Install `fuse` package (e.g. `apt-get install fuse`, `yum install fuse`)

If current user doesn't have permission to execute `fusermount` command, you'll see:

```
fuse: fuse: fork/exec /usr/bin/fusermount: permission denied
```

When this happens, check `fusermount` permission:

```shell
# Only root user and fuse group user have executable permission
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount

# All users have executable permission
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

## Read write slow & read write error {#read-write-error}

### Connection problems with object storage (slow internet speed) {#io-error-object-storage}

If JuiceFS Client cannot connect to object storage, or the bandwidth is simply not enough, JuiceFS will complain in logs:

```text
# upload speed is slow
<INFO>: slow request: PUT chunks/0/0/1_0_4194304 (%!s(<nil>), 20.512s)

# flush timeouts usually means failure to upload data to object storage
<ERROR>: flush 9902558 timeout after waited 8m0s
<ERROR>: pending slice 9902558-80: ...
```

If the problem is a network connection issue, or the object storage has service issue, troubleshooting is relatively simple. But if the error was caused by low bandwidth, there's some more to consider.

The first issue with slow connection is upload / download timeouts (demonstrated in the above error logs), to tackle this problem:

* Reduce upload concurrency, e.g. [`--max-uploads=1`](../reference/command_reference.mdx#mount-data-storage-options), to avoid upload timeouts.
* Reduce buffer size, e.g. [`--buffer-size=64`](../reference/command_reference.mdx#mount-data-cache-options) or even lower. In a large bandwidth condition, increasing buffer size improves parallel performance. But in a low speed environment, this only makes `flush` operations slow and prone to timeouts.
* Default timeout for GET / PUT requests are 60 seconds, increasing `--get-timeout` and `--put-timeout` may help with read / write timeouts.

In addition, the ["Client Write Cache"](../guide/cache.md#client-write-cache) feature needs to be used with caution in low bandwidth environment. Let's briefly go over the JuiceFS Client background job design: every JuiceFS Client runs background jobs by default, one of which is data compaction, and if the client has poor internet speed, it'll drag down performance for the whole system. A worse case is when client write cache is also enabled, compaction results are uploaded too slowly, forcing other clients into a read hang when accessing the affected files:

```text
# While compaction results are slowly being uploaded in low speed clients, read from other clients will hang and eventually fail
<ERROR>: read file 14029704: input/output error
<INFO>: slow operation: read (14029704,131072,0): input/output error (0) <74.147891>
<WARNING>: fail to read sliceId 1771585458 (off:4194304, size:4194304, clen: 37746372): get chunks/0/0/1_0_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB058C0B5C3134CB80B6
```

To avoid this type of issue, we recommend disabling background jobs on low-bandwidth clients, i.e. adding [`--no-bgjob`](../reference/command_reference.mdx#mount-metadata-options) option to the mount command.

### WARNING log: block not found in object storage {#warning-log-block-not-found-in-object-storage}

When using JuiceFS at scale, there will be some warnings in client logs:

```
<WARNING>: fail to read sliceId 1771585458 (off:4194304, size:4194304, clen: 37746372): get chunks/0/0/1_0_4194304: oss: service returned error: StatusCode=404, ErrorCode=NoSuchKey, ErrorMessage="The specified key does not exist.", RequestId=62E8FB058C0B5C3134CB80B6
```

When this type of warning occurs, but not accompanied by I/O errors (indicated by `input/output error` in client logs), you can safely ignore them and continue normal use, client will retry automatically and resolves this issue.

This warning means that JuiceFS Client cannot read a particular slice, because a block does not exist, and object storage has to return a `NoSuchKey` error. Usually this is caused by:

* Clients carry out compaction asynchronously, which upon completion, will change the relationship between file and its corresponding blocks, causing problems for other clients that's already reading this file, hence the warning.
* Some clients enabled ["Client Write Cache"](../guide/cache.md#client-write-cache), they write a file, commit to the Metadata Service, but the corresponding blocks are still pending to upload (caused by for example, [slow internet speed](#io-error-object-storage)). Meanwhile, other clients that are already accessing this file will meet this warning.

Again, if no errors occur, just safely ignore this warning.

## Read amplification

In JuiceFS, a typical read amplification manifests as object storage traffic being much larger than JuiceFS Client read speed. For example, JuiceFS Client is reading at 200MiB/s, while S3 traffic grows up to 2GiB/s.

JuiceFS is equipped with the [prefetch mechanism](../guide/cache.md#client-read-cache): when reading a block at arbitrary position, the whole block is asynchronously scheduled for download. This is a read optimization enabled by default, but in some cases, this brings read amplification. Once we know this, we can start the diagnose.

We'll collect JuiceFS access log (see [Access log](./fault_diagnosis_and_analysis.md#access-log)) to determine the file system access patterns of our application, and adjust JuiceFS configuration accordingly. Below is a diagnose process in an actual production environment:

```shell
# Collect access log for a period of time, like 30 seconds:
cat /jfs/.accesslog | grep -v "^#$" >> access.log

# Simple analysis using wc / grep finds out that most operations are read:
wc -l access.log
grep "read (" access.log | wc -l

# Pick a file and track operation history using its inode (first argument of read):
grep "read (148153116," access.log
```

Access log looks like:

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

Studying the access log, it's easy to conclude that our application performs frequent random small reads on a very large file, notice how the offset (the third argument of `read`) jumps significantly between each read, this means consecutive reads are accessing very different parts of the large file, thus prefetched data blocks is not being effectively utilized (a block is 4MiB by default, an offset of 4194304 bytes), only causing read amplifications. In this situation, we can safely set `--prefetch` to 0, so that prefetch concurrency is zero, which is essentially disabled. Re-mount and our problem is solved.

## High memory usage {#memory-optimization}

If JuiceFS Client takes up too much memory, you may choose to optimize memory usage using below methods, but note that memory optimization is not free, and each setting adjustment will bring corresponding overhead, please do sufficient testing and verification before adjustment.

* Read/Write buffer size (`--buffer-size`) directly correlate to JuiceFS Client memory usage, using a lower `--buffer-size` will effectively decrease memory usage, but please note that the reduction may also affect the read and write performance. Read more at [Read/Write Buffer](../guide/cache.md#buffer-size).
* JuiceFS mount client is an Go program, which means you can decrease `GOGC` (default to 100, in percentage) to adopt a more active garbage collection. This inevitably increase CPU usage and may even directly hinder performance. Read more at [Go Runtime](https://pkg.go.dev/runtime#hdr-Environment_Variables).
* If you use self-hosted Ceph RADOS as the data storage of JuiceFS, consider replacing glibc with [TCMalloc](https://google.github.io/tcmalloc), the latter comes with more efficient memory management and may decrease off-heap memory footprint in this scenario.

## Unmount error {#unmount-error}

If a file or directory are opened when you unmount JuiceFS, you'll see below errors, assuming JuiceFS is mounted on `/jfs`:

```shell
# Linux
umount: /jfs: target is busy.
        (In some cases useful info about processes that use
         the device is found by lsof(8) or fuser(1))

# macOS
Resource busy -- try 'diskutil unmount'
```

In such case:

* Locate the files being opened using commands like `lsof /jfs`, deal with these processes (like force quit), and retry.
* Force close the FUSE connection by `echo 1 > /sys/fs/fuse/connections/[device-number]/abort`, and then retry. You might need to find out the `[device-number]` using `lsof /jfs`, but if JuiceFS is the only FUSE mount point in the system, then `/sys/fs/fuse/connections` will contain only a single directory, no need to check further.
* If you just want to unmount ASAP, and do not care what happens to opened files, run `juicefs umount --force` to forcibly umount, note that behavior is different between Linux and macOS:
  * For Linux, `juicefs umount --force` is translated to `umount --lazy`, file system will be detached, but opened files remain, FUSE client will exit when file descriptors are released.
  * For macOS, `juicefs umount --force` is translated to `umount -f`, file system will be forcibly unmounted and opened files will be closed immediately.

## Development related issues {#development-related-issues}

Compiling JuiceFS requires GCC 5.4 and above, this error may occur when using lower versions:

```
/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1
/go/pkg/tool/linux_amd64/compile: signal: killed
```

If glibc version is different between build environment and runtime, you may see below error:

```
$ juicefs
juicefs: /lib/aarch64-linux-gnu/libc.so.6: version 'GLIBC_2.28' not found (required by juicefs)
```

This requires you to re-compile JuiceFS Client in your runtime host environment. Most Linux distributions comes with glibc by default, you can check its version with `ldd --version`.
