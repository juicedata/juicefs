---
title: 状态检查 & 维护
sidebar_position: 4
---

任何一种存储系统在投入使用之后都需要定期进行检查和维护，尽早发现并修复潜在的问题，从而保证文件系统可靠运行、存储的数据完整一致。

JuiceFS 提供了一系列检查和维护文件系统的工具，不但可以帮助我们了解文件系统的基本信息、运行状态，还能够帮助我们更容易地发现和修复潜在的问题。

## status

`juicefs status` 命令用来查看一个 JuiceFS 文件系统的基本信息，所有活跃的会话状态（包括挂载、SDK 访问、S3 网关、WebDAV 连接）以及统计信息。

文件系统的基本信息中包括名称、UUID、存储类型、对象存储 Bucket、回收站状态等；统计信息默认有文件系统的配额与用量。

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
  ],
  "Statistic": {
    "UsedSpace": 4886528,
    "AvailableSpace": 1125899901956096,
    "UsedInodes": 643,
    "AvailableInodes": 10485760,
  }
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

通过 `--more, -m` 选项扫描 trash 中的文件和 slice，以及延迟删除的文件和 slice：

```shell
juicefs status -m redis://xxx.cache.amazonaws.com:6379/1
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
  ],
  "Statistic": {
    "UsedSpace": 4886528,
    "AvailableSpace": 1125899901956096,
    "UsedInodes": 643,
    "AvailableInodes": 10485760,
    "TrashFileCount": 277,
    "TrashFileSize": 1152597,
    "PendingDeletedFileCount": 156,
    "PendingDeletedFileSize": 1313577,
    "TrashSliceCount": 581,
    "TrashSliceSize": 1845292,
    "PendingDeletedSliceCount": 1378,
    "PendingDeletedSliceSize": 26245344,
  }
```

## info

`juicefs info` 用于检查指定文件或目录的元数据信息，其中包括该文件对应的每个 block 在对象存储上的对象路径以及作用于该文件的 flock 与 plock。

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
flocks:
+-----+----------------------+------+
| Sid |         Owner        | Type |
+-----+----------------------+------+
| 4   | 14034871352581537016 |    W |
+-----+----------------------+------+
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

默认情况下 `juicefs info -r` 在 `fast` 模式下运行，它结果中的目录用量不一定精准。如果你怀疑其准确性，可以使用 `--strict` 选项查看精准用量：

