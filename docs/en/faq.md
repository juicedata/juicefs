# FAQ

## Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](guide/how_to_setup_object_storage.md#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

## Does Redis support sentinel or Cluster mode as JuiceFS metadata engine?

Yes，There is a [best practices article](https://juicefs.com/docs/community/faq/) on Redis as a JuiceFS metadata engine.

## What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](introduction/comparison/juicefs_vs_alluxio.md) for more information.

## How is the performance of JuiceFS?

JuiceFS is a distributed file system, the latency of metedata is determined by 1 (reading) or 2 (writing) round trip(s) between client and metadata service (usually 1-3 ms). The latency of first byte is determined by the performance of underlying object storage (usually 20-100 ms). Throughput of sequential read/write could be 50MB/s - 2800MiB/s (see [fio benchmark](benchmark/fio.md) for more information), depends on network bandwidth and how the data could be compressed.

JuiceFS is built with multiple layers of caching (invalidated automatically), once the caching is warmed up, the latency and throughput of JuiceFS could be close to local filesystem (having the overhead of FUSE).

## Why don't you see the original file to JuiceFS in the object store?

While using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you may notice that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform; instead, there are only a directory of chunks and a bunch of directories and files named by numbers in the bucket. Don't panic! That's exactly what makes JuiceFS a high-performance file system. For details, please refer to [How to store files in JuiceFS](https://juicefs.com/docs/community/how_juicefs_store_files).

## Does JuiceFS support random read/write?

Yes, including those issued using mmap. Currently JuiceFS is optimized for sequential reading/writing, and optimized for random reading/writing is work in progress. If you want better random reading performance, it's recommended to turn off compression ([`--compress none`](reference/command_reference.md#juicefs-format)).

## What is the rationale for JuiceFS random writes?

Instead of passing the raw object into the object store, JuiceFS splits it into N blocks of 4M size, numbers it and uploads it to the object store, and stores the numbers into the metadata engine. When a random write is performed, it logically overwrites the original content, but actually marks the overwrite as old data and uploads the random write content to the object store.  When it's time to read the old data, just read the new data from the random part that you just uploaded. When a random write is performed, it logically overwrites the original content, but actually marks the overwrite as old data and uploads the random write content to the object store. This shifts the complexity of random writes to the complexity of reads. This is only a macro implementation logic, you can read [JuiceFS internal implementation](https://juicefs.com/docs/community/internals) and [read and write process](https://juicefs.com/docs/community/internals/io_processing/) for details of logic and coordinate with code combing.

## Why do I delete files at the mount point, but there is no change or very little change in object storage footprint?

The first reason is that you might enable trash bin, and for data security, the trash bin is open by default, so the deleted files are actually put in the trash bin, but they're not actually deleted, so the size of the object store doesn't change. Trash bin can be specified by `juicefs format` or modified by `juicefs config`.Please refer to the [trash bin usage documentation](https://juicefs.com/docs/community/security/trash) for more information about the trash bin function.
The second reason is that JuiceFS deletes object stores asynchronously. Therefore, the object storage footprint changes slowly.

## Why is the size displayed at the mount point different from the object storage footprint?

It can be inferred from the answer to the question [ What is the rationale for JuiceFS random writes ](#What is the rationale for JuiceFS random writes?) that the size of the object store is in most cases greater than or equal to the actual size, especially if a large number of write overwrites in a short period of time result in many file fragments. These fragments still occupy space in the object store until merge and reclaim are triggered. However, you don't have to worry about these fragments taking up space all the time, because every time the file is read, it will be checked and triggered when necessary. Alternatively, you can manually trigger merge and reclaim with the `juicefs gc  --compact --delete` command.

## When my update will be visible to other clients?

All the metadata updates are immediately visible to all others. JuiceFS guarantees close-to-open consistency, see ["Consistency"](guide/cache_management.md#consistency) for more information.

The new data written by `write()` will be buffered in kernel or client, visible to other processes on the same machine, not visible to other machines.

Either call `fsync()`, `fdatasync()` or `close()` to force upload the data to the object storage and update the metadata, or after several seconds of automatic refresh, other clients can visit the updates. It is also the strategy adopted by the vast majority of distributed file systems.

See ["Write Cache in Client"](guide/cache_management.md#write-cache-in-client) for more information.

## How to copy a large number of small files into JuiceFS quickly?

You could mount JuiceFS with [`--writeback` option](reference/command_reference.md#juicefs-mount), which will write the small files into local disks first, then upload them to object storage in background, this could speedup coping many small files into JuiceFS.

See ["Write Cache in Client"](guide/cache_management.md#write-cache-in-client) for more information.

## Can I mount JuiceFS without `root`?

Yes, JuiceFS could be mounted using `juicefs` without root. The default directory for caching is `$HOME/.juicefs/cache` (macOS) or `/var/jfsCache` (Linux), you should change that to a directory which you have write permission.

See ["Read Cache in Client"](guide/cache_management.md#read-cache-in-client) for more information.

## How to unmount JuiceFS?

Use [`juicefs umount`](reference/command_reference.md#juicefs-umount) command to unmount the volume.

## How to upgrade JuiceFS client?

First unmount JuiceFS volume, then re-mount the volume with newer version client.

## `docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.`

When you use [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) to mount a directory on the host machine into a container, you may encounter this error. The reason is that `juicefs mount` command was executed with non-root user. In turn, Docker daemon doesn't have permission to access the directory.

There are two solutions to this problem:

1. Execute `juicefs mount` command with root user
2. Modify FUSE configuration and add `allow_other` mount option, see [this document](reference/fuse_mount_options.md#allow_other) for more information.

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

Read ["Sync Accounts between Multiple Hosts"](administration/sync_accounts_between_multiple_hosts.md) to resolve this problem.

## What other ways JuiceFS supports access to data besides normal mount?

In addition to ordinary mounting, the following modes are supported:
- S3 Gateway way: Access JuiceFS through the S3 protocol, details refer to [JuiceFS S3 Gateway using guide] (https://juicefs.com/docs/community/s3_gateway)
- Webdav mode: Access JuiceFS through Webdav protocol
  Docker Volume Plugin: Easy way to use JuiceFS in Docker, for details on how to use JuiceFS in Docker, please refer to [Docker JuiceFS Guide](https://juicefs.com/docs/community/juicefs_on_docker)
- CSI Driver: Using JuiceFS as the storage layer of the Kubernetes cluster by means of the Kubernetes CSI Driver, Details refer to [use JuiceFS guide Kubernetes](https://juicefs.com/docs/community/how_to_use_on_kubernetes)
- Hadoop SDK: Java client that is highly compatible with HDFS interfaces for use in Hadoop systems. See [using JuiceFS in Hadoop](https://juicefs.com/docs/community/hadoop_java_sdk) for details.

## Where is the JuiceFS log?

Logs are written to the log file only when JuiceFS is mounted in the background, and logs are directly printed to the terminal by foreground mount or other foreground commands.
- Log file on Mac system default is `/Users/$User/.juicefs/juicefs.log`
- On Linux, the default log file for root user startup is `/var/log/juicefs.log`, and that for non-root users is `~/.juicefs.log`.

## How to destroy a file system?
Destroy a file system with 'juicefs Destroy', which empties the metadata engine and object store of related data. Please refer to this [documentation](https://juicefs.com/docs/community/administration/destroy) for details on how to use this command.

## Does JuiceFS Gateway support advanced features such as multi-user management?

The built-in `gateway` subcommand of JuiceFS does not support functions such as multi-user management and only provides basic S3 Gateway functions. If you need to use these advanced features, you can refer to [this repository](https://github.com/juicedata/minio/tree/gateway), which uses JuiceFS as an implementation of MinIO Gateway and supports the full functionality of MinIO Gateway.

## Does JuiceFS support using a directory in the object store as a '--bucket' parameter?

As of the release of JuiceFS 1.0.0, this feature is not supported.

## Does JuiceFS support docking data that already exists in the object store?

As of the release of JuiceFS 1.0, this feature is not supported.

## Does JuiceFS currently support distributed caching?

As of the release of JuiceFS 1.0, this feature is not supported.

## Is there currently an SDK available for JuiceFS?

As of the JuiceFS 1.0 release, the community had two SDKS, one is the [Java SDK](https://juicefs.com/docs/community/hadoop_java_sdk/), which is highly compatible with HDFS interface maintained by JuiceFS official, and the other is the [Python SDK](https://github.com/megvii-research/juicefs-python) maintained by community users.
