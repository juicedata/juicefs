---
sidebar_label: Synchronization
position: 4
---

# Migrate and Synchronize Data across Clouds with JuiceFS Sync

The subcommand `sync` of JuiceFS is a full-featured data synchronization utility that can synchronize or migrate data concurrently with multiple threads between all [object storages JuiceFS supports](../guide/how_to_set_up_object_storage.md). It can be used to migrate data not only between _object storage_ and _JuiceFS_, but also between _object storages_ in different clouds or regions. In addition, similar to `rsync`, the JuiceFS subcommand `sync` can also be used to synchronize local directories and access remote directories through SSH, HDFS, WebDAV, etc.. It also provides advanced features such as full synchronization, incremental synchronization, and conditional pattern matching.

## Basic Usage

### Command Syntax

```shell
juicefs sync [command options] SRC DST
```

Synchronize data from `SRC` to `DST`, capable for both directories and files.

Arguments:

- `SRC` is the source data address or path;
- `DST` is the destination address or path;
- `[command options]` are synchronization options. See [command reference](../reference/command_reference.md#juicefs-sync) for more details.

Address syntax follows `[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]`.

:::tip
MinIO only supports path style, and the address format is `minio://[ACCESS_KEY:SECRET_KEY[:TOKEN]@]ENDPOINT/BUCKET[/PREFIX]`
:::

Explanation:

- `NAME` is the storage type like `s3` or `oss`. See [available storage services](../guide/how_to_set_up_object_storage.md#supported-object-storage) for more details;
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
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronize
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

For JuiceFS 1.1+, we can sync with JuiceFS without mounting it:

```shell
myfs=redis://10.10.0.8:6379/1 juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ jfs://myfs/movies/
```

The following command synchronizes `images` directory from [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages).

```shell
# mount JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronization
juicefs sync /mnt/jfs/images/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/images/
```

### Synchronize between Object Storages

The following command synchronizes all of the data on [Object Storage A](#required-storages) to [Object Storage B](#required-storages).

```shell
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

## Advanced Usage

### Incremental and Full Synchronization

The subcommand `sync` works incrementally by default, which compares the differences between the source and target paths, and then synchronizes only the differences. You can add option `--update` or `-u` to keep updated the `mtime` of the synchronized directories and files.

For full synchronization, i.e. synchronizing all the time no matter whether the destination files exist or not, you can add option `--force-update` or `-f`. For example, the following command fully synchronizes `movies` directory from [Object Storage A](#required-storages) to [JuiceFS File System](#required-storages).

```shell
# mount JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
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
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
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

### Multi-threading and Bandwidth Throttling

The subcommand `sync` enables 10 threads by default. You can customize thread count by `--thread` option.

In addition, you can set option `--bwlimit` in the unit `Mbps` to limit the bandwidth used by the synchronization. The default value is `0`, meaning that bandwidth will not be limited.

### Directory Structure and File Permissions

The subcommand `sync` only synchronizes file objects and directories containing file objects, and skips empty directories by default. To synchronize empty directories, you can use `--dirs` option.

In addition, when synchronizing between file systems such as local, SFTP and HDFS, option `--perms` can be used to synchronize file permissions from the source to the destination.

### Copy Symbolic Links

You can use `--links` option to disable symbolic link resolving when synchronizing **local directories**. That is, synchronizing only the symbolic links themselves rather than the directories or files they are pointing to. The new symbolic links created by the synchronization refer to the same paths as the original symbolic links without any conversions, no matter whether their references are reachable before or after the synchronization.

Some details need to be noticed

1. The `mtime` of a symbolic link will not be synchronized;
2. `--check-new` and `--perms` will be ignored when synchronizing symbolic links.

### Multi-machine Concurrent Synchronization

Synchronizing between two object storages is essentially pulling data from one and pushing it to the other. As shown in the figure below, the efficiency of synchronization depends on the bandwidth between the client and the cloud.

![](../images/juicefs-sync-single.png)

When synchronizing a huge amount of data, there is often a bottleneck in the synchronization since the client machine runs out of bandwidth. For this case, JuiceFS Sync provides a multi-machine concurrent solution, as shown in the figure below.

![](../images/juicefs-sync-worker.png)

Manager machine executes `sync` command as the master, and defines multiple Worker machines by setting option `--worker`. JuiceFS will dynamically split the synchronization workload according to the total number of Workers and distribute to Workers for concurrent synchronization. That is, split the synchronization workload which should originally be processed on one machine into multiple parts, and dispatch them to multiple machines for concurrent processing. This increases the amount of data that can be processed per unit time, and the total bandwidth is also multiplied.

Passwordless SSH login from Manager to Workers should be enabled before configuring multi-machine concurrent synchronization to ensure that the client programs and the synchronization workload can be successfully distributed to Workers.

:::note NOTICE
Manager distributes JuiceFS client programs to Workers. To avoid compatibility issues, please make sure the Workers use the same operating system of the same architecture as the Manager.
:::

For example, synchronize data from [Object Storage A](#required-storages) to [Object Storage B](#required-storages) concurrently with multiple machines.

```shell
juicefs sync --worker bob@192.168.1.20,tom@192.168.8.10 s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

The synchronization workload between the two object storages is shared by the current machine and the two Workers `bob@192.168.1.20` and `tom@192.168.8.10`.

:::tip Tips
Please set the SSH port in `.ssh/config` on the Manager machine if Workers don't listen on the default SSH port 22.
:::

## Application Scenarios

### Geo-disaster Recovery Backup

Geo-disaster recovery backup backs up files, and thus the files stored in JuiceFS should be synchronized to other object storages. For example, synchronize files in [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages):

```shell
# mount JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronization
sudo juicefs sync /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

After sync, you can see all the files in [Object Storage A](#required-storages).

### Build a JuiceFS Data Copy

Unlike the file-oriented disaster recovery backup, the purpose of creating a copy of JuiceFS data is to establish a mirror with exactly the same content and structure as the JuiceFS data storage. When the object storage in use fails, you can switch to the data copy by modifying the configurations. Note that only the file data of the JuiceFS file system is replicated, and the metadata stored in the metadata engine still needs to be backed up.

This requires manipulating the underlying object storage directly to synchronize it with the target object storage. For example, to take the [Object Storage B](#required-storages) as the data copy of the [JuiceFS File System](#required-storages):

```shell
juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

After sync, the file content and hierarchy in the [Object Storage B](#required-storages) are exactly the same as the [underlying object storage of JuiceFS](#required-storages).

:::tip Tips
Please read [architecture](../introduction/architecture.md) for more details about how JuiceFS stores files.
:::