```shell
$ juicefs info -r ./mnt --strict

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

## summary

JuiceFS 1.1.0 之后支持 `summary` 子命令，可以递归列出目录树和各层的使用量：

```bash
$ juicefs summary /mnt/jfs/
+---------------------------+---------+------+-------+
|            PATH           |   SIZE  | DIRS | FILES |
+---------------------------+---------+------+-------+
| /                         | 1.0 GiB |  100 |   445 |
| d/                        | 1.0 GiB |    1 |     1 |
| d/test1                   | 1.0 GiB |    0 |     1 |
| pjdfstest/                | 2.8 MiB |   39 |   304 |
| pjdfstest/tests/          | 1.1 MiB |   18 |   240 |
| pjdfstest/autom4te.cache/ | 692 KiB |    1 |     7 |
| pjdfstest/.git/           | 432 KiB |   17 |    26 |
| pjdfstest/configure       | 176 KiB |    0 |     1 |
| pjdfstest/config.log      |  84 KiB |    0 |     1 |
| pjdfstest/pjdfstest.o     |  80 KiB |    0 |     1 |
| pjdfstest/pjdfstest       |  68 KiB |    0 |     1 |
| pjdfstest/aclocal.m4      |  44 KiB |    0 |     1 |
| pjdfstest/pjdfstest.c     |  40 KiB |    0 |     1 |
| pjdfstest/config.status   |  36 KiB |    0 |     1 |
| pjdfstest/...             | 164 KiB |    2 |    24 |
| roa/                      | 2.3 MiB |   59 |   140 |
| roa/.git/                 | 1.4 MiB |   17 |    26 |
| roa/roa/                  | 252 KiB |    9 |    30 |
| roa/integration/          | 148 KiB |   13 |    22 |
| roa/roa-core/             | 124 KiB |    4 |    17 |
| roa/Cargo.lock            |  84 KiB |    0 |     1 |
| roa/roa-async-std/        |  36 KiB |    2 |     6 |
| roa/.github/              |  32 KiB |    2 |     6 |
| roa/examples/             |  32 KiB |    1 |     7 |
| roa/roa-diesel/           |  32 KiB |    2 |     5 |
| roa/assets/               |  28 KiB |    2 |     5 |
| roa/...                   | 108 KiB |    6 |    15 |
+---------------------------+---------+------+-------+
```

可以使用 `--depth value, -d value` 和 `--entries value, -e value` 选项控制目录层级和每层显示的最大数量：

```bash
$ juicefs summary /mnt/jfs/ -d 3 -e 3
+------------------------------------+---------+------+-------+
|                PATH                |   SIZE  | DIRS | FILES |
+------------------------------------+---------+------+-------+
| /                                  | 1.0 GiB |  100 |   445 |
| d/                                 | 1.0 GiB |    1 |     1 |
| d/test1                            | 1.0 GiB |    0 |     1 |
| pjdfstest/                         | 2.8 MiB |   39 |   304 |
| pjdfstest/tests/                   | 1.1 MiB |   18 |   240 |
| pjdfstest/tests/open/              | 112 KiB |    1 |    26 |
| pjdfstest/tests/rename/            | 112 KiB |    1 |    25 |
| pjdfstest/tests/link/              |  76 KiB |    1 |    18 |
| pjdfstest/tests/...                | 776 KiB |   14 |   171 |
| pjdfstest/autom4te.cache/          | 692 KiB |    1 |     7 |
| pjdfstest/autom4te.cache/output.0  | 180 KiB |    0 |     1 |
| pjdfstest/autom4te.cache/output.1  | 180 KiB |    0 |     1 |
| pjdfstest/autom4te.cache/output.2  | 180 KiB |    0 |     1 |
| pjdfstest/autom4te.cache/...       | 148 KiB |    0 |     4 |
| pjdfstest/.git/                    | 432 KiB |   17 |    26 |
| pjdfstest/.git/objects/            | 252 KiB |    3 |     2 |
| pjdfstest/.git/hooks/              |  64 KiB |    1 |    13 |
| pjdfstest/.git/logs/               |  32 KiB |    5 |     3 |
| pjdfstest/.git/...                 |  80 KiB |    7 |     8 |
| pjdfstest/...                      | 692 KiB |    2 |    31 |
| roa/                               | 2.3 MiB |   59 |   140 |
| roa/.git/                          | 1.4 MiB |   17 |    26 |
| roa/.git/objects/                  | 1.3 MiB |    3 |     2 |
| roa/.git/hooks/                    |  64 KiB |    1 |    13 |
| roa/.git/logs/                     |  32 KiB |    5 |     3 |
| roa/.git/...                       |  72 KiB |    7 |     8 |
| roa/roa/                           | 252 KiB |    9 |    30 |
| roa/roa/src/                       | 228 KiB |    7 |    27 |
| roa/roa/README.md                  | 8.0 KiB |    0 |     1 |
| roa/roa/templates/                 | 8.0 KiB |    1 |     1 |
| roa/roa/...                        | 4.0 KiB |    0 |     1 |
| roa/integration/                   | 148 KiB |   13 |    22 |
| roa/integration/diesel-example/    |  52 KiB |    4 |     9 |
| roa/integration/multipart-example/ |  36 KiB |    4 |     5 |
| roa/integration/juniper-example/   |  32 KiB |    2 |     5 |
| roa/integration/...                |  24 KiB |    2 |     3 |
| roa/...                            | 476 KiB |   19 |    62 |
+------------------------------------+---------+------+-------+
```

此命令也支持标准 csv 输出，用于其它软件解析：

```bash
$ juicefs summary /mnt/jfs/ --csv
PATH,SIZE,DIRS,FILES
/,1079132160,100,445
d/,1073745920,1,1
d/test1,1073741824,0,1
pjdfstest/,2969600,39,304
pjdfstest/tests/,1105920,18,240
pjdfstest/autom4te.cache/,708608,1,7
pjdfstest/.git/,442368,17,26
pjdfstest/configure,180224,0,1
pjdfstest/config.log,86016,0,1
pjdfstest/pjdfstest.o,81920,0,1
pjdfstest/pjdfstest,69632,0,1
pjdfstest/aclocal.m4,45056,0,1
pjdfstest/pjdfstest.c,40960,0,1
pjdfstest/config.status,36864,0,1
pjdfstest/...,167936,2,24
roa/,2412544,59,140
roa/.git/,1511424,17,26
roa/roa/,258048,9,30
roa/integration/,151552,13,22
roa/roa-core/,126976,4,17
roa/Cargo.lock,86016,0,1
roa/roa-async-std/,36864,2,6
roa/.github/,32768,2,6
roa/examples/,32768,1,7
roa/roa-diesel/,32768,2,5
roa/assets/,28672,2,5
roa/...,110592,6,15
```

默认情况下 `juicefs summary` 在 `fast` 模式下运行，它结果中的目录用量不一定精准。如果你怀疑其准确性，可以使用 `--strict` 选项查看精准用量。

## gc {#gc}

`juicefs gc` 是一个用来处理「对象泄漏」与「待清理对象」，以及因为覆盖写而产生的碎片数据的工具。它以元数据信息为基准与对象存储中的数据进行逐一扫描比对，从而找出或清理对象存储上需要处理的数据块。

:::info 说明
**对象泄漏**是指数据块在对象存储，但元数据引擎中没有对应的记录的情况。对象泄漏极少出现，成因可能是程序 bug、元数据引擎或对象存储的未预期问题、断电、断网等等。
**待清理对象**是指被原数据引擎标记为删除但还未清理的对象。待删除对象很常见，比如到期的 trash 文件与 slice 和延迟删除的文件与 slice。
:::

:::tip 提示
虽然几乎不会出现对象泄漏的情况，但你仍然可以根据需要进行相应例行检查。文件在上传到对象存储时可能产生临时的中间文件，它们会在写入完成后被清理。为了避免中间文件被误判为泄漏的对象，`juicefs gc` 默认会跳过最近 1 个小时上传的文件。可以通过 `JFS_GC_SKIPPEDTIME` 环境变量调整跳过的时间范围（单位为秒）。例如设置跳过最近 2 个小时的文件：`export JFS_GC_SKIPPEDTIME=7200`。
:::

:::tip 提示
因为 `juicefs gc` 命令会扫描对象存储中的所有对象，所以对于数据量较大的文件系统执行这个命令会有一定开销。另外使用此命令之前请确保您不需要回滚到旧版本元数据，并且建议您备份对象存储数据。
:::

### 扫描

默认情况下 `juicefs gc` 仅执行扫描：

```shell
$ juicefs gc sqlite3://myjfs.db
Pending deleted files: 0                            0.0/s         
 Pending deleted data: 0.0 b   (0 Bytes)            0.0 b/s       
