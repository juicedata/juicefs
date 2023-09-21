---
title: Directory Statistics
sidebar_position: 5
---

From JuiceFS v1.1.0, directory statistics is enabled by default when formatting a new volume (existing ones will stay disabled, you'll have to enable it explicitly). Directory stats accelerates `quota`, `info` and the `summary` subcommands, but comes with a minor performance cost.

:::tip
The usage statistic relies on the mount process, please do not enable this feature until all writable mount processes are upgraded to v1.1.0.
:::

## Enable directory stats {#enable-directory-stats}

Run `juicefs config $URL --dir-stats` to enable directory stats, after that, you can run `juicefs config $URL` to verify:

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

Upon seeing `"DirStats": true`, directory stats is successfully enabled, if you'd like to disable it:

```shell
$ juicefs config redis://localhost --dir-stats=false
2023/05/31 15:59:39.046134 juicefs[30752] <INFO>: Meta address: redis://localhost [interface.go:494]
2023/05/31 15:59:39.048301 juicefs[30752] <INFO>: Ping redis latency: 171.308µs [redis.go:3566]
 dir-stats: true -> false
```

:::tip
The [directory quota](./quota.md#directory-quota) functionality depends on directory stats, that's why setting a quota automatically enables directory stats. To disable directory stats for such volume, you'll need to remove all quotas.
:::

## Check directory stats {#check-directory-stats}

Use `juicefs info $PATH` to check stats for a single directory:

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

Run `juicefs info -r $PATH` to recursively sum up:

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

You can also use `juicefs summary $PATH` to list all directory stats:

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

:::note
Directory stats only stores single directory usage, to get a recursive sum, you'll need to use `juicefs info -r`, this could be a costly operation for large directories, if you need to frequently get the total stats for particular directories, consider [setting an empty quota](./quota.md#limit-capacity-and-inodes-of-directory) on such directories, to achieve recursive stats this way.

Different from Community Edition, JuiceFS Enterprise Edition already put a [recursive sum](/docs/cloud/guide/view_storage_usage) on directory stats, you can directly view the total usage by running `ls -lh`.
:::

## Troubleshooting {#troubleshooting}

Directory stats is calculated asynchronously, and can potentially produce inaccurate results when clients run into problems, `juicefs info`, `juicefs summary` and `juicefs quota` all provide a `--strict` option to run in strict mode, which bypasses directory stats, as opposed to the default fast mode.

When strict mode and fast mode produces different results, use `juicefs fsck` to fix things up:

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

# Check directory stats for /d
$ juicefs fsck sqlite3://test.db --path /d --sync-dir-stat
2023/05/31 17:14:34.700239 juicefs[32667] <INFO>: Meta address: sqlite3://test.db [interface.go:494]
[xorm] [info]  2023/05/31 17:14:34.700291 PING DATABASE sqlite3
2023/05/31 17:14:34.701553 juicefs[32667] <WARNING>: usage stat of /d should be &{1073741824 1073741824 1}, but got &{469762048 469762048 1} [base.go:2010]
2023/05/31 17:14:34.701577 juicefs[32667] <WARNING>: Stat of path /d (inode 2) should be synced, please re-run with '--path /d --repair --sync-dir-stat' to fix it [base.go:2025]
2023/05/31 17:14:34.701615 juicefs[32667] <FATAL>: some errors occurred, please check the log of fsck [main.go:31]

# Fix directory stats for /d
$ juicefs fsck -v sqlite3://test.db --path /d --sync-dir-stat --repair
2023/05/31 17:14:43.445153 juicefs[32721] <DEBUG>: maxprocs: Leaving GOMAXPROCS=8: CPU quota undefined [maxprocs.go:47]
2023/05/31 17:14:43.445289 juicefs[32721] <INFO>: Meta address: sqlite3://test.db [interface.go:494]
[xorm] [info]  2023/05/31 17:14:43.445350 PING DATABASE sqlite3
2023/05/31 17:14:43.462374 juicefs[32721] <DEBUG>: Stat of path /d (inode 2) is successfully synced [base.go:2018]

# Verify that stats has been fixed
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
