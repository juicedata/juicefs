---
title: Status Check & Maintenance
sidebar_position: 4
---

Any kind of storage system needs to be checked and maintained regularly after it is put into use, so that potential problems can be found and fixed as early as possible to ensure reliable operation of the file system and the integrity and consistency of the stored data.

JuiceFS provides a series of tools to check and maintain the file system, not only to help us understand the basic information of the file system and its operation status, but also to help us detect and fix potential problems more easily.

## status

`juicefs status` is used to review basic information about a JuiceFS file system and the status of all active sessions (including mounts, SDK accesses, S3 gateways, WebDAV connections).

The basic information of the file system includes name, UUID, storage type, bucket, trash status, etc.

```shell
juicefs status redis://xxx.cache.amazonaws.com:6379/1
```

```json
{
  "Setting": {
    "Name": "myjfs",
    "UUID": "6b0452fc-0502-404c-b163-c9ab577ec766",
    "Storage": "s3",
    "Bucket": "https://xxx.s3.amazonaws.com",
    "AccessKey": "xxx",
    "SecretKey": "removed",
    "BlockSize": 4096,
    "Compression": "none",
    "TrashDays": 1,
    "MetaVersion": 1
  },
  "Sessions": [
    {
      "Sid": 2,
      "Heartbeat": "2021-08-23T16:47:59+08:00",
      "Version": "1.0.0+2022-08-08.cf0c269",
      "Hostname": "ubuntu-s-1vcpu-1gb-sgp1-01",
      "MountPoint": "/home/herald/mnt",
      "ProcessID": 2869146
    }
  ]
}
```

Specifying the `Sid` of a session with the `--session, -s` option allows you to provide more information about the session.

```shell
juicefs status --session 2 redis://xxx.cache.amazonaws.com:6379/1
```

```json
{
  "Sid": 2,
  "Heartbeat": "2021-08-23T16:47:59+08:00",
  "Version": "1.0.0+2022-08-08.cf0c269",
  "Hostname": "ubuntu-s-1vcpu-1gb-sgp1-01",
  "MountPoint": "/home/herald/mnt",
  "ProcessID": 2869146
}
```

Depending on the status of the session, the message may also include.

- Sustained inodes: These are files that have been deleted, but because they have been opened in this session, they will be held temporarily until the file is closed.
- Flocks: BSD lock information about the file that locked by this session
- Plocks: POSIX lock Information about the file that locked by this session

## info

`juicefs info` is used to check the metadata information of the specified file or directory, which includes the object path on the object storage for each block corresponding to that file.

### Checking the metadata of a file

```shell
$ juicefs info mnt/luggage-6255515.jpg

mnt/luggage-6255515.jpg :
  inode: 36
  files: 1
   dirs: 0
 length: 789.02 KiB (807955 Bytes)
   size: 792.00 KiB (811008 Bytes)
   path: /luggage-6255515.jpg
objects:
+------------+------------------------------+--------+--------+--------+
| chunkIndex |          objectName          |  size  | offset | length |
+------------+------------------------------+--------+--------+--------+
|          0 | myjfs/chunks/0/0/80_0_807955 | 807955 |      0 | 807955 |
+------------+------------------------------+--------+--------+--------+
```

### Checking the metadata of a directory

This command checks only one level of directories by default:

```shell
$ juicefs info ./mnt

mnt :
  inode: 1
  files: 9
   dirs: 4
 length: 2.41 MiB (2532102 Bytes)
   size: 2.44 MiB (2555904 Bytes)
   path: /
```

If you want to recursively check all subdirectories, you need to specify the `--recursive, -r` option:

```shell
$ juicefs info -r ./mnt

./mnt :
  inode: 1
  files: 33
   dirs: 4
 length: 80.29 MiB (84191037 Bytes)
   size: 80.34 MiB (84242432 Bytes)
   path: /
```

### Checking metadata with inode

You can also perform reverse lookup on the file path and data block information via inode, but you need to enter the mount point directory.

```shell
~     $ cd mnt
~/mnt $ juicefs info -i 36

36 :
  inode: 36
  files: 1
   dirs: 0
 length: 789.02 KiB (807955 Bytes)
   size: 792.00 KiB (811008 Bytes)
   path: /luggage-6255515.jpg
objects:
+------------+------------------------------+--------+--------+--------+
| chunkIndex |          objectName          |  size  | offset | length |
+------------+------------------------------+--------+--------+--------+
|          0 | myjfs/chunks/0/0/80_0_807955 | 807955 |      0 | 807955 |
+------------+------------------------------+--------+--------+--------+
```

## gc

`juicefs gc` is designed to handle "object leak" and run compaction on data fragments created by file overwrites. It scans metadata and compare with object storage to find or clean up any object storage blocks that need processing.

:::info
**Object leak** is a situation where a block of data is in the object storage, but there is no corresponding record in the metadata engine. Object leak are rare and can be caused by program bugs, unanticipated problems with the metadata engine or object storage, power outages, network disconnections, etc.
:::

