---
title: FUSE Mount Options
sidebar_position: 5
slug: /fuse_mount_options
---

JuiceFS provides several access methods, FUSE is the common one, which is the way to mount the file system locally using the `juicefs mount` command. Users can add FUSE mount options for more granular control.

This guide describes the common FUSE mount options for JuiceFS, with two ways to add mount options:

1. Run [`juicefs mount`](../reference/command_reference.mdx#mount), and use `-o` to specify multiple options separated by commas.

   ```bash
   juicefs mount -d -o allow_other,writeback_cache sqlite3://myjfs.db ~/jfs
   ```

2. When writing `/etc/fstab` items, add FUSE options directly to the `options` field, with multiple options separated by commas.

   ```
   # <file system>       <mount point>   <type>      <options>           <dump>  <pass>
   redis://localhost:6379/1    /jfs      juicefs     _netdev,writeback_cache   0       0
   ```

## default_permissions

This option is automatically enabled when JuiceFS is mounted and does not need to be explicitly specified. It will enable the kernel's file access checks, which are performed outside the filesystem. When enabled, both the kernel checks and the file system checks must succeed before further operations.

:::tip
The kernel performs standard Unix permission checks based on mode bits, UID/GID, and directory entry ownership.
:::

## allow_other

By default FUSE only allows access to the user mounting the file system. `allow_other` option overrides this behavior to allow access for other users. When mounting JuiceFS using root, `allow_other` is automatically assumed (search for `AllowOther` in [`fuse.go`](https://github.com/juicedata/juicefs/blob/main/pkg/fuse/fuse.go)). When mounting by non-root users, you'll need to first modify `/etc/fuse.conf` and enable `user_allow_other`, and then add `allow_other` to the mount command.

## writeback_cache

:::note
This mount option requires at least version 3.15 Linux kernel
:::

FUSE supports ["writeback-cache mode"](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means the `write()` syscall can often complete rapidly. It's recommended to enable this mount option when write small data (e.g. 100 bytes) frequently.

## user_id and group_id

These two options are used to specify the owner ID and owner group ID of the mount point, but only allow to execute the mount command as root, e.g. `sudo juicefs mount -o user_id=100,group_id=100`.

## debug

This option will output Debug information from the low-level library (`go-fuse`) to `juicefs.log`.

:::note
This option will output debug information for the low-level library (`go-fuse`) to `juicefs.log`. Note that this option is different from the global `-debug` option for the JuiceFS client, where the former outputs debug information for the `go-fuse` library and the latter outputs debug information for the JuiceFS client. see the documentation [Fault Diagnosis and Analysis](../administration/fault_diagnosis_and_analysis.md).
:::
