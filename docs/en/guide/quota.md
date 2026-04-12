---
title: Storage Quota
sidebar_position: 4
---

JuiceFS supports both total file system quota and subdirectory quota, both of which can be used to limit the available capacity and the number of available inodes. Both file system quota and directory quota are hard limits. When the total file system quota is exhausted, subsequent writes will return `ENOSPC` (No space left) error; and when the directory quota is exhausted, subsequent writes will return `EDQUOT` (Disk quota exceeded) error.

:::tip
The storage quota settings are stored in the metadata engine for all mount points to read, and the client of each mount point will also cache its own used capacity and inodes and synchronize them with the metadata engine once per second. Meanwhile the client will read the latest usage value from the metadata engine every 10 seconds to synchronize the usage information among each mount point, but this information synchronization mechanism cannot guarantee that the usage data is counted accurately.
:::

## File system quota {#file-system-quota}

For Linux, the default capacity of a JuiceFS type file system is identified as `1.0P` by using the `df` command.

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  1.0P  682M  1.0P    1% /mnt
```

:::note
The capacity of underlying object storage is usually unlimited, i.e., JuiceFS storage is unlimited. Therefore, the displayed capacity is just an estimate rather than the actual storage limit.
:::

The `config` command that comes with the client allows you to view the details of a file system.

```shell
$ juicefs config $METAURL
{
  "Name": "ujfs",
  "UUID": "1aa6d290-279b-432f-b9b5-9d7fd597dec2",
  "Storage": "minio",
  "Bucket": "127.0.0.1:9000/jfs1",
  "AccessKey": "herald",
  "SecretKey": "removed",
  "BlockSize": 4096,
  "Compression": "none",
  "Shards": 0,
  "Partitions": 0,
  "Capacity": 0,
  "Inodes": 0,
  "TrashDays": 0
}
```

### Limit total capacity {#limit-total-capacity}

The capacity limit (in GiB) can be set with `--capacity` when creating a file system, e.g. to create a file system with an available capacity of 100 GiB:

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
    --capacity 100 \
    $METAURL myjfs
```

You can also set a capacity limit for a created file system with the `config` command:

```shell
$ juicefs config $METAURL --capacity 100
2022/01/27 12:31:39.506322 juicefs[16259] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:31:39.521232 juicefs[16259] <WARNING>: The latency to database is too high: 14.771783ms
  capacity: 0 GiB -> 100 GiB
```

For file systems that have been set with storage quota, the identification capacity becomes the quota capacity:

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  100G  682M  100G    1% /mnt
```

### Limit the total number of inodes {#limit-total-number-of-inodes}

On Linux systems, each file (a folder is also a type of file) has an inode regardless of size, so limiting the number of inodes is equivalent to limiting the number of files.

The quota can be set with `--inodes` when creating the file system, e.g.

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
    --inodes 100 \
    $METAURL myjfs
```

The file system created by the above command allows only 100 files to be stored. However, there is no limit to the size of individual files. For example, it will still work if a single file is equivalent or even larger than 1 TB as long as the total number of files does not exceed 100.

You can also set a capacity quota for a created file system by using the `config` command:

```shell
$ juicefs config $METAURL --inodes 100
2022/01/27 12:35:37.311465 juicefs[16407] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:35:37.322991 juicefs[16407] <WARNING>: The latency to database is too high: 11.413961ms
    inodes: 0 -> 100
```

### Combine `--capacity` and `--inodes` {#limit-total-capacity-and-inodes}

You can combine `--capacity` and `--inodes` to set the capacity quota of a file system with more flexibility. For example, to create a file system that the total capacity limits to 100 TiB with only 100000 files to be stored:

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
    --capacity 102400 \
    --inodes 100000 \
    $METAURL myjfs
