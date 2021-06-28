# FUSE Mount Options

This is a guide that lists important FUSE mount options. These mount options are specified by `-o` option when execute [`juicefs mount`](command_reference.md#juicefs-mount) command (use comma to separate multiple options). For example:

```bash
$ juicefs mount -d -o allow_other,writeback_cache localhost ~/jfs
```

## debug

Enable debug log

## allow_other

This option overrides the security measure restricting file access to the user mounting the file system. So all users (including root) can access the files. This option is by default only allowed to root, but this restriction can be removed with `user_allow_other` configuration option in `/etc/fuse.conf`.

## writeback_cache

> **Note**: This mount option requires at least version 3.15 Linux kernel.

FUSE supports ["writeback-cache mode"](https://www.kernel.org/doc/Documentation/filesystems/fuse-io.txt), which means the `write()` syscall can often complete very fast. It's recommended enable this mount option when write very small data (e.g. 100 bytes) frequently.
