---
sidebar_label: Storage Quota
sidebar_position: 5
---
# Storage Quota

Since JuiceFS v0.14.2, JuiceFS have supported file system level storage quotas. This feature allows users to:

- Limit the total available capacity of the file system
- Limit the total inodes of the file system

:::tip
The storage quota settings are stored in the metadata engine for all mount points to read, and the client of each mount point will also cache its own used capacity and inodes and synchronize them with the metadata engine once per second. Meanwhile the client will read the latest usage value from the metadata engine every 10 seconds to synchronize the usage information among each mount point, but this information synchronization mechanism cannot guarantee that the usage data is counted accurately.
:::

## View file system information

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

## Limit total capacity

The capacity limit (in GiB) can be set with `--capacity` when creating a file system, e.g. to create a file system with an available capacity of 100 GiB:

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
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

## Limit the total number of inodes

On Linux systems, each file (a folder is also a type of file) has an inode regardless of size, so limiting the number of inodes is equivalent to limiting the number of files.

The quota can be set with `--inodes` when creating the file system, e.g.

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
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

## Combine `--capacity` and `--inode`

You can combine `--capacity` and `--inodes` to set the capacity quota of a file system with more flexibility. For example, to create a file system that the total capacity limits to 100 TiB with only 100000 files to be stored:

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
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