```

Similarly, for the file systems that have been created, you can follow the settings below separately.

```shell
juicefs config $METAURL --capacity 102400
```

```shell
juicefs config $METAURL --inodes 100000
```

:::tip
The client reads the latest storage quota settings from the metadata engine periodically to update the local settings. The refresh interval is controlled by the `--heartbeat` option (default: 12 seconds). Other mount points may take up to the heartbeat interval to update the quota setting.
:::

## Directory quota {#directory-quota}

JuiceFS began to support directory-level storage quota since v1.1, and you can use the `juicefs quota` subcommand for directory quota management and query.

:::tip
The usage statistic relies on the mount process, please do not use this feature until all writable mount processes are upgraded to v1.1.0.
:::

### Limit directory capacity {#limit-directory-capacity}

You can use `juicefs quota set $METAURL --path $DIR --capacity $N` to set the directory capacity limit in GiB. For example, to set a capacity quota of 1GiB for the directory `/test`:

```shell
$ juicefs quota set $METAURL --path /test --capacity 1
+-------+---------+---------+------+-----------+-------+-------+
|  Path |   Size  |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+---------+---------+------+-----------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% | unlimited |   314 |       |
+-------+---------+---------+------+-----------+-------+-------+
```

After the setting is successful, you can see a table describing the current quota setting directory, quota size, current usage and other information.

:::tip
The use of the `quota` subcommand does not require a local mount point, and it is expected that the input directory path is a path relative to the JuiceFS root directory rather than a local mount path. It may take a long time to set a quota for a large directory, because the current usage of the directory needs to be calculated.
:::

If you need to query the quota and current usage of a certain directory, you can use the `juicefs quota get $METAURL --path $DIR` command:

```shell
$ juicefs quota get $METAURL --path /test
+-------+---------+---------+------+-----------+-------+-------+
|  Path |   Size  |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+---------+---------+------+-----------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% | unlimited |   314 |       |
+-------+---------+---------+------+-----------+-------+-------+
```

You can also use the `juicefs quota ls $METAURL` command to list all directory quotas.

### Limit the total number of directory inodes

You can use `juicefs quota set $METAURL --path $DIR --inodes $N` to set the directory inode quota, the unit is one. For example, to set a quota of 400 inodes for the directory `/test`:

```shell
$ juicefs quota set $METAURL --path /test --inodes 400
+-------+---------+---------+------+--------+-------+-------+
|  Path |   Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+---------+---------+------+--------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% |    400 |   314 |   78% |
+-------+---------+---------+------+--------+-------+-------+
```

### Limit capacity and inodes of directory {#limit-capacity-and-inodes-of-directory}

You can combine `--capacity` and `--inodes` to set the capacity limit of the directory more flexibly. For example, to set a quota of 10GiB and 1000 inodes for the `/test` directory:

```shell
$ juicefs quota set $METAURL --path /test --capacity 10 --inodes 1000
+-------+--------+---------+------+--------+-------+-------+
|  Path |  Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+--------+---------+------+--------+-------+-------+
| /test | 10 GiB | 1.6 MiB |   0% |  1,000 |   314 |   31% |
+-------+--------+---------+------+--------+-------+-------+
```

In addition, you can also not limit the capacity of the directory and the number of inodes (set to `0` means unlimited), and only use the `quota` command to count the current usage of the directory:

```shell
$ juicefs quota set $METAURL --path /test --capacity 0 --inodes 0
+-------+-----------+---------+------+-----------+-------+-------+
|  Path |    Size   |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+-----------+---------+------+-----------+-------+-------+
| /test | unlimited | 1.6 MiB |      | unlimited |   314 |       |
+-------+-----------+---------+------+-----------+-------+-------+
```

### Nested quota {#nested-quota}

JuiceFS allows nested quota to be set on multiple levels of directories, client performs recursive lookup to ensure quota settings take effect on every level of directory. This means even if the parent directory is allocated a smaller quota, you can still set a larger quota on the child directory.

### Subdirectory mount {#subdirectory-mount}

JuiceFS supports mounting arbitrary subdirectories using [`--subdir`](../reference/command_reference.mdx#mount-metadata-options). If the directory quota is set for the mounted subdirectory, you can use the `df` command that comes with the system to view the directory quota and current usage. For example, the file system quota is 1PiB and 10M inodes, while the quota for the `/test` directory is 1GiB and 400 inodes. The output of the `df` command when mounted using the root directory is:

```shell
$ df -h
Filesystem      Size  Used Avail Use% Mounted on
...
JuiceFS:myjfs   1.0P  1.6M  1.0P   1% /mnt/jfs

