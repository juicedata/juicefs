---
title: FUSE 挂载选项
sidebar_position: 5
slug: /fuse_mount_options
---

JuiceFS 文件系统为用户提供多种访问方式，FUSE 是其中较为常用的一种，即使用 `juicefs mount` 命令将文件系统挂载到本地的方式。用户可以根据需要添加 FUSE 支持的挂载选项，从而实现更细粒度的控制。

本指南介绍 JuiceFS 常用的 FUSE 挂载选项，有两种添加挂载选项的方式：

1. 手动执行 [`juicefs mount`](../reference/command_reference.mdx#mount) 命令时，通过 `-o` 选项指定，多个选项使用半角逗号分隔。

   ```bash
   juicefs mount -d -o allow_other,writeback_cache sqlite3://myjfs.db ~/jfs
   ```

2. Linux 发行版通过 `/etc/fstab` 定义自动挂载时，在 `options` 字段处直接添加选项，多个选项使用半角逗号分隔。

   ```
   # <file system>       <mount point>   <type>      <options>           <dump>  <pass>
   redis://localhost:6379/1    /jfs      juicefs     _netdev,writeback_cache   0       0
   ```

## default_permissions

JuiceFS 在挂载时会自动启用该选项，无需显式指定。该选项将启用内核的文件访问权限检查，它会在文件系统之外进行，启用后，内核检查和文件系统检查必须全部成功才允许进一步操作，该选项通常与 `allow_other` 一起使用。

:::tip
内核执行的是标准的 Unix 权限检查，基于 mode bits、UID/GID、目录所有权。
:::

## allow_other

FUSE 默认只有挂载文件系统的用户可以访问挂载点中的文件，`allow_other` 选项可以让其他用户也可以访问挂载点上的文件。当 root 用户挂载时，该选项会自动启用（在 [`fuse.go`](https://github.com/juicedata/juicefs/blob/main/pkg/fuse/fuse.go) 搜索 `AllowOther` 字样），无需显式指定。而如果是普通用户挂载，则需要修改 `/etc/fuse.conf`，在该配置文件中开启 `user_allow_other` 配置选项，才能在普通用户挂载时启用 `allow_other`。

## writeback_cache

:::note 注意
该挂载选项仅在 Linux 3.15 及以上版本内核上支持
:::

FUSE 支持[「writeback-cache 模式」](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt)，这意味着 `write()` 系统调用通常可以非常快速地完成。当频繁写入非常小的数据（如 100 字节左右）时，建议启用此挂载选项。

## user_id 和 group_id

这两个选项用来指定挂载点的所有者 ID 和所有者组 ID，但仅允许以 root 身份指定，例如 `sudo juicefs mount -o user_id=100,group_id=100`。

## debug

该选项会将低层类库（`go-fuse`）的 Debug 信息输出到 `juicefs.log` 中。

:::note 注意
该选项会将低层类库（`go-fuse`）的 Debug 信息输出到 `juicefs.log` 中，需要注意的是，该选项与 JuiceFS 客户端的全局 `--debug` 选项不同，前者是输出 `go-fuse` 类库的调试信息，后者是输出 JuiceFS 客户端的调试信息。详情参考文档[故障诊断和分析](../administration/fault_diagnosis_and_analysis.md)。
:::
