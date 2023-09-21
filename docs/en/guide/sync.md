---
title: Data Synchronization
sidebar_position: 7
---

[`juicefs sync`](../reference/command_reference.md#sync) is a powerful data migration tool, which can copy data across all supported storages including object storage, JuiceFS itself, and local file systems, you can freely copy data between any of these systems. In addition, it supports remote directories through SSH, HDFS, WebDAV, etc. while providing advanced features such as  incremental synchronization, and pattern matching (like rsync), and distributed syncing.

## Basic Usage

### Command Syntax

```shell
juicefs sync [command options] SRC DST
```

Synchronize data from `SRC` to `DST`, capable for both directories and files.

Arguments:

- `SRC` is the source data address or path;
- `DST` is the destination address or path;
- `[command options]` are synchronization options. See [command reference](../reference/command_reference.md#sync) for more details.

Address format:

```shell
[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]

# MinIO only supports path style
minio://[ACCESS_KEY:SECRET_KEY[:TOKEN]@]ENDPOINT/BUCKET[/PREFIX]
```

Explanation:

- `NAME` is the storage type like `s3` or `oss`. See [available storage services](../reference/how_to_set_up_object_storage.md#supported-object-storage) for more details;
- `ACCESS_KEY` and `SECRET_KEY` are the credentials for accessing object storage APIs; If special characters are included, it needs to be escaped and replaced manually. For example, `/` needs to be replaced with its escape character `%2F`.
- `TOKEN` token used to access the object storage, as some object storage supports the use of temporary token to obtain permission for a limited time
- `BUCKET[.ENDPOINT]` is the address of the object storage;
- `PREFIX` is the common prefix of the directories to synchronize, optional.

Here is an example of the object storage address of Amazon S3.

```
s3://ABCDEFG:HIJKLMN@myjfs.s3.us-west-1.amazonaws.com
```

In particular, `SRC` and `DST` ending with a trailing `/` are treated as directories, e.g. `movie/`. Those don't end with a trailing `/` are treated as _prefixes_, and will be used for pattern matching. For example, assuming we have `test` and `text` directories in the current directory, the following command can synchronize them into the destination `~/mnt/`.

```shell
juicefs sync ./te ~/mnt/te
```

In this way, the subcommand `sync` takes `te` as a prefix to find all the matching directories, i.e. `test` and `text`. `~/mnt/te` is also a prefix, and all directories and files synchronized to this destination will be renamed by replacing the original prefix `te` with the new prefix `te`. The changes in the names of directories and files before and after synchronization cannot be seen in the above example. However, if we take another prefix, for example, `ab`,

```shell
juicefs sync ./te ~/mnt/ab
```

the `test` directory synchronized to the destination directory will be renamed as `abst`, and `text` will be `abxt`.

### Required Storages {#required-storages}

Assume that we have the following storages.

1. **Object Storage A**
   - Bucket name: aaa
   - Endpoint: `https://aaa.s3.us-west-1.amazonaws.com`

2. **Object Storage B**
   - Bucket name: bbb
   - Endpoint: `https://bbb.oss-cn-hangzhou.aliyuncs.com`

3. **JuiceFS File System**
   - Metadata Storage: `redis://10.10.0.8:6379/1`
   - Object Storage: `https://ccc-125000.cos.ap-beijing.myqcloud.com`

All of the storages share the same **secret key**:

- **ACCESS_KEY**: `ABCDEFG`
- **SECRET_KEY**: `HIJKLMN`

### Synchronize between Object Storage and JuiceFS

The following command synchronizes `movies` directory on [Object Storage A](#required-storages) to [JuiceFS File System](#required-storages).

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronize
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

The following command synchronizes `images` directory from [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages).

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronization
juicefs sync /mnt/jfs/images/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/images/
```

### Synchronize between Object Storages

The following command synchronizes all of the data on [Object Storage A](#required-storages) to [Object Storage B](#required-storages).

```shell
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

### Sync Without Mount Point <VersionAdd>1.1</VersionAdd>

For data migrations that involve JuiceFS, it's recommended use the `jfs://` protocol, rather than mount JuiceFS and access its local directory, which bypasses the FUSE mount point and access JuiceFS directly. Under large scale scenarios, bypassing FUSE can save precious resources and increase performance.

```shell
myfs=redis://10.10.0.8:6379/1 juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ jfs://myfs/movies/
```

## Advanced Usage

## Observation {#observation}

Simply put, when using `sync` to transfer big files, progress bar might move slowly or get stuck. If this happens, you can observe the progress using other methods.

`sync` assumes it's mainly used to copy a large amount of files, its progress bar is designed for this scenario: progress only updates when a file has been transferred. In a large file scenario, every file is transferred slowly, hence the slow or even static progress bar. This is worse for destinations without multipart upload support (e.g. `file`, `sftp`, `jfs`, `gluster` schemes), where every file is transferred single-threaded.

If progress bar is not moving, use below methods to observe and troubleshoot:

* If either end is a JuiceFS mount point, you can use [`juicefs stats`](../administration/fault_diagnosis_and_analysis.md#stats) to quickly check current IO status.
* If destination is a local disk, look for temporary files that end with `.tmp.xxx`, these are the temp files created by `sync`, they will be renamed upon transfer complete. Look for size changes in temp files to verify the current IO status.
* If both end are object storage services, use tools like `nethogs` to check network IO.

### Incremental and Full Synchronization

The subcommand `sync` works incrementally by default, which compares the differences between the source and target paths, and then synchronizes only the differences. You can add option `--update` or `-u` to keep updated the `mtime` of the synchronized directories and files.

For full synchronization, i.e. synchronizing all the time no matter whether the destination files exist or not, you can add option `--force-update` or `-f`. For example, the following command fully synchronizes `movies` directory from [Object Storage A](#required-storages) to [JuiceFS File System](#required-storages).

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# full synchronization
juicefs sync --force-update s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

### Pattern Matching

The pattern matching function of the subcommand `sync` is similar to that of `rsync`, which allows you to exclude or include certain classes of files by rules and synchronize any set of files by combining multiple rules. Now we have the following rules available.

- Patterns ending with `/` only matches directories; otherwise, they match files, links or devices.
- Patterns containing `*`, `?` or `[` match as wildcards, otherwise, they match as regular strings;
- `*` matches any non-empty path components (it stops at `/`).
- `?` matches any single character except `/`;
- `[` matches a set of characters, for example `[a-z]` or `[[:alpha:]]`;
- Backslashes can be used to escape characters in wildcard patterns, while they match literally when no wildcards are present.
- It is always matched recursively using patterns as prefixes.

#### Exclude Directories/Files

Option `--exclude` can be used to exclude patterns. The following example shows a full synchronization from [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages), excluding hidden directories and files:

:::note Remark
Linux regards a directory or a file with a name starts with `.` as hidden.
:::

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# full synchronization, excluding hidden directories and files
juicefs sync --exclude '.*' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

You can use this option several times with different parameters in the command to exclude multiple patterns. For example, using the following command can exclude all hidden files, `pic/` directory and `4.png` file in synchronization:

```shell
juicefs sync --exclude '.*' --exclude 'pic/' --exclude '4.png' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

#### Include Directories/Files

Option `--include` can be used to include patterns you don't want to exclude. For example, only `pic/` and `4.png` are synchronized and all the others are excluded after executing the following command:

```shell
juicefs sync --include 'pic/' --include '4.png' --exclude '*' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

:::info NOTICE
The earlier options have higher priorities than the latter ones. Thus, the `--include` options should come before `--exclude`. Otherwise, all the `--include` options such as `--include 'pic/' --include '4.png'` which appear later than `--exclude '*'` will be ignored.
:::

### Directory Structure and File Permissions

The subcommand `sync` only synchronizes file objects and directories containing file objects, and skips empty directories by default. To synchronize empty directories, you can use `--dirs` option.

In addition, when synchronizing between file systems such as local, SFTP and HDFS, option `--perms` can be used to synchronize file permissions from the source to the destination.

### Copy Symbolic Links

You can use `--links` option to disable symbolic link resolving when synchronizing **local directories**. That is, synchronizing only the symbolic links themselves rather than the directories or files they are pointing to. The new symbolic links created by the synchronization refer to the same paths as the original symbolic links without any conversions, no matter whether their references are reachable before or after the synchronization.

Some details need to be noticed

1. The `mtime` of a symbolic link will not be synchronized;
2. `--check-new` and `--perms` will be ignored when synchronizing symbolic links.

## Concurrent data synchronization {#concurrent-sync}

`juicefs sync` by default starts 10 threads to run syncing jobs, you can set the `--threads` option to increase or decrease the number of threads as needed. But also note that due to various factors, blindly increasing `--threads` may not always work, and you should also consider:

* `SRC` and `DST` storage systems may have already reached their bandwidth limits, if this is indeed the bottleneck, further increasing concurrency will not improve the situation;
* Performing `juicefs sync` on a single host may be limited by host resources, e.g. CPU or network throttle, if this is the case, consider using [distributed synchronization](#distributed-sync) (introduced below);
* If the synchronized data is mainly small files, and the `list` API of `SRC` storage system has excellent performance, then the default single-threaded `list` of `juicefs sync` may become a bottleneck. You can consider enabling [concurrent `list`](#concurrent-list) (introduced below).

### Concurrent `list` {#concurrent-list}

From the output of `juicefs sync`, pay attention to the `Pending objects` count, if this value stays zero, consumption is faster than production and you should increase `--list-threads` to enable concurrent `list`, and then use `--list-depth` to control `list` depth.

For example, if you're dealing with a object storage bucket used by JuiceFS, directory structure will be `/<vol-name>/chunks/xxx/xxx/...`, using `--list-depth=2` will perform concurrent listing on `/<vol-name>/chunks` which usually renders the best performance.

### Distributed synchronization {#distributed-sync}

Synchronizing between two object storages is essentially pulling data from one and pushing it to the other. The efficiency of the synchronization will depend on the bandwidth between the client and the cloud.

![JuiceFS-sync-single](../images/juicefs-sync-single.png)

When copying large scale data, node bandwidth can easily bottleneck the synchronization process. For this scenario, `juicefs sync` provides a multi-machine concurrent solution, as shown in the figure below.

![JuiceFS-sync-worker](../images/juicefs-sync-worker.png)

Manager node executes `sync` command as the master, and defines multiple worker nodes by setting option `--worker` (manager node itself also serve as a worker node). JuiceFS will split the workload distribute to Workers for distributed synchronization. This increases the amount of data that can be processed per unit time, and the total bandwidth is also multiplied.

When using distributed syncing, you should configure SSH logins so that the manager can access all worker nodes without password, if SSH port isn't the default 22, you'll also have to include that in the manager's `~/.ssh/config`. Manager will distribute the JuiceFS Client to all worker nodes, so they should all use the same architecture to avoid running into compatibility problems.

For example, synchronize data from [Object Storage A](#required-storages) to [Object Storage B](#required-storages) concurrently with multiple machines.

```shell
juicefs sync --worker bob@192.168.1.20,tom@192.168.8.10 s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

The synchronization workload between the two object storages is shared by the current machine and the two Workers `bob@192.168.1.20` and `tom@192.168.8.10`.

## Application Scenarios

### Geo-disaster Recovery Backup

Geo-disaster recovery backup backs up files, and thus the files stored in JuiceFS should be synchronized to other object storages. For example, synchronize files in [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages):

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronization
juicefs sync /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

After sync, you can see all the files in [Object Storage A](#required-storages).

### Build a JuiceFS Data Copy

Unlike the file-oriented disaster recovery backup, the purpose of creating a copy of JuiceFS data is to establish a mirror with exactly the same content and structure as the JuiceFS data storage. When the object storage in use fails, you can switch to the data copy by modifying the configurations. Note that only the file data of the JuiceFS file system is replicated, and the metadata stored in the metadata engine still needs to be backed up.

This requires manipulating the underlying object storage directly to synchronize it with the target object storage. For example, to take the [Object Storage B](#required-storages) as the data copy of the [JuiceFS File System](#required-storages):

```shell
juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

After sync, the file content and hierarchy in the [Object Storage B](#required-storages) are exactly the same as the [underlying object storage of JuiceFS](#required-storages).