$ df -i -h
Filesystem     Inodes IUsed IFree IUse% Mounted on
...
JuiceFS:myjfs     11M   315   10M    1% /mnt/jfs
```

When mounted using the `/test` subdirectory, the output of the `df` command is:

```shell
$ df -h
Filesystem      Size  Used Avail Use% Mounted on
...
JuiceFS:myjfs   1.0G  1.6M 1023M   1% /mnt/jfs

$ df -i -h
Filesystem     Inodes IUsed IFree IUse% Mounted on
...
JuiceFS:myjfs     400   314    86   79% /mnt/jfs
```

:::note
When there is no quota set for the mounted subdirectory, JuiceFS will query up to find the nearest directory quota and return it to `df`. If directory quotas are set for multiple levels of parent directories, JuiceFS will return the minimum available capacity and number of inodes after calculation.
:::

## User and group quota {#user-and-group-quota}

In addition to directory quotas, JuiceFS supports quotas by UID (user) and GID (group). These are also hard limits. Writes that exceed the limit return `EDQUOT` (disk quota exceeded).

Unlike directory quotas, user/group quotas are not based on the directory hierarchy. They are accounted and enforced by the ownership metadata (UID/GID) of files and directories.

:::tip
User/group quotas are tracked by numeric UID/GID, not by usernames or group names. You can use the `id` command to check the actual values of UID/GID.
:::

### Set user quota

Use `--uid` to set a quota for a user. For example, limit UID `1000` to `2 GiB` and `200` inodes:

```shell
$ juicefs quota set $METAURL --uid 1000 --capacity 2 --inodes 200
+---------+---------+---------+------+--------+-------+-------+
| User ID |   Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+---------+---------+---------+------+--------+-------+-------+
|     1000| 2.0 GiB | 1.6 MiB |   0% |    200 |    12 |    6% |
+---------+---------+---------+------+--------+-------+-------+
```

Query and delete a user quota:

```shell
juicefs quota get $METAURL --uid 1000
juicefs quota delete $METAURL --uid 1000
```

### Set group quota

Use `--gid` to set a quota for a group. For example, limit GID `100` to `5 GiB` and `500` inodes:

```shell
$ juicefs quota set $METAURL --gid 100 --capacity 5 --inodes 500
+----------+---------+---------+------+--------+-------+-------+
| Group ID |   Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+----------+---------+---------+------+--------+-------+-------+
|      100 | 5.0 GiB | 3.2 MiB |   0% |    500 |    21 |    4% |
+----------+---------+---------+------+--------+-------+-------+
```

Query and delete a group quota:

```shell
juicefs quota get $METAURL --gid 100
juicefs quota delete $METAURL --gid 100
```

### List all quotas

Use `juicefs quota list $METAURL` to list directory, user, and group quotas together. In the output, user and group quotas are marked as `uid:<id>` and `gid:<id>` respectively.

### Consistency check and repair

Like directory quotas, user/group quotas can become inconsistent after abnormal exits. Use the `check` subcommand to verify and repair them.

Check all user/group quotas:

```shell
juicefs quota check $METAURL
```

Check a specific user or group:

```shell
juicefs quota check $METAURL --uid 1000
juicefs quota check $METAURL --gid 100
```

Once an inconsistency is detected, run the check with `--repair` to fix it:

```shell
juicefs quota check $METAURL --repair
```

### Accounting scope

User/group quota usage is accounted based on the final UID/GID ownership of objects after the write operation, rather than billing directly against the client account that initiated the operation.

For example, when creating files under a directory with `setgid`, the file's GID can inherit from the parent directory, and the inherited group quota is consumed.

### Usage check and fix {#usage-check-and-fix}

Since directory usage updates are laggy and asynchronous, loss may occur under unusual circumstances (such as a client exiting unexpectedly). We can use the `juicefs quota check $METAURL --path $DIR` command to check or fix it:

```shell
$ juicefs quota check $METAURL --path /test
2023/05/23 15:40:12.704576 juicefs[1638846] <INFO>: quota of /test is consistent [base.go:839]
+-------+--------+---------+------+--------+-------+-------+
|  Path |  Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+--------+---------+------+--------+-------+-------+
| /test | 10 GiB | 1.6 MiB |   0% |  1,000 |   314 |   31% |
+-------+--------+---------+------+--------+-------+-------+
```

## Usage accounting scope

This section summarizes the accounting rules of JuiceFS usage across three dimensions:

- Global usage: total usage at the file system level (file system quota usage).
- Directory usage: directory quota usage (`juicefs quota --path ...` and related directory statistics from `summary`/`info`).
- User/group usage: user and group quota usage (`--uid` / `--gid`).

### Core differences

| Object type | Global usage | Directory usage | User/group usage |
| --- | --- | --- | --- |
| Regular file | Counted by file data size (aligned to 4 KiB) | Counted in directory tree (aligned to 4 KiB) | Counted by file owner UID/GID (aligned to 4 KiB) |
| Hard-linked file | Counted once per inode; creating extra links does not duplicate usage | Each directory entry is counted in its directory scope (same inode can appear repeatedly in directory usage) | Counted once per inode; creating extra links does not duplicate usage |
| Directory and other file types | Counted as inode usage; space uses metadata minimum granularity (effectively 4 KiB) | Counted as inode usage; space uses metadata minimum granularity (effectively 4 KiB) | Counted as inode usage; space uses metadata minimum granularity (effectively 4 KiB) |
| Trash files | Still counted after moving to trash; released only after real trash cleanup | Removed from the original tree, so no longer counted in original/ancestor directory usage; quotas on trash directories are not supported | Still counted to original owner UID/GID after moving to trash; released only after actual trash cleanup |

### Details

- Regular files

  For all three dimensions, regular files are accounted by their file length, aligned to 4 KiB, and each consumes one inode.

- Hard-linked files

  The key difference is whether accounting deduplicates by inode:

  - Global usage and user/group usage: deduplicate by inode. Creating a hard link only adds a directory entry; no new file entity is created.
  - Directory usage: accumulates by directory entries in the tree. Creating a hard link under a directory increases usage for that directory and its ancestors.

- Directories and other non-regular files

  Directories, symlinks, and other non-regular files mainly consume inodes. Their space usage is treated as 4 KiB by default.

- Trash files

  When the trash feature is enabled, deleting a file usually moves it to the trash instead of performing immediate physical deletion.

  - For global usage and user/group usage: usage is not released immediately after moving to trash; it decreases only after trash cleanup.
  - For directory usage: once entries are moved out of the original directory tree, usage on original directory quotas decreases. Quotas on trash directories are not currently supported.

When the directory usage is correct, the current directory quota usage will be output; if it fails, the error log will be output:

```shell
$ juicefs quota check $METAURL --path /test
2023/05/23 15:48:17.494604 juicefs[1639997] <WARNING>: /test: quota(314, 4.0 KiB) != summary(314, 1.6 MiB) [base.go:843]
2023/05/23 15:48:17.494644 juicefs[1639997] <FATAL>: quota of /test is inconsistent, please repair it with --repair flag [main.go:31]
```

At this point you can use the `--repair` option to repair directory usage:

```shell
$ juicefs quota check $METAURL --path /test --repair
2023/05/23 15:50:08.737086 juicefs[1640281] <WARNING>: /test: quota(314, 4.0 KiB) != summary(314, 1.6 MiB) [base.go:843]
2023/05/23 15:50:08.737123 juicefs[1640281] <INFO>: repairing... [base.go:852]
+-------+--------+---------+------+--------+-------+-------+
|  Path |  Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+--------+---------+------+--------+-------+-------+
| /test | 10 GiB | 1.6 MiB |   0% |  1,000 |   314 |   31% |
+-------+--------+---------+------+--------+-------+-------+
```
