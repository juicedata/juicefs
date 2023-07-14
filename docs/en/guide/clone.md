---
title: Clone Files or Directories
sidebar_position: 6
---

## Basic usage of `clone` {#basic-usage-of-clone}

The JuiceFS client provides the `clone` command to quickly clone directories or files within a single JuiceFS mount point. The cloning process involves copying only the metadata without copying the data blocks, making it extremely fast.

The command format for `clone` is as follows:

```shell
juicefs clone <SRC PATH> <DST PATH>

# Clone a file
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# Clone with preserving the UID, GID, and mode of the file
juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2`,
```

- `<SRC PATH>`: The source path, which can be a file or directory.
- `<DST PATH>`: The destination path, which can be a file or directory.

:::tip
This feature requires JuiceFS v1.1 or later
:::

## Preserve source's UID, GID, and mode {#preserve-source-uid-gid-mode}

The `--preserve, -p` option is provided to preserve the UID, GID, and mode attributes of the source during cloning. By default, the current user's UID and GID are used. The mode is recalculated based on the current user's umask.

## Consistency guarantee of the `clone` {#consistency-guarantee-of-clone}

The `clone` subcommand provides consistency guarantees as follows:

- For file: The `clone` command ensures atomicity, meaning that the cloned file will always be in a correct and consistent state.
- For directory: The `clone` command does not guarantee atomicity for directories. In other words, if the source directory changes during the cloning process, the target directory may be different from the source directory.

## Other considerations for `clone` {#other-considerations-for-clone}

1. The destination directory is not visible until the `clone` command is completed.
2. If metadata redundancy occurs due to a failed `clone` command, it can be cleaned up using the `juicefs gc --delete` command.
3. Both the source and destination of the `clone` command must be located under the same JuiceFS mount point.
