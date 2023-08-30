---
title: 克隆文件或目录
sidebar_position: 6
---

对指定数据进行克隆，创建克隆时不会实际拷贝对象存储数据，而是仅拷贝元数据，因此不论对多大的文件或目录进行克隆，都非常快。对于 JuiceFS，这个命令是 `cp` 更好的替代，甚至对于 Linux 客户端来说，如果所使用的内核支持 [`copy_file_range`](https://man7.org/linux/man-pages/man2/copy_file_range.2.html)，那么调用 `cp` 时，实际发生的也是同样的元数据拷贝，调用将会格外迅速。

![clone](../images/juicefs-clone.svg)

克隆结果是纯粹的元数据拷贝，实际引用的对象存储块和源文件相同，因此在各方面都和源文件一样，可以正常读写。有任何一方文件数据被实际修改时，对应的数据块变更会以写入时复制（Copy-on-Write）的方式，写入到新的数据块，而其他未经修改的文件区域，由于对象存储数据块仍然相同，所以引用关系依然保持不变。

需要注意的是，**克隆产生的元数据，也同样占用文件系统存储空间，以及元数据引擎的存储空间**，因此对庞大的目录进行克隆操作时请格外谨慎。

```shell
juicefs clone SRC DST

# 克隆文件
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# 克隆目录
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2
```

## 一致性保证 {#consistency}

`clone` 命令提供如下的一致性保证：

- 对于文件：`clone` 命令确保原子性，即克隆后的文件始终处于正确和一致的状态。
- 对于目录：`clone` 命令对目录的原子性没有保证。换句话说，在克隆过程中，如果源目录发生变化，则目标目录与源目录可能不一致。

需要注意，在 `clone` 命令完成前，目标目录是不可见的。如果克隆未能顺利完成（比如手动中断），意味着可能存在元数据泄漏，可以通过 [`juicefs gc --delete`](../reference/command_reference.md#gc) 清理这些垃圾数据。
