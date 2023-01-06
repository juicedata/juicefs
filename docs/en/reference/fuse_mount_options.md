---
title: FUSE Mount Options
sidebar_position: 6
slug: /fuse_mount_options
---

JuiceFS provides several access methods, FUSE is the common one, which is the way to mount the file system locally using the `juicefs mount` command. Users can add FUSE mount options for more granular control.

This guide describes the common FUSE mount options for JuiceFS, with two ways to add mount options:

1. Manually execute [`juicefs mount`](../reference/command_reference.md#mount) command, specified by the `-o` option, with multiple options separated by commas.

   ```bash
   juicefs mount -d -o allow_other,writeback_cache sqlite3://myjfs.db ~/jfs
   ```

2. Linux distributions define automounting via `/etc/fstab` by adding options directly to the `options` field, with multiple options separated by commas.

   ```
   # <file system>       <mount point>   <type>      <options>           <dump>  <pass>
   redis://localhost:6379/1    /jfs      juicefs     _netdev,allow_other   0       0
   ```

## default_permissions

This option is automatically enabled when JuiceFS is mounted and does not need to be explicitly specified. It will enable the kernel's file access checks, which are performed outside the filesystem. When enabled, both the kernel checks and the file system checks must succeed before further operations.

:::tip
The kernel performs standard Unix permission checks based on mode bits, UID/GID, and directory entry ownership.
:::

## allow_other

By default, only the user who mounted the file system can access the files. The `allow_other` option allows other users (including the root user) to access the files as well.

By default, this option is only available to the root user, but can be unrestricted by modifying `/etc/fuse.conf` and turning on the `user_allow_other` configuration option.

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
