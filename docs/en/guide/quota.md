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
The client reads the latest storage quota settings from the metadata engine every 60 seconds to update the local settings, and this frequency may cause other mount points to take up to 60 seconds to update the quota setting.
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
