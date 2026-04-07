---
title: 目录用量统计
sidebar_position: 5
---

JuiceFS 在 v1.1.0 开始支持目录用量统计并在文件系统格式化时默认开启，旧版本 volume 迁移到新版本后默认关闭（需要[手动开启](#enable-directory-stats)）。目录用量统计可以加速 `quota`、`info` 和 `summary` 等子命令，但由于客户端会异步更新统计信息，启用后也会带来少量开销。

:::tip 提示
由于用量统计需要挂载客户端支持，请确保除所有可写入客户端已升级到 v1.1.0 以上版本再启用此特性。
:::

## 启用目录用量统计 {#enable-directory-stats}

运行 `juicefs config $URL --dir-stats` 来启用目录统计，启用以后，使用 `juicefs config $URL` 命令确认生效：

```shell
$ juicefs config redis://localhost
2023/05/31 15:56:39.721188 juicefs[30626] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/05/31 15:56:39.723284 juicefs[30626] <INFO>: Ping redis latency: 159.226µs [redis.go:3566]
{
  "Name": "myjfs",
  "UUID": "82db28de-bf5f-43bf-bba3-eb3535a86c48",
  "Storage": "file",
  "Bucket": "/root/.juicefs/local/",
  "BlockSize": 4096,
  "Compression": "none",
  "EncryptAlgo": "aes256gcm-rsa",
  "TrashDays": 1,
  "MetaVersion": 1,
  "DirStats": true
}
```

可以看到 `"DirStats": true` 代表目录用量统计已启用，我们可以尝试禁用它：

```shell
$ juicefs config redis://localhost --dir-stats=false
2023/05/31 15:59:39.046134 juicefs[30752] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/05/31 15:59:39.048301 juicefs[30752] <INFO>: Ping redis latency: 171.308µs [redis.go:3566]
 dir-stats: true -> false
```

:::tip 提示
[目录配额](./quota.md#directory-quota)功能依赖目录用量统计，为目录设置配额后会自动开启目录用量统计，并且需要删除所有目录配额后才能禁用目录用量统计。
:::

## 查看目录统计 {#check-directory-stats}

运行 `juicefs info $PATH` 查看单层目录的统计用量：

```shell
$ juicefs info /mnt/jfs/pjdfstest/
/mnt/jfs/pjdfstest/ :
  inode: 2
  files: 10
   dirs: 4
 length: 43.74 KiB (44794 Bytes)
   size: 92.00 KiB (94208 Bytes)
   path: /pjdfstest
```

也可以使用 `juicefs info -r $PATH` 递归查看目录统计并汇总：

```shell
/mnt/jfs/pjdfstest/: 278                       921.0/s
/mnt/jfs/pjdfstest/: 1.6 MiB (1642496 Bytes)   5.2 MiB/s
/mnt/jfs/pjdfstest/ :
  inode: 2
  files: 278
   dirs: 37
 length: 592.42 KiB (606638 Bytes)
   size: 1.57 MiB (1642496 Bytes)
   path: /pjdfstest
```

另外你可以使用 `juicefs summary $PATH` 命令来查看各层级的目录用量：

```shell
$ ./juicefs summary /mnt/jfs/pjdfstest/
/mnt/jfs/pjdfstest/: 315                       1044.4/s
/mnt/jfs/pjdfstest/: 1.6 MiB (1642496 Bytes)   5.2 MiB/s
+------------------+---------+------+-------+
|       PATH       |   SIZE  | DIRS | FILES |
+------------------+---------+------+-------+
| /                | 1.6 MiB |   37 |   278 |
| tests/           | 1.1 MiB |   18 |   240 |
| tests/open/      | 112 KiB |    1 |    26 |
| tests/...        | 328 KiB |    7 |    71 |
| .git/            | 432 KiB |   17 |    26 |
| .git/objects/    | 252 KiB |    3 |     2 |
| ...              |  12 KiB |    0 |     3 |
+------------------+---------+------+-------+
```

:::note 说明
目录统计只计算每个目录的单层用量，如果要查看递归统计用量，需要使用 `juicefs info -r`，对于大目录，遍历汇总可能带来很大的开销。如需持续查看某些特定目录的总用量，可参考目录配额通过[设置空配额](./quota.md#limit-capacity-and-inodes-of-directory)的方式统计目录总用量。

与社区版不同，JuiceFS 企业版的目录大小已经进行了[递归统计](/docs/zh/cloud/guide/quota#file-directory-size)，可以直接用 `ls -lh` 看到递归统计的目录总大小。
:::

## 故障和修复 {#troubleshooting}

由于目录用量是异步统计，当客户端发生异常时可能会丢失部分统计值导致结果不准确。`juicefs info`、`juicefs summary` 和 `juicefs quota` 命令均配有 `--strict` 选项在严苛模式下运行以绕过目录统计（默认模式一般称为快速模式，fast mode）。

如果发现严格模式和快速模式结果不一致，考虑使用 `juicefs fsck` 命令进行诊断和修复：

```shell
$ juicefs info -r /jfs/d
/jfs/d: 1                             3.3/s
/jfs/d: 448.0 MiB (469766144 Bytes)   1.4 GiB/s
/jfs/d :
  inode: 2
  files: 1
   dirs: 1
 length: 448.00 MiB (469762048 Bytes)
   size: 448.00 MiB (469766144 Bytes)
   path: /d

$ juicefs info -r --strict /jfs/d
/jfs/d: 1                            3.3/s
/jfs/d: 1.0 GiB (1073745920 Bytes)   3.3 GiB/s
/jfs/d :
  inode: 2
  files: 1
   dirs: 1
 length: 1.00 GiB (1073741824 Bytes)
   size: 1.00 GiB (1073745920 Bytes)
   path: /d

# 检查目录 /d 的用量统计
$ juicefs fsck sqlite3://test.db --path /d --sync-dir-stat
2023/05/31 17:14:34.700239 juicefs[32667] <INFO>: Meta address: sqlite3://test.db [interface.go:494]
[xorm] [info]  2023/05/31 17:14:34.700291 PING DATABASE sqlite3
2023/05/31 17:14:34.701553 juicefs[32667] <WARNING>: usage stat of /d should be &{1073741824 1073741824 1}, but got &{469762048 469762048 1} [base.go:2010]
2023/05/31 17:14:34.701577 juicefs[32667] <WARNING>: Stat of path /d (inode 2) should be synced, please re-run with '--path /d --repair --sync-dir-stat' to fix it [base.go:2025]
2023/05/31 17:14:34.701615 juicefs[32667] <FATAL>: some errors occurred, please check the log of fsck [main.go:31]

# 修复目录 /d 的用量统计
$ juicefs fsck -v sqlite3://test.db --path /d --sync-dir-stat --repair
2023/05/31 17:14:43.445153 juicefs[32721] <DEBUG>: maxprocs: Leaving GOMAXPROCS=8: CPU quota undefined [maxprocs.go:47]
2023/05/31 17:14:43.445289 juicefs[32721] <INFO>: Meta address: sqlite3://test.db [interface.go:494]
[xorm] [info]  2023/05/31 17:14:43.445350 PING DATABASE sqlite3
2023/05/31 17:14:43.462374 juicefs[32721] <DEBUG>: Stat of path /d (inode 2) is successfully synced [base.go:2018]

# 验证
$ juicefs info -r /jfs/d
/jfs/d: 1                            3.3/s
/jfs/d: 1.0 GiB (1073745920 Bytes)   3.3 GiB/s
/jfs/d :
  inode: 2
  files: 1
   dirs: 1
 length: 1.00 GiB (1073741824 Bytes)
   size: 1.00 GiB (1073745920 Bytes)
   path: /d
```
