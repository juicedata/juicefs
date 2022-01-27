---
sidebar_label: Storage Quota
sidebar_position: 7
---
# JuiceFS Storage Quota

JuiceFS v0.14.2 begins to support file system level storage quotas, a feature that includes:

- Limit the total available capacity of the file system
- Limit the total inodes of the file system

:::tip
The storage quota settings are stored in the metadata engine for all mount points to read, and the client of each mount point will also cache its own used capacity and inodes and synchronize them with the metadata engine once per second, while the client will read the latest usage value from the metadata engine every 10 seconds to synchronize the usage information among each mount point, but this information synchronization mechanism does not guarantee that the usage data will be counted accurately.
:::

## View file system information

In a Linux environment, for example, the default capacity of a JuiceFS type file system is identified as `1.0P` using the `df` command that comes with the system.

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  1.0P  682M  1.0P    1% /mnt
```

:::note
JuiceFS implements support for the POSIX interface through FUSE, because the underlying object storage is usually of infinitely scalable capacity, so the marked capacity is only a valuation (which also means unlimited) and not the actual capacity, which changes dynamically with the actual usage.
:::

The `config` command that comes with the client allows you to view the details of a filesystem.

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

The capacity limit in GiB can be set with `--capacity` when creating a file system, e.g. to create a file system with an available capacity of 100 GiB:

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--capacity 100 \
$METAURL myjfs
```

You can also set a capacity limit for a created filesystem with the `config` command:

```shell
$ juicefs config $METAURL --capacity 100
2022/01/27 12:31:39.506322 juicefs[16259] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:31:39.521232 juicefs[16259] <WARNING>: The latency to database is too high: 14.771783ms
  capacity: 0 GiB -> 100 GiB
```

For file systems with storage quota set, the identification capacity becomes the quota capacity:

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  100G  682M  100G    1% /mnt
```

## Limit the total number of inodes

On Linux systems, each file (a folder is also a type of file) has an inode regardless of size, so limiting the number of inodes is equivalent to limiting the number of files.

The quota can be set with `--inodes` when creating the filesystem, e.g.

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--inodes 100 \
$METAURL myjfs
```

The file system created by the above command allows only 100 files to be stored, but there is no limit to the size of individual files, for example, a single file of 1TB or even larger is fine, as long as the total number of files does not exceed 100.

You can also set a capacity quota for a created filesystem by using the `config` command:

```shell
$ juicefs config $METAURL --inodes 100
2022/01/27 12:35:37.311465 juicefs[16407] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:35:37.322991 juicefs[16407] <WARNING>: The latency to database is too high: 11.413961ms
    inodes: 0 -> 100
```

## Put together

You can combine `--capacity` and `--inodes` to set the capacity quota of a filesystem more flexibly, for example, to create a filesystem that limits the total capacity to 100TiB and allows only 100000 files to be stored:

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--capacity 102400 \
--inodes 100000 \
$METAURL myjfs
```

Similarly, for the created file systems, the following settings can be made separately.

```shell
juicefs config $METAURL --capacity 102400
```

```shell
juicefs config $METAURL --inodes 100000
```

:::tip
The client reads the latest storage quota settings from the metadata engine every 60 seconds to update the local settings, and this time frequency may cause other mount points to take up to 60 seconds to complete the quota setting update.
:::
