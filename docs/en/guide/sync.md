---
title: Data Synchronization
sidebar_position: 7
description: Learn how to use the data sync tool in JuiceFS.
---

[`juicefs sync`](../reference/command_reference.mdx#sync) is a powerful data migration tool, which can copy data across all supported storages including object storage, JuiceFS itself, and local file systems, you can freely copy data between any of these systems. In addition, it supports remote directories through SSH, HDFS, WebDAV, etc. while providing advanced features such as  incremental synchronization, and pattern matching (like rsync), and distributed syncing.

## Basic usage {#basic-usage}

### Command syntax {#command-syntax}

```shell
juicefs sync [command options] SRC DST
```

Arguments:

- `SRC` is the source data address or path;
- `DST` is the destination address or path;
- `[command options]` are synchronization options. See [command reference](../reference/command_reference.mdx#sync) for more details.

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

### Required storages {#required-storages}

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

### Synchronize between object storage and JuiceFS {#synchronize-between-object-storage-and-juicefs}

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

### Synchronize between object storages {#synchronize-between-object-storages}

The following command synchronizes all of the data on [Object Storage A](#required-storages) to [Object Storage B](#required-storages).

```shell
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

### Synchronize between local and remote servers {#synchronize-between-local-and-remote-servers}

To copy files between directories on a local computer, simply specify the source and destination paths. For example, to synchronize the `/media/` directory with the `/backup/` directory:

```shell
juicefs sync /media/ /backup/
```

If you need to synchronize between servers, you can access the target server using the SFTP/SSH protocol. For example, to synchronize the local `/media/` directory with the `/backup/` directory on another server:

```shell
juicefs sync /media/ username@192.168.1.100:/backup/
# Specify password (optional)
juicefs sync /media/ "username:password"@192.168.1.100:/backup/
```

When using the SFTP/SSH protocol, if no password is specified, the sync task will prompt for the password. If you want to explicitly specify the username and password, you need to enclose them in double quotation marks, with a colon separating the username and password.

### Sync without mount point <VersionAdd>1.1</VersionAdd> {#sync-without-mount-point}

For data migrations that involve JuiceFS, it's recommended use the `jfs://` protocol, rather than mount JuiceFS and access its local directory, which bypasses the FUSE mount point and access JuiceFS directly. Under large scale scenarios, bypassing FUSE can save precious resources and increase performance.

```shell
myfs=redis://10.10.0.8:6379/1 juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ jfs://myfs/movies/
```

## Advanced usage {#advanced-usage}

### Observation {#observation}

Simply put, when using `sync` to transfer big files, progress bar might move slowly or get stuck. If this happens, you can observe the progress using other methods.

`sync` assumes it's mainly used to copy a large amount of files, its progress bar is designed for this scenario: progress only updates when a file has been transferred. In a large file scenario, every file is transferred slowly, hence the slow or even static progress bar. This is worse for destinations without multipart upload support (e.g. `file`, `sftp`, `jfs`, `gluster` schemes), where every file is transferred single-threaded.

If progress bar is not moving, use below methods to observe and troubleshoot:

* If either end is a JuiceFS mount point, you can use [`juicefs stats`](../administration/fault_diagnosis_and_analysis.md#stats) to quickly check current IO status.
* If destination is a local disk, look for temporary files that end with `.tmp.xxx`, these are the temp files created by `sync`, they will be renamed upon transfer complete. Look for size changes in temp files to verify the current IO status.
* If both end are object storage services, use tools like `nethogs` to check network IO.

### Incremental and full synchronization {#incremental-and-full-synchronization}

The subcommand `sync` works incrementally by default, which compares the differences between the source and target paths, and then synchronizes only the differences. You can add option `--update` or `-u` to keep updated the `mtime` of the synchronized directories and files.

For full synchronization, i.e. synchronizing all the time no matter whether the destination files exist or not, you can add option `--force-update` or `-f`. For example, the following command fully synchronizes `movies` directory from [Object Storage A](#required-storages) to [JuiceFS File System](#required-storages).

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# full synchronization
juicefs sync --force-update s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

### Filtering {#filtering}

You can provide multiple matching patterns to filter the paths to be synchronized through the options `--exclude` and `--include`. The `--exclude` option indicates that the matched path will not be synchronized, the `--include` option indicates that the matched path will be synchronized. When no rules are provided, all scanned files will be synchronized by default. When there are multiple matching patterns, matching attempts will be made in the order of the provided matching patterns based on the ["Filter mode"](#filtering-mode) you use, and ultimately it will be decided whether to synchronize a certain file.

:::tip
When multiple matching patterns are provided, it may become difficult to determine whether to synchronize a file depending on the specific "filter mode" you use. At this time, it is recommended to add the `--dry` option to check in advance whether the specific files to be synchronized meet expectations. If not, you need to adjust the matching patterns.
:::

#### Matching rules {#matching-rules}

Matching rules refer to giving a path and a pattern, and then determining whether the path can match the pattern. The pattern can contain some special characters (similar to shell wildcards):

+ A single `*` matches any character, but terminates matching when encountering `/`;
+ `**` matches any character, including `/`;
+ `?` matches any single character that is not `/`;
+ `[...]` matches a set of characters, for example `[a-z]` matches any lowercase letter;
+ `[^...]` does not match a set of characters, for example `[^abc]` matches any character except `a`, `b`, `c`.

In addition, there are some matching rules to note:

- If the matching pattern does not contain special characters, it will completely match the file name in the path. For example, `foo` can match `foo` and `xx/foo`, but does not match `foo1` (cannot match prefix) and `2foo` (cannot match suffix) and `foo/xx` (`foo` is not a directory);
- If the matching pattern ends with `/`, it will only match directories, not ordinary files;
- If the matching pattern starts with `/`, it means matching the full path (the path does not need to start with `/`), so `/foo` matches the `foo` file at the root of the transfer.

Here are some examples of matching patterns:

+ `--exclude '*.o'` will exclude all files whose file names can match `*.o`;
+ `--exclude '/foo/*/bar'` will exclude files named `bar` in the directory named `foo` in the root directory and "two levels" down.
+ `--exclude '/foo/**/bar'` will exclude files named `bar` in the directory named `foo` in the root directory and "any levels" down.

#### Filtering modes {#filtering-mode}

Filtering mode refers to how to decide whether to synchronize a path based on multiple matching rules. The `sync` command supports two modes: "full path filtering" and "layer-by-layer filtering". By default, the `sync` command uses layer-by-layer filtering mode, and the `--match-full-path` option can be used to use the full path filtering mode. Since the workflow of the full path filtering mode is easier to understand, it is recommended to use the full path filtering mode first.

##### Full path filtering mode (recommended) <VersionAdd>1.2.0</VersionAdd> {#full-path-filtering-mode}

The full path filtering mode refers to directly matching the "full path" of the object to be matched with multiple patterns in sequence. Once a matching pattern is successfully matched, the result ("synchronize" or "exclude") will be returned directly and ignore subsequent matching patterns.

Below is the flow chart for full path filtering mode:

![Full path filtering mode flow chart](../images/sync-full-path-filtering-mode-flow-chart.svg)

For example, there is a file with the path `a1/b1/c1.txt`, and 3 matching patterns `--include 'a*.txt' --inlude 'c1.txt' --exclude 'c*.txt'`. In the full path filtering mode, the string `a1/b1/c1.txt` will be directly matched against the matching pattern in sequence. The specific steps are:

1. Try to match `a1/b1/c1.txt` with `--include 'a*.txt'`, the result is no match. Because `*` cannot match the `/` character, refer to ["Matching rules"](#matching-rules);
2. Try to match `a1/b1/c1.txt` with `--inlude 'c1.txt'`. At this time, the match will be successful according to the matching rules. Although the subsequent `--exclude 'c*.txt'` can be matched according to the matching rules, according to the logic of the full path filtering mode, once a pattern is matched, subsequent patterns will no longer try to match. So the final matching result is "synchronize".

Here are some more examples:

+ `--exclude '/foo**'` will exclude all files or directories with the root directory name `foo`;
+ `--exclude '**foo/**'` will exclude all directories ending with `foo`;
+ `--include '*/' --include '*.c' --exclude '*'` will only include all directories and files with the suffix `.c`, except that all files and directories will excluded;
+ `--include 'foo/bar.c' --exclude '*'` will only include the `foo` directory and the `foo/bar.c` file.

##### Layer-by-layer filtering mode {#layer-by-layer-filtering-mode}

The core of the layer-by-layer filtering mode is to first split the full path according to the directory level and combine it into multiple string sequences layer by layer. For example, the full path is `a1/b1/c1.txt`, and the resulting sequence is `a1`, `a1/b1`, `a1/b1/c1.txt`. Then each element in this sequence is regarded as the path in the full path filtering mode, and ["full path filtering"](#full-path-filtering-mode) is executed in sequence.

If an element matches a certain pattern, there will be two processing logics:

- If the pattern is an exclude pattern, the "exclude" behavior will be returned directly as the final matching result;
- If the pattern is an include pattern, the subsequent patterns to be matched at this layer are skipped and the next layer is entered directly.

If all patterns at a certain layer are not matched, go to the next layer. **If "exclude" is not returned after all layers are matched, the default behavior "synchronize" will be returned.**

Below is the flow chart for layer-by-layer filtering mode:

![Layer-by-layer filtering mode flow chart](../images/sync-layer-by-layer-filtering-mode-flow-chart.svg)

For example, there is a file with the path `a1/b1/c1.txt`, and 3 matching patterns `--include 'a*.txt' --inlude 'c1.txt' --exclude 'c*.txt'`. In layer-by-layer filtering mode, the sequence is `a1`, `a1/b1`, `a1/b1/c1.txt`. The specific matching steps are:

1. The path of the first layer is `a1`. According to the matching patterns, the result is that nothing is matched. Go to the next layer;
2. The path of the second layer is `a1/b1`. According to the matching patterns, the result is that all are not matched. Go to the next layer;
3. The path of the third layer is `a1/b1/c1.txt`. According to the matching patterns, the `--inlude 'c1.txt'` pattern will be matched. The behavior of this pattern is "synchronize" and entering the next layer;
4. Since there is no next layer, the final returned behavior is "synchronize".

In the above example, the matching is successful until the last layer. In addition, there may be two situations:

- If the match is successful before the last layer, and the matching pattern is exclude pattern, the "exclude" behavior will be directly returned as the final result, skipping all subsequent layers;
- All layers have been matched, but none have been matched. At this time, the "synchronize" behavior will also be returned.

In a word, the layer-by-layer filtering mode is to perform full path filtering from high to low according to the path layer. Each layer of filtering has only two results: either directly getting "exclude" as the final result, or entering the next layer. The only way to get "synchronize" result is to run through all layers of filtering.

Here are some more examples:

+ `--exclude /foo` will exclude all files or directories with the root directory name `foo`;
+ `--exclude foo/` will exclude all directories named `foo`;
+ For multi-level directories such as `dir_name/.../.../...`, all paths under `dir_name` will be matched according to the directory hierarchy. If the parent directory of a file is "excluded", the file will not be synchronized even if the include rule of the file is added. If you want to synchronize this file, you must ensure that its "all parent directories" are not excluded. For example, the file `/some/path/this-file-will-not-be-synced` in the following example will not be synchronized because its parent directory `some` has been excluded by the rule `--exclude '*'`:

  ```shell
  --include '/some/path/this-file-will-not-be-synced' \
  --exclude '*'
  ```

  One solution is to include all directories in the directory hierarchy, that is, use the `--include '*/'` rule (which needs to be placed before the `--exclude '*'` rule); another solution is to add include rules to all parent directories, for example:

  ```shell
  --include '/some/' \
  --include '/some/path/' \
  --include '/some/path/this-file-will-be-synced' \
  --exclude '*'
  ```

The behavior of layer-by-layer filtering mode is more complicated to understand and use, but it is basically compatible with rsync's `--include/--exclude` options, so it is generally recommended to be used in scenarios that require compatibility with rsync behavior.

### Directory structure and file permissions {#directory-structure-and-file-permissions}

The subcommand `sync` only synchronizes file objects and directories containing file objects, and skips empty directories by default. To synchronize empty directories, you can use `--dirs` option.

In addition, when synchronizing between file systems such as local, SFTP and HDFS, option `--perms` can be used to synchronize file permissions from the source to the destination.

### Copy symbolic links {#copy-symbolic-links}

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

## Application scenarios {#application-scenarios}

### Geo-disaster recovery backup {#geo-disaster-recovery-backup}

Geo-disaster recovery backup backs up files, and thus the files stored in JuiceFS should be synchronized to other object storages. For example, synchronize files in [JuiceFS File System](#required-storages) to [Object Storage A](#required-storages):

```shell
# mount JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# synchronization
juicefs sync /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

After sync, you can see all the files in [Object Storage A](#required-storages).

### Build a JuiceFS data copy {#build-a-juicefs-data-copy}

Unlike the file-oriented disaster recovery backup, the purpose of creating a copy of JuiceFS data is to establish a mirror with exactly the same content and structure as the JuiceFS data storage. When the object storage in use fails, you can switch to the data copy by modifying the configurations. Note that only the file data of the JuiceFS file system is replicated, and the metadata stored in the metadata engine still needs to be backed up.

This requires manipulating the underlying object storage directly to synchronize it with the target object storage. For example, to take the [Object Storage B](#required-storages) as the data copy of the [JuiceFS File System](#required-storages):

```shell
juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

After sync, the file content and hierarchy in the [Object Storage B](#required-storages) are exactly the same as the [underlying object storage of JuiceFS](#required-storages).

### Sync across regions using S3 Gateway {#sync-across-region}

When transferring a large amount of small files across different regions via FUSE mount points, clients will inevitably talk to the metadata service in the opposite region via public internet (or dedicated network connection with limited bandwidth). In such cases, metadata latency can become the bottleneck of the data transfer:

![sync via public metadata service](../images/sync-public-metadata.svg)

S3 Gateway comes to rescue in these circumstances: deploy a gateway in the source region, and since this gateway accesses metadata via private network, metadata latency is eliminated to a minimum, bringing the best performance for small file intensive scenarios.

![sync via gateway](../images/sync-via-gateway.svg)

Read [S3 Gateway](../deployment/s3_gateway.md) to learn its deployment and use.
