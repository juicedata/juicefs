---
title: 克隆文件或目录
sidebar_position: 8
---

## clone 基本用法

JuiceFS 客户端提供了 `clone` 命令用以快速在单个 JuiceFS 文件系统内部克隆目录或者文件，其原理是只拷贝元数据但不拷贝数据块，因此拷贝速度非常快。

clone 的命令格式如下：

```shell
$ juicefs clone <SRC PATH> <DST PATH>

# Clone a file
$ juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
$ juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# Clone with preserving the UID, GID, and mode of the file
$ juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2`,
```

- `<SRC PATH>`：源端的路径，可以是文件或者目录
- `<DST PATH>`：目标端的路径，可以是文件或者目录

### 保留源端的 UID, GID, mode

clone 提供了 `--preserve, -p` 参数用以克隆时保留源端的 UID, GID, mode 属性。默认行为是使用当前用户的 UID, GID。mode 则使用当前用户的 umask 重新计算获得。

### clone 子命令的一致性保证

`clone` 子命令提供如下的一致性保证：

- 对于单个文件：`clone` 命令确保原子性，即克隆后的文件始终处于正确和一致的状态。

- 对于目录：`clone` 命令对目录的原子性没有保证。换句话说，在克隆过程中，如果源目录发生变化，则目标目录可能处于不一致的状态。

### clone 的其他注意事项

1. clone 的源端与目标端都必须位于同一个 JuiceFS 文件系统挂载点下
2. 在 clone 命令完成前，目标目录是不可见的
3. 假如 clone 命令失败产生了元数据冗余，则可以通过 `juicefs gc --delete` 命令清理
