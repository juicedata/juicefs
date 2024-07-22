---
title: 存储配额
sidebar_position: 4
---

JuiceFS 同时支持文件系统总配额和子目录配额，均可用于限制可用容量和可用 inode 数量。文件系统配额和目录配额均是硬限制，当文件系统总配额用尽时，后续写入会返回 `ENOSPC`（No space left）错误；而当目录配额用尽时，后续写入会返回 `EDQUOT`（Disk quota exceeded）错误。

:::tip 提示
存储限额设置会保存在元数据引擎中以供所有挂载点读取，每个挂载点的客户端也会缓存自己的已用容量和 inodes 数，周期性地向元数据引擎同步。与此同时，客户端也会周期性地从元数据引擎读取最新的用量值，从而实现用量信息在每个挂载点之间同步，但这种信息同步机制并不能保证用量数据被精确统计，可能会存在十秒级延迟。
:::

## 文件系统配额 {#file-system-quota}

JuiceFS v1.0 支持文件系统级别的存储配额。以 Linux 环境为例，使用系统自带的 `df` 命令可以看到，一个 JuiceFS 类型的文件系统默认的容量标识为 `1.0P` ：

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

### 限制总容量 {#limit-total-capacity}

可以在创建文件系统时通过 `--capacity` 设置容量限额，单位 GiB，例如创建一个可用容量为 100 GiB 文件系统的：

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
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

### 限制 inode 总量 {#limit-total-number-of-inodes}

在 Linux 系统中，每个文件（文件夹也是文件的一种）不论大小都有一个 inode，因此限制 inode 数量等同于限制文件数量。

可以在创建文件系统时通过 `--inodes` 设置限额，例如：

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
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

### 组合使用 {#limit-total-capacity-and-inodes}

你可以结合 `--capacity` 和 `--inodes` 更灵活的设置文件系统的容量限额，比如，创建一个文件系统，限制总容量为 100TiB 且仅允许存储 100000 文件：

