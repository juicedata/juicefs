# FAQ

## Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](guide/how_to_set_up_object_storage.md#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

## Does support Redis in Sentinel or Cluster-mode as the metadata engine for JuiceFS?

Yes，There is also a [best practice document](administration/metadata/redis_best_practices.md) for Redis as the JuiceFS metadata engine for reference.

## What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](introduction/comparison/juicefs_vs_alluxio.md) for more information.

## How is the performance of JuiceFS?

JuiceFS is a distributed file system, the latency of metedata is determined by 1 (reading) or 2 (writing) round trip(s) between client and metadata service (usually 1-3 ms). The latency of first byte is determined by the performance of underlying object storage (usually 20-100 ms). Throughput of sequential read/write could be 50MB/s - 2800MiB/s (see [fio benchmark](benchmark/fio.md) for more information), depends on network bandwidth and how the data could be compressed.

JuiceFS is built with multiple layers of caching (invalidated automatically), once the caching is warmed up, the latency and throughput of JuiceFS could be close to local filesystem (having the overhead of FUSE).

## Why can't I see the original files that have been stored in JuiceFS in object storage?

While using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you may notice that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform; instead, there are only a directory of chunks and a bunch of directories and files named by numbers in the bucket. Don't panic! That's exactly what makes JuiceFS a high-performance file system. For details, please refer to ["How JuiceFS Store Files"](introduction/architecture.md#how-juicefs-stores-files).

## Does JuiceFS support random read/write?

Yes, including those issued using mmap. Currently JuiceFS is optimized for sequential reading/writing, and optimized for random reading/writing is work in progress. If you want better random reading performance, it's recommended to turn off compression ([`--compress none`](reference/command_reference.md#juicefs-format)).

## What is the implementation principle of JuiceFS supporting random write?

JuiceFS does not store the original file in the object storage, but splits it into N data blocks according to a certain size (4MiB by default), uploads it to the object storage, and stores the ID of the data block in the metadata engine. When writing randomly, it is logical to overwrite the original content. In fact, the metadata of the **data block to be overwritten** is marked as old data, and only the **new data block** generated during random writing is uploaded to the object storage, and update the metadata corresponding to the **new data block** to the metadata engine.

When reading the data of the overwritten part, according to the **latest metadata**, it can be read from the **new data block** uploaded during random writing, and the **old data block** may be deleted by the background garbage collection tasks automatically clean up. This shifts the complexity of random writes to the complexity of reads.

This is just a rough introduction to the implementation logic. The specific read and write process is very complicated. You can study the two documents ["JuiceFS Internals"](development/data_structures.md) and ["Data Processing Flow"](introduction/io_processing.md) and comb them together with the code.

## Why do I delete files at the mount point, but there is no change or very little change in object storage footprint?

The first reason is that you may have enabled the trash feature. In order to ensure data security, the trash is enabled by default. The deleted files are actually placed in the trash and are not actually deleted, so the size of the object storage will not change. trash retention time can be specified with `juicefs format` or modified with `juicefs config`. Please refer to the ["Trash"](security/trash.md) documentation for more information.

The second reason is that JuiceFS deletes the data in the object storage asynchronously, so the space change of the object storage will be slower. If you need to immediately clean up the data in the object store that needs to be deleted, you can try running the [`juicefs gc`](reference/command_reference.md#juicefs-gc) command.

## Why is the size displayed at the mount point different from the object storage footprint?

From the answer to this question ["What is the implementation principle of JuiceFS supporting random write?"](#what-is-the-implementation-principle-of-juicefs-supporting-random-write), it can be inferred that the occupied space of object storage is greater than or equal to the actual size in most cases, especially after a large number of overwrites in a short period of time generate many file fragments. These fragments still occupy space in object storage until merges and reclamations are triggered. But don't worry about these fragments occupying space all the time, because every time you read/write a file, it will check and trigger the defragmentation of the file when necessary. Alternatively, you can manually trigger merges and recycles with the `juicefs gc --compact --delete` command.

In addition, if the JuiceFS file system has compression enabled (not enabled by default), the objects stored on the object storage may be smaller than the actual file size (depending on the compression ratio of different types of files).

If the above factors have been excluded, please check the [storage class](guide/how_to_set_up_object_storage.md#storage-class) of the object storage you are using. The cloud service provider may set the minimum billable size for some storage classes. For example, the [minimum billable size](https://www.alibabacloud.com/help/en/object-storage-service/latest/storage-fees) of Alibaba Cloud OSS IA storage is 64KB. If a single file is smaller than 64KB, it will be calculated as 64KB.

## When my update will be visible to other clients?

All the metadata updates are immediately visible to all others. JuiceFS guarantees close-to-open consistency, see ["Consistency"](guide/cache_management.md#data-consistency) for more information.

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

## `Resource busy -- try 'diskutil unmount'` error when unmounting the mount point

This means that a file or directory under the mount point is in use and cannot be `umount` directly. You can check (such as through the `lsof` command) whether an open terminal is located in a directory on the JuiceFS mount point, or an application is processing files in the mount point. If so, exit the terminal or application and try to unmount the file system using the `juicefs umount` command.

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

## `cannot update volume XXX from XXX to XXX`
The meta database has already been formatted and previous configuration cannot be updated by this `format`. You can execute the `juicefs format` command after manually cleaning up the meta database.

## Why the same user on host X has permission to access a file in JuiceFS while has no permission to it on host Y?

The same user has different UID or GID on host X and host Y. Use `id` command to show the UID and GID:

```bash
$ id alice
uid=1201(alice) gid=500(staff) groups=500(staff)
```

Read ["Sync Accounts between Multiple Hosts"](administration/sync_accounts_between_multiple_hosts.md) to resolve this problem.

## What other ways JuiceFS supports access to data besides mount?

In addition to mounting, the following methods are also supported:

- Kuberenetes CSI Driver: Use JuiceFS as the storage layer of Kubernetes cluster through the Kubernetes CSI Driver. For details, please refer to ["Use JuiceFS on Kubernetes"](deployment/how_to_use_on_kubernetes.md).
- Hadoop Java SDK: It is convenient to use a Java client compatible with the HDFS interface to access JuiceFS in the Hadoop ecosystem. For details, please refer to ["Use JuiceFS on Hadoop Ecosystem"](deployment/hadoop_java_sdk.md).
- S3 Gateway: Access JuiceFS through the S3 protocol. For details, please refer to ["Deploy JuiceFS S3 Gateway"](deployment/s3_gateway.md).
- Docker Volume Plugin: A convenient way to use JuiceFS in Docker, please refer to ["Use JuiceFS on Docker"](deployment/juicefs_on_docker.md).
- WebDAV Gateway: Access JuiceFS via WebDAV protocol

## Where is the JuiceFS log?

Different types of JuiceFS clients have different ways to obtain logs. For details, please refer to ["Client log"](administration/fault_diagnosis_and_analysis.md#client-log) document.

## How to destroy a file system?

Destroy a file system with `juicefs destroy` command, which will clear the metadata engine and object storage related data. For details about the use of this command, please refer to the [document](administration/destroy.md).

## Does JuiceFS Gateway support advanced features such as multi-user management?

The built-in `gateway` subcommand of JuiceFS does not support functions such as multi-user management, and only provides basic S3 gateway functions. If you need to use these advanced features, please refer to the [documentation](deployment/s3_gateway.md#use-a-full-featured-s3-gateway).

## Does JuiceFS support using a directory in object storage as the value of the `--bucket` option?

As of the release of JuiceFS 1.0, this feature is not supported.

## Does JuiceFS support accessing data that already exists in object storage?

As of the release of JuiceFS 1.0, this feature is not supported.

## Does JuiceFS currently support distributed caching?

As of the release of JuiceFS 1.0, this feature is not supported.

## Is there currently an SDK available for JuiceFS?

As of the release of JuiceFS 1.0, the community has two SDKs, one is the [Java SDK](deployment/hadoop_java_sdk.md) that is highly compatible with the HDFS interface officially maintained by Juicedata, and the other is the [Python SDK](https://github.com/megvii-research/juicefs-python) maintained by community users.