Cleaned pending files: 0                            0.0/s         
 Cleaned pending data: 0.0 b   (0 Bytes)            0.0 b/s       
        Listed slices: 4437                         82800.0/s     
         Trash slices: 0                            0.0/s         
           Trash data: 0.0 b   (0 Bytes)            0.0 b/s       
 Cleaned trash slices: 0                            0.0/s         
   Cleaned trash data: 0.0 b   (0 Bytes)            0.0 b/s       
      Scanned objects: 4741/4741 [==============================================================]  387369.2/s used: 12.247821ms
        Valid objects: 4741                         395521.0/s    
           Valid data: 1.7 GiB (1846388716 Bytes)   143.6 GiB/s   
    Compacted objects: 0                            0.0/s         
       Compacted data: 0.0 b   (0 Bytes)            0.0 b/s       
       Leaked objects: 0                            0.0/s         
          Leaked data: 0.0 b   (0 Bytes)            0.0 b/s       
      Skipped objects: 0                            0.0/s         
         Skipped data: 0.0 b   (0 Bytes)            0.0 b/s       
2023/06/09 10:14:33.683384 juicefs[280403] <INFO>: scanned 4741 objects, 4741 valid, 0 compacted (0 bytes), 0 leaked (0 bytes), 0 delslices (0 bytes), 0 delfiles (0 bytes), 0 skipped (0 bytes) [gc.go:379]
```

### 清理

当 `juicefs gc` 命令扫描到了「泄漏的对象」或「待清理对象」，可以通过 `--delete` 选项对它们进行清理。客户端默认启动 10 个线程执行清理操作，可以使用 `--threads, -p` 选项来调整线程数量。

```shell
$ juicefs gc sqlite3://myjfs.db --delete
Cleaned pending slices: 0                            0.0/s         
 Pending deleted files: 0                            0.0/s         
  Pending deleted data: 0.0 b   (0 Bytes)            0.0 b/s       
 Cleaned pending files: 0                            0.0/s         
  Cleaned pending data: 0.0 b   (0 Bytes)            0.0 b/s       
         Cleaned trash: 0                            0.0/s         