```shell
juicefs format --storage minio \
    --bucket 127.0.0.1:9000/jfs1 \
    ... \
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
客户端每 60 秒从元数据引擎读取一次最新的文件系统存储限额设置来更新本地的设置，这个时间频率可能会造成其他挂载点最长需要 60 秒才能完成限额设置的更新。
:::

## 目录配额 {#directory-quota}

JuiceFS v1.1 开始支持目录级别的存储配额，可以使用 `juicefs quota` 子命令进行目录配额管理和查询。

:::tip 提示
由于用量统计需要挂载客户端支持，请确保除所有可写入客户端已升级到 v1.1.0 以上版本再使用此特性。
:::

### 限制目录容量 {#limit-directory-capacity}

可以使用 `juicefs quota set $METAURL --path $DIR --capacity $N` 设置目录容量限额，单位 GiB。例如给目录`/test`设置 1GiB 的容量配额：

```shell
$ juicefs quota set $METAURL --path /test --capacity 1
+-------+---------+---------+------+-----------+-------+-------+
|  Path |   Size  |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+---------+---------+------+-----------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% | unlimited |   314 |       |
+-------+---------+---------+------+-----------+-------+-------+
```

设置成功后你可以看到有一个表格描述当前设置配额的目录、配额大小、当前用量等信息。

:::tip 提示
`quota` 子命令的使用无需本地挂载点，期望输入的目录路径为相对 JuiceFS 根目录的路径而非本地挂载路径。给大目录设置配额可能需要等待较长时间，因为需要计算目录当前用量。
:::

如果需要查询某个目录的配额和当前用量，可以使用 `juicefs quota get $METAURL --path $DIR` 命令：

```shell
$ juicefs quota get $METAURL --path /test
+-------+---------+---------+------+-----------+-------+-------+
|  Path |   Size  |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+---------+---------+------+-----------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% | unlimited |   314 |       |
+-------+---------+---------+------+-----------+-------+-------+
```

也可以使用 `juicefs quota ls $METAURL` 命令列出所有的目录配额。

### 限制目录的 inode 总量 {#limit-total-number-of-directory-inodes}

可以使用 `juicefs quota set $METAURL --path $DIR --inodes $N` 设置目录 inode 限额，单位为个。例如给目录`/test`设置 400 个 inode 的配额：

```shell
$ juicefs quota set $METAURL --path /test --inodes 400
+-------+---------+---------+------+--------+-------+-------+
|  Path |   Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+---------+---------+------+--------+-------+-------+
| /test | 1.0 GiB | 1.6 MiB |   0% |    400 |   314 |   78% |
+-------+---------+---------+------+--------+-------+-------+
```

### 组合使用 {#limit-capacity-and-inodes-of-directory}

可以结合 `--capacity` 和 `--inodes` 更灵活地设置目录的容量限额。比如，给`/test`目录设置 10GiB 和 1000 个 inode 的配额：

```shell
$ juicefs quota set $METAURL --path /test --capacity 10 --inodes 1000
+-------+--------+---------+------+--------+-------+-------+
|  Path |  Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+--------+---------+------+--------+-------+-------+
| /test | 10 GiB | 1.6 MiB |   0% |  1,000 |   314 |   31% |
+-------+--------+---------+------+--------+-------+-------+
```

另外，你也可以不限制目录的容量和 inode 数（设为 `0` 表示不限制），只通过 `quota` 命令统计目录的当前用量：

```shell
$ juicefs quota set $METAURL --path /test --capacity 0 --inodes 0
+-------+-----------+---------+------+-----------+-------+-------+
|  Path |    Size   |   Used  | Use% |   Inodes  | IUsed | IUse% |
+-------+-----------+---------+------+-----------+-------+-------+
| /test | unlimited | 1.6 MiB |      | unlimited |   314 |       |
+-------+-----------+---------+------+-----------+-------+-------+
```

### 配额嵌套 {#nested-quota}

JuiceFS 允许自由地设置各级目录配额，实际使用的时候会递归地向上查询，确保当前目录用量满足每一级目录的配额设置。也就是说，就算父目录设置了一个较小的配额，也不影响子目录可以设置更大配额。

### 子目录挂载 {#subdirectory-mount}

JuiceFS 支持使用 [`--subdir`](../reference/command_reference.mdx#mount-metadata-options) 挂载任意子目录。如果挂载的子目录设置了目录配额，则可以使用系统自带的 `df` 命令查看目录配额和当前使用量。比如文件系统配额为 1PiB 和 10M 个 inode，而 `/test` 目录的配额为 1GiB 和 400 个 inode。使用根目录挂载时 `df` 命令的输出为：

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

而使用 `/test` 子目录挂载时，`df` 命令的输出为：

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

:::note 说明
当挂载的子目录没有设置配额，JuiceFS 会逐级往上查询知道找到最近的目录配额再返回给 `df`。如果有多级父目录均设置目录配额，JuiceFS 会在计算后返回最小的可用容量和 inode 数量。
:::

### 用量检查与修复 {#usage-check-and-fix}

由于目录用量的更新是滞后且异步的，在异常情况下可能会发生丢失（比如客户端意外退出）。我们可以使用 `juicefs quota check $METAURL --path $DIR` 命令进行检查或修复：

```shell
$ juicefs quota check $METAURL --path /test
2023/05/23 15:40:12.704576 juicefs[1638846] <INFO>: quota of /test is consistent [base.go:839]
+-------+--------+---------+------+--------+-------+-------+
|  Path |  Size  |   Used  | Use% | Inodes | IUsed | IUse% |
+-------+--------+---------+------+--------+-------+-------+
| /test | 10 GiB | 1.6 MiB |   0% |  1,000 |   314 |   31% |
+-------+--------+---------+------+--------+-------+-------+
```

目录用量正确时会输出当前的目录配额用量；失败时候则会输出错误日志：

```shell
$ juicefs quota check $METAURL --path /test
2023/05/23 15:48:17.494604 juicefs[1639997] <WARNING>: /test: quota(314, 4.0 KiB) != summary(314, 1.6 MiB) [base.go:843]
2023/05/23 15:48:17.494644 juicefs[1639997] <FATAL>: quota of /test is inconsistent, please repair it with --repair flag [main.go:31]
```

这时你可以使用 `--repair` 选项来修复目录用量：

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
