---
title: 销毁文件系统
sidebar_position: 8
---

JuiceFS 客户端提供了 `destroy` 命令用以彻底销毁一个文件系统，销毁操作将会产生以下结果：

- 清空此文件系统的全部元数据记录；
- 清空此文件系统的全部数据块

销毁文件系统的命令格式如下：

```shell
juicefs destroy <METADATA URL> <UUID>
```

- `<METADATA URL>`：元数据引擎的 URL 地址；
- `<UUID>`：文件系统的 UUID。

## 查找文件系统的 UUID

JuiceFS 客户端的 `status` 命令可以查看一个文件系统的详细信息，只需指定文件系统的元数据引擎 URL 即可，例如：

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

## 销毁文件系统

:::danger 危险操作
销毁操作将导致文件系统关联的数据库记录和对象存储中的数据全部被清空，请务必先备份重要数据后再操作！
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

在销毁文件系统时，客户端会发出确认提示，请务必仔细核对文件系统信息，确认无误后输入 `y` 确认。

## 常见错误

```shell
2022/01/26 21:47:30.949149 juicefs[31483] <FATAL>: 1 sessions are active, please disconnect them first
```

如果收到类似上面的错误提示，说明文件系统没有被妥善卸载，请检查并确认卸载了所有挂载点后再行操作。
