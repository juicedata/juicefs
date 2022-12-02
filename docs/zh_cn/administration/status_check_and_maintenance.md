---
title: 状态检查 & 维护
sidebar_position: 4
---

任何一种存储系统在投入使用之后都需要定期进行检查和维护，尽早发现并修复潜在的问题，从而保证文件系统可靠运行、存储的数据完整一致。

JuiceFS 提供了一系列检查和维护文件系统的工具，不但可以帮助我们了解文件系统的基本信息、运行状态，还能够帮助我们更容易地发现和修复潜在的问题。

## status

`juicefs status` 命令用来查看一个 JuiceFS 文件系统的基本信息以及所有活跃的会话状态（包括挂载、SDK 访问、S3 网关、WebDAV 连接）。

文件系统的基本信息中包括名称、UUID、存储类型、对象存储 Bucket、回收站状态等。

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

通过 `--session, -s` 选项指定会话的 `Sid` 可以进一步显示该会话的更多信息：

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

根据会话的状态，信息中还可能包括：

- Sustained inodes：这些是已经被删掉的文件，但是因为在这个会话中已经被打开，因此会被暂时保留直至文件关闭。
- Flocks：被这个会话加锁的文件的 BSD 锁信息
- Plocks：被这个会话加锁的文件的 POSIX 锁信息

## info

`juicefs info` 用于检查指定文件或目录的元数据信息，其中包括该文件对应的每个 block 在对象存储上的对象路径。

### 检查一个文件的元数据

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

### 检查一个目录的元数据

该命令默认只检查一层目录：

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

如果希望递归检查所有子目录，需要指定 `--recursive, -r` 选项：

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

### 使用 inode 检查元数据

还可以通过 inode 来反向查找文件路径及数据块的信息，但需要先进入挂载点：

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

## gc {#gc}

`juicefs gc` 是一个专门用来处理「对象泄漏」，以及因为覆盖写而产生的碎片数据的工具。它以元数据信息为基准与对象存储中的数据进行逐一扫描比对，从而找出或清理对象存储上需要处理的数据块。

:::info 说明
**对象泄漏**是指数据块在对象存储，但元数据引擎中没有对应的记录的情况。对象泄漏极少出现，成因可能是程序 bug、元数据引擎或对象存储的未预期问题、断电、断网等等。
:::

:::tip 提示
文件在上传到对象存储时可能产生临时的中间文件，它们会在写入完成后被清理。为了避免中间文件被误判为泄漏的对象，`juicefs gc` 默认会跳过最近 1 个小时上传的文件。可以通过 `JFS_GC_SKIPPEDTIME` 环境变量调整跳过的时间范围（单位为秒）。例如设置跳过最近 2 个小时的文件：`export JFS_GC_SKIPPEDTIME=7200`。
:::

:::tip 提示
因为 `juicefs gc` 命令会扫描对象存储中的所有对象，所以对于数据量较大的文件系统执行这个命令会有一定开销。
:::

### 扫描「泄漏的对象」

虽然几乎不会出现对象泄漏的情况，但你仍然可以根据需要进行相应例行检查，默认情况下 `juicefs gc` 仅执行扫描：

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

### 清理「泄漏的对象」

当 `juicefs gc` 命令扫描到了「泄漏的对象」，可以通过 `--delete` 选项对它们进行清理。客户端默认启动 10 个线程执行清理操作，可以使用 `--threads, -p` 选项来调整线程数量。

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

随后可以再执行一次 `juicefs gc` 检查是否清理成功。

## fsck

`juicefs fsck` 是一个以数据块为基准与元数据进行逐一扫描比对的工具，主要用来修复文件系统内可能发生而且可以修复的各种问题。它可以帮你找到元数据引擎中存在记录，但对象存储中没有对应数据块的情况，还可以检查文件的属性信息是否存在。

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

从结果可以看到，`juicefs fsck` 扫描发现文件系统中因为丢失了数据块致使一个文件损坏。

虽然结果表明后端存储中的文件已经损坏，但还是有必要去挂载点查验一下文件是否可以访问，因为 JuiceFS 会在本地缓存最近访问过的文件数据，文件损坏之前的版本如果已经缓存在本地，则可以将缓存的文件数据块重新上传以避免丢失数据。你可以在缓存目录（即 `--cache-dir` 选项对应的路径）中根据 `juicefs fsck` 命令输出的数据块路径查找是否存在缓存数据，例如上面例子中丢失的数据块路径为 `0/1/1063_0_2693747`。
