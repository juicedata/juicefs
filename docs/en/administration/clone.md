---
title: Clone Files or Directories
sidebar_position: 8
---

## Basic Usage of `clone`

The JuiceFS client provides the `clone` command to quickly clone directories or files within a single JuiceFS mount point. The cloning process involves copying only the metadata without copying the data blocks, making it extremely fast.

The command format for `clone` is as follows:

```shell
$ juicefs clone <SRC PATH> <DST PATH>

# Clone a file
$ juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
$ juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# Clone with preserving the UID, GID, and mode of the file
$ juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2`,
```

- `<SRC PATH>`: The source path, which can be a file or directory.
- `<DST PATH>`: The destination path, which can be a file or directory.

### Preserve Source's UID, GID, and mode

The `--preserve, -p` option is provided to preserve the UID, GID, and mode attributes of the source during cloning. By default, the current user's UID and GID are used. The mode is recalculated based on the current user's umask.

### Consistency Guarantee of the `clone`

The `clone` subcommand provides consistency guarantees as follows:

- For individual files: The `clone` command ensures atomicity, meaning that the cloned file will always be in a correct and consistent state.

- For directories: The `clone` command does not guarantee atomicity for directories. In other words, If the source directory changes during the cloning process, the target directory may be different from the source directory.

### Other Considerations for `clone`

1. The destination directory is not visible until the `clone` command is completed.
2. If metadata redundancy occurs due to a failed `clone` command, it can be cleaned up using the `juicefs gc --delete` command.
3. Both the source and destination of the `clone` command must be located under the same JuiceFS mount point.
