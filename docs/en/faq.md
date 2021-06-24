# FAQ

## Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](how_to_setup_object_storage.md#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

## Can I use Redis cluster?

The simple answer is no. JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, which is not well supported in cluster mode. Sentinal or other HA solution for Redis are needed.

See ["Redis Best Practices"](redis_best_practices.md) for more information.

## What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](comparison_with_others.md) for more information.

## How is the performance of JuiceFS?

JuiceFS is a distributed file system, the latency of metedata is determined by 1 (reading) or 2 (writing) round trip(s) between client and metadata service (usually 1-3 ms). The latency of first byte is determined by the performance of underlying object storage (usually 20-100 ms). Throughput of sequential read/write could be 50MB/s - 2800MiB/s (see [fio benchmark](fio.md) for more information), depends on network bandwidth and how the data could be compressed.

JuiceFS is built with multiple layers of caching (invalidated automatically), once the caching is warmed up, the latency and throughput of JuiceFS could be close to local filesystem (having the overhead of FUSE).

## Does JuiceFS support random read/write?

Yes, including those issued using mmap. Currently JuiceFS is optimized for sequential reading/writing, and optimized for random reading/writing is work in progress. If you want better random reading performance, it's recommended to turn off compression ([`--compress none`](command_reference.md#juicefs-format)).

## When my update will be visible to other clients?

All the metadata updates are immediately visible to all others. JuiceFS guarantees close-to-open consistency, see ["Consistency"](cache_management.md#consistency) for more information.

The new data written by `write()` will be buffered in kernel or client, visible to other processes on the same machine, not visible to other machines.

Either call `fsync()`, `fdatasync()` or `close()` to force upload the data to the object storage and update the metadata, or after several seconds of automatic refresh, other clients can visit the updates. It is also the strategy adopted by the vast majority of distributed file systems.

See ["Write Cache in Client"](cache_management.md#write-cache-in-client) for more information.

## How to copy a large number of small files into JuiceFS quickly?

You could mount JuiceFS with [`--writeback` option](command_reference.md#juicefs-mount), which will write the small files into local disks first, then upload them to object storage in background, this could speedup coping many small files into JuiceFS.

See ["Write Cache in Client"](cache_management.md#write-cache-in-client) for more information.

## Can I mount JuiceFS without `root`?

Yes, JuiceFS could be mounted using `juicefs` without root. The default directory for caching is `$HOME/.juicefs/cache` (macOS) or `/var/jfsCache` (Linux), you should change that to a directory which you have write permission.

See ["Read Cache in Client"](cache_management.md#read-cache-in-client) for more information.

## How to unmount JuiceFS?

Use [`juicefs umount`](command_reference.md#juicefs-umount) command to unmount the volume.

## How to upgrade JuiceFS client?

First unmount JuiceFS volume, then re-mount the volume with newer version client.

## `docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.`

When you use [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) to mount a directory on the host machine into a container, you may encounter this error. The reason is that `juicefs mount` command was executed with non-root user. In turn, Docker daemon doesn't have permission to access the directory.

There are two solutions to this problem:

1. Execute `juicefs mount` command with root user
2. Modify FUSE configuration and add `allow_other` mount option, see [this document](fuse_mount_options.md#allow_other) for more information.

## `/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1` or `/go/pkg/tool/linux_amd64/compile: signal: killed`

This error may caused by GCC version is too low, please try to upgrade your GCC to 5.4+.

## `format: ERR wrong number of arguments for 'auth' command`

This error means you use Redis < 6.0.0 and specify username in Redis URL when execute `juicefs format` command. Only Redis >= 6.0.0 supports specify username, so you need omit the username parameter in the URL, e.g. `redis://:password@host:6379/1`.

## `fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory`

This error means `juicefs mount` command was executed with non-root user, and `fusermount` command cannot found.

There are two solutions to this problem:

1. Execute `juicefs mount` command with root user
2. Install `fuse` package (e.g. `apt-get install fuse`, `yum install fuse`)

## `fuse: fuse: fork/exec /usr/bin/fusermount: permission denied`

This error means current user doesn't have permission to execute `fusermount` command. For example, check `fusermount` permission with following command:

```sh
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount
```

Above example means only root user and `fuse` group user have executable permission. Another example:

```sh
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

Above example means all users have executable permission.

## Why the same user on host X has permission to access a file in JuiceFS while has no permission to it on host Y?

The same user has different UID or GID on host X and host Y. Use `id` command to show the UID and GID:

```bash
$ id alice
uid=1201(alice) gid=500(staff) groups=500(staff)
```

Read ["Sync Accounts between Multiple Hosts"](sync_accounts_between_multiple_hosts.md) to resolve this problem.
