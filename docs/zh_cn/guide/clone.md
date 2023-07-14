---
title: 克隆文件或目录
sidebar_position: 6
---

## `clone` 基本用法 {#basic-usage-of-clone}

JuiceFS 客户端提供了 `clone` 命令用以快速在同一挂载点下克隆目录或者文件，其原理是只拷贝元数据但不拷贝数据块，因此拷贝速度非常快。

`clone` 的命令格式如下：

```shell
juicefs clone <SRC PATH> <DST PATH>

# 克隆文件
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# 克隆目录
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# 克隆时保留文件的 UID、GID 和模式
juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2`,
```

- `<SRC PATH>`：源端的路径，可以是文件或者目录
- `<DST PATH>`：目标端的路径，可以是文件或者目录

:::tip 版本提示
该功能需要 JuiceFS v1.1 及以上版本
:::

## 保留源端的 UID、GID、mode {#preserve-source-uid-gid-mode}

`clone` 提供了 `--preserve, -p` 参数用以克隆时保留源端的 UID、GID、mode 属性。默认行为是使用当前用户的 UID 和 GID。mode 则使用当前用户的 umask 重新计算获得。

## `clone` 命令的一致性保证 {#consistency-guarantee-of-clone}

`clone` 命令提供如下的一致性保证：

- 对于文件：`clone` 命令确保原子性，即克隆后的文件始终处于正确和一致的状态。
- 对于目录：`clone` 命令对目录的原子性没有保证。换句话说，在克隆过程中，如果源目录发生变化，则目标目录与源目录可能不一致。

## `clone` 的其他注意事项 {#other-considerations-for-clone}

1. 在 `clone` 命令完成前，目标目录是不可见的
2. 假如 `clone` 命令失败产生了元数据冗余，则可以通过 `juicefs gc --delete` 命令清理
3. `clone` 的源端与目标端都必须位于同一个 JuiceFS 挂载点下