Cleaned detached nodes: 0                            0.0/s         
         Listed slices: 4437                         75803.6/s     
          Trash slices: 0                            0.0/s         
            Trash data: 0.0 b   (0 Bytes)            0.0 b/s       
  Cleaned trash slices: 0                            0.0/s         
    Cleaned trash data: 0.0 b   (0 Bytes)            0.0 b/s       
       Scanned objects: 4741/4741 [==============================================================]  337630.2/s used: 14.056704ms
         Valid objects: 4741                         345974.4/s    
            Valid data: 1.7 GiB (1846388716 Bytes)   125.6 GiB/s   
     Compacted objects: 0                            0.0/s         
        Compacted data: 0.0 b   (0 Bytes)            0.0 b/s       
        Leaked objects: 0                            0.0/s         
           Leaked data: 0.0 b   (0 Bytes)            0.0 b/s       
       Skipped objects: 0                            0.0/s         
          Skipped data: 0.0 b   (0 Bytes)            0.0 b/s       
2023/06/09 10:15:49.819995 juicefs[280474] <INFO>: scanned 4741 objects, 4741 valid, 0 compacted (0 bytes), 0 leaked (0 bytes), 0 delslices (0 bytes), 0 delfiles (0 bytes), 0 skipped (0 bytes) [gc.go:379]
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

### 强制同步目录用量

在[目录用量统计](../guide/dir-stats.md)中我们介绍了这个新功能。虽然 fsck 默认会发现以及修复明显损坏的目录用量，但目录用量仍有可能不精准。我们可以使用 `--sync-dir-stat` 选项来强制检查或修复目录用量：

```bash
$ juicefs fsck redis://localhost --path /d --sync-dir-stat
2023/06/07 15:59:14.080820 juicefs[228395] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/06/07 15:59:14.082555 juicefs[228395] <INFO>: Ping redis latency: 49.904µs [redis.go:3569]
2023/06/07 15:59:14.083412 juicefs[228395] <WARNING>: usage stat of /d should be &{1073741824 1073741824 1}, but got &{0 0 0} [base.go:2026]
2023/06/07 15:59:14.083443 juicefs[228395] <WARNING>: Stat of path /d (inode 10701) should be synced, please re-run with '--path /d --repair --sync-dir-stat' to fix it [base.go:2041]
2023/06/07 15:59:14.083473 juicefs[228395] <FATAL>: some errors occurred, please check the log of fsck [main.go:31]

$ juicefs fsck redis://localhost --path /d --repair --sync-dir-stat
2023/06/07 16:00:43.043851 juicefs[228487] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/06/07 16:00:43.051556 juicefs[228487] <INFO>: Ping redis latency: 577.29µs [redis.go:3569]

# 成功修复
$ juicefs fsck redis://localhost --path /d --sync-dir-stat
2023/06/07 16:01:08.401972 juicefs[228547] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/06/07 16:01:08.404041 juicefs[228547] <INFO>: Ping redis latency: 85.566µs [redis.go:3569]
```

## compact {#compact}

`juicefs compact` 是 v1.2 版本中新增的功能，它是一个用来处理因为覆盖写而产生的碎片数据的工具。它将随机写产生的大量不连续的 slice 进行合并或清理，从而提升文件系统的读性能。

相比于 `juicefs gc` 对整个文件系统进行垃圾回收和碎片整理，`juicefs compact` 可指定目录处理因为覆盖写而产生的碎片数据。

```shell
juicefs compact /mnt/jfs/foo
```

另外，可以使用 `-p, --threads` 选项指定并发线程数，以加快处理速度。默认值为 10，可以根据实际情况调整。

```shell
juicefs compact /mnt/jfs/foo -p 20
```