:::tip
Temporary intermediate files may be produced when files are uploaded to the object storage, and they will be cleaned up after the writing is complete. To avoid intermediate files being misclassified as leaked objects, `juicefs gc` skips files uploaded in the last 1 hour by default. The skipped time range (in seconds) can be adjusted via the `JFS_GC_SKIPPEDTIME` environment variable. For example, to set skip the last 2 hours of files: `export JFS_GC_SKIPPEDTIME=7200`.
:::

:::tip
Because the `juicefs gc` command scans all objects in the object storage, there is some overhead in executing this command for file system with large amounts of data.
:::

### Scan for leaked objects

Although object leak almost never occur, you can still perform the appropriate routine checks as needed, and by default `juicefs gc` only performs scans:

```shell
$ juicefs gc sqlite3://myjfs.db

2022/11/10 11:35:53.662024 juicefs[24404] <INFO>: Meta address: sqlite3://myjfs.db [interface.go:402]
2022/11/10 11:35:53.662759 juicefs[24404] <INFO>: Data use file:///Users/herald/.juicefs/local/myjfs/ [gc.go:108]
  Listed slices count: 92
Scanned objects count: 91 / 91 [======================================]  done
  Valid objects count: 91
  Valid objects bytes: 7.67 MiB (8040969 Bytes)
 Leaked objects count: 0
 Leaked objects bytes: 0.00 b   (0 Bytes)
Skipped objects count: 0
Skipped objects bytes: 0.00 b   (0 Bytes)
2022/11/10 11:35:53.665015 juicefs[24404] <INFO>: scanned 91 objects, 91 valid, 0 leaked (0 bytes), 0 skipped (0 bytes) [gc.go:306]
```

### Purge leaked objects

When the `juicefs gc` command scans for "leaked objects", you can purge them with the `--delete` option. The client starts 10 threads by default to perform the purge operation, you can adjust the number of threads with the `--threads, -p` option.

```shell
$ juicefs gc sqlite3://myjfs.db --delete

2022/11/10 10:49:31.490016 juicefs[24086] <INFO>: Meta address: sqlite3://myjfs.db [interface.go:402]
2022/11/10 10:49:31.490831 juicefs[24086] <INFO>: Data use file:///Users/herald/.juicefs/local/myjfs/ [gc.go:108]
  Listed slices count: 92
Deleted pending count: 0
Scanned objects count: 103 / 103 [====================================]  done
  Valid objects count: 92
  Valid objects bytes: 7.67 MiB  (8045065 Bytes)
 Leaked objects count: 11
 Leaked objects bytes: 12.87 MiB (13494874 Bytes)
Skipped objects count: 0
Skipped objects bytes: 0.00 b    (0 Bytes)
2022/11/10 10:49:31.493682 juicefs[24086] <INFO>: scanned 103 objects, 92 valid, 11 leaked (13494874 bytes), 0 skipped (0 bytes) [gc.go:306]
```

You can then run `juicefs gc` again to check if the purge was successful.

## fsck

`juicefs fsck` is a tool that performs block-by-block comparison with metadata, mainly to fix various problems that may occur and can be fixed within the file system. It can help you find cases where records exist in the metadata engine but there is no corresponding data block in the object storage, and it can also check if the file attribute information exists.

```shell {5}
$ juicefs fsck sqlite3://myjfs2.db

2022/11/10 17:31:19.062348 juicefs[26158] <INFO>: Meta address: sqlite3://myjfs2.db [interface.go:402]
2022/11/10 17:31:19.063132 juicefs[26158] <INFO>: Data use file:///Users/herald/.juicefs/local/myjfs/ [fsck.go:73]
2022/11/10 17:31:19.065857 juicefs[26158] <ERROR>: can't find block 0/1/1063_0_2693747 for file /david-bruno-silva-Z19vToWBDIc-unsplash.jpg: stat /Users/herald/.juicefs/local/myjfs/chunks/0/1/1063_0_2693747: no such file or directory [fsck.go:146]
  Found blocks count: 68
  Found blocks bytes: 34.24 MiB (35904042 Bytes)
 Listed slices count: 65
Scanned slices count: 65 / 65 [=======================================]  done
Scanned slices bytes: 36.81 MiB (38597789 Bytes)
   Lost blocks count: 1
   Lost blocks bytes: 2.57 MiB  (2693747 Bytes)
2022/11/10 17:31:19.066243 juicefs[26158] <FATAL>: 1 objects are lost (2693747 bytes), 1 broken files:
        INODE: PATH
           57: /david-bruno-silva-Z19vToWBDIc-unsplash.jpg [fsck.go:168]
```

As you can see from the results, the `juicefs fsck` scan found a file corruption in the file system due to a missing data block.

Although the result indicates that the file in the backend storage is corrupted, it is still necessary to check if the file is accessible at the mount point, because JuiceFS will cache the recently accessed file data locally, and the version of the file before the corruption can be re-uploaded with the cached file data block to avoid losing data if it is already cached locally. You can look for cached data in the cache directory (i.e. the path corresponding to the `--cache-dir` option) based on the path of the block output from the `juicefs fsck` command, e.g. the path of the missing block in the above example is `0/1/1063_0_2693747`.
