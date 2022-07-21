---
sidebar_label: 存储配额
sidebar_position: 5
---
# 存储配额

JuiceFS v0.14.2 开始支持文件系统级别的存储配额，该功能包括：

- 限制文件系统的总可用容量
- 限制文件系统的 inode 总数

:::tip 提示
存储限额设置会保存在元数据引擎中以供所有挂载点读取，每个挂载点的客户端也会缓存自己的已用容量和 inodes 数，每秒向元数据引擎同步一次。与此同时，客户端每 10 秒会从元数据引擎读取最新的用量值，从而实现用量信息在每个挂载点之间同步，但这种信息同步机制并不能保证用量数据被精确统计。
:::

## 查看文件系统的基本信息

以 Linux 环境为例，使用系统自带的 `df` 命令可以看到，一个 JuiceFS 类型的文件系统默认的容量标识为 `1.0P` ：

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  1.0P  682M  1.0P    1% /mnt
```

:::note 说明
JuiceFS 通过 FUSE 实现对 POSIX 接口的支持，因为底层通常是容量能够无限扩展的对象存储，所以标识容量只是一个估值（也代表无限制）并非实际容量，它会随着实际用量动态变化。
:::

通过客户端自带的 `config` 命令可以查看一个文件系统的详细信息：

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

## 限制总容量

可以在创建文件系统时通过 `--capacity` 设置容量限额，单位 GiB，例如创建一个可用容量为 100 GiB 文件系统的：

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--capacity 100 \
$METAURL myjfs
```

也可以通过 `config` 命令，为一个已创建的文件系统设置容量限额：

```shell
$ juicefs config $METAURL --capacity 100
2022/01/27 12:31:39.506322 juicefs[16259] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:31:39.521232 juicefs[16259] <WARNING>: The latency to database is too high: 14.771783ms
  capacity: 0 GiB -> 100 GiB
```

设置了存储限额的文件系统，标识容量会变成限制容量：

```shell
$ df -Th | grep juicefs
JuiceFS:ujfs   fuse.juicefs  100G  682M  100G    1% /mnt
```

## 限制 inode 总量

在 Linux 系统中，每个文件（文件夹也是文件的一种）不论大小都有一个 inode，因此限制 inode 数量等同于限制文件数量。

可以在创建文件系统时通过 `--inodes` 设置限额，例如：

```
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--inodes 100 \
$METAURL myjfs
```

以上命令创建的文件系统仅允许存储 100 个文件，但不限制单个文件的大小，比如单个文件 1TB 甚至更大也没有问题，只要文件总数不超过 100 个即可。

也可以通过 `config` 命令，为一个已创建的文件系统设置容量限额：

```shell
$ juicefs config $METAURL --inodes 100
2022/01/27 12:35:37.311465 juicefs[16407] <INFO>: Meta address: postgres://herald@127.0.0.1:5432/jfs1
2022/01/27 12:35:37.322991 juicefs[16407] <WARNING>: The latency to database is too high: 11.413961ms
    inodes: 0 -> 100
```

## 组合使用

你可以结合 `--capacity` 和 `--inodes` 更灵活的设置文件系统的容量限额，比如，创建一个文件系统，限制总容量为 100TiB 且仅允许存储 100000 文件：

```shell
$ juicefs format --storage minio \
--bucket 127.0.0.1:9000/jfs1 \
...
--capacity 102400 \
--inodes 100000 \
$METAURL myjfs
```

同样地，对于已创建的文件系统，可分别进行设置：

```shell
juicefs config $METAURL --capacity 102400
```

```shell
juicefs config $METAURL --inodes 100000
```

:::tip 提示
客户端每 60 秒从元数据引擎读取一次最新的存储限额设置来更新本地的设置，这个时间频率可能会造成其他挂载点最长需要 60 秒才能完成限额设置的更新。
:::
