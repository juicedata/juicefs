---
sidebar_label: FUSE Mount Options
sidebar_position: 6
slug: /fuse_mount_options
---
# FUSE Mount Options

This guide lists important FUSE mount options. These mount options are specified by the option `-o`  when execute the command [`juicefs mount`](../reference/command_reference.md#juicefs-mount) (use comma to separate multiple options). For example:

```bash
juicefs mount -d -o allow_other,writeback_cache localhost ~/jfs
```

## debug

Enable debug log

## allow_other

This option overrides the default security restriction that only users amounting the file system can access to files. That is all users (including root) can access the files. Only root is allowed to use this option by default, but this restriction can be removed by the configuration option `user_allow_other` in `/etc/fuse.conf`.

## writeback_cache

:::note
This mount option requires at least version 3.15 Linux kernel
:::

FUSE supports ["writeback-cache mode"](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means the `write()` syscall can often complete rapidly. It's recommended to enable this mount option when write small data (e.g. 100 bytes) frequently.
