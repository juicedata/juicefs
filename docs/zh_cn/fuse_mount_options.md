# FUSE 挂载选项

本指南列出了重要的 FUSE 挂载选项。当执行 [`juicefs mount`](command_reference.md#juicefs-mount) 命令时，这些安装选项由 `-o` 选项指定，多个选项使用半角逗号分隔。 例如：

```bash
$ juicefs mount -d -o allow_other,writeback_cache localhost ~/jfs
```

## debug

启用调试日志

## allow_other

默认只有挂载文件系统的用户才能访问文件系统中的文件，此选项可解锁该限制。设置此选项以后，所有用户，包括 root 用户都可以访问该文件系统中的文件。

默认情况下，这个选项只允许 root 用户使用，但是可以通过修改 `/etc/fuse.conf`，在该配置文件中开启 `user_allow_other` 配置选项解除限制。

## writeback_cache

> **注意**：该挂载选项仅在 Linux 3.15 及以上版本内核上支持。

FUSE 支持[「writeback-cache 模式」](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt)，这意味着 `write()` 系统调用通常可以非常快速地完成。当频繁写入非常小的数据（如 100 字节左右）时，建议启用此挂载选项。
