---
title: How to destroy a file system
sidebar_position: 8
---

JuiceFS client provides the `destroy` command to completely destroy a file system, which will result in

- Deletion of all metadata entries of this file system
- Deletion of all data blocks of this file system

Use this command in the following format.

```shell
juicefs destroy <METADATA URL> <UUID>
```

- `<METADATA URL>`: The URL address of the metadata engine
- `<UUID>`: The UUID of the file system

## Find the UUID of the file system

JuiceFS client provides a `status` command to view detailed information about a file system by simply specifying the file system's metadata engine URL, e.g.

```shell {8}
$ juicefs status redis://127.0.0.1:6379/1

2022/01/26 21:41:37.577645 juicefs[31181] <INFO>: Meta address: redis://127.0.0.1:6379/1
2022/01/26 21:41:37.578238 juicefs[31181] <INFO>: Ping redis: 55.041µs
{
  "Setting": {
    "Name": "macjfs",
    "UUID": "eabb96d5-7228-461e-9240-fddbf2b576d8",
    "Storage": "file",
    "Bucket": "jfs/",
    "AccessKey": "",
    "BlockSize": 4096,
    "Compression": "none",
    "Shards": 0,
    "Partitions": 0,
    "Capacity": 0,
    "Inodes": 0,
    "TrashDays": 1
  },
  ...
}
```

## Destroy a file system

:::danger
The destroy operation will cause all the data in the database and the object storage associated with the file system to be deleted. Please make sure to back up the important data before operating!
:::

```shell {1}
$ juicefs destroy redis://127.0.0.1:6379/1 eabb96d5-7228-461e-9240-fddbf2b576d8

2022/01/26 21:52:17.488987 juicefs[31518] <INFO>: Meta address: redis://127.0.0.1:6379/1
2022/01/26 21:52:17.489668 juicefs[31518] <INFO>: Ping redis: 55.542µs
 volume name: macjfs
 volume UUID: eabb96d5-7228-461e-9240-fddbf2b576d8
data storage: file://jfs/
  used bytes: 18620416
 used inodes: 23
WARNING: The target volume will be destroyed permanently, including:
WARNING: 1. objects in the data storage
WARNING: 2. entries in the metadata engine
Proceed anyway? [y/N]: y
deleting objects: 68
The volume has been destroyed! You may need to delete cache directory manually.
```

When destroying a file system, the client will issue a confirmation prompt. Please make sure to check the file system information carefully and enter `y` after confirming it is correct.

## FAQ

```shell
2022/01/26 21:47:30.949149 juicefs[31483] <FATAL>: 1 sessions are active, please disconnect them first
```

If you receive an error like the one above, which indicates that the file system has not been properly unmounted, please check and confirm that all mount points are unmounted before proceeding.
