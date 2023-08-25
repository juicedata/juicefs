---
title: Clone Files or Directories
sidebar_position: 6
---

This command makes a clone of your data by creating a mere metadata copy, without creating any new data in the object storage, thus cloning is very fast regardless of target file / directory size. Under JuiceFS, this command is a better alternative to `cp`, moreover, for Linux clients using kernels with [`copy_file_range`](https://man7.org/linux/man-pages/man2/copy_file_range.2.html) support, then the `cp` command achieves the same result as `juicefs clone`.

![clone](../images/juicefs-clone.svg)

The clone result is a metadata copy, all the files are still referencing the same object storage blocks, that's why a clone behaves the same in every way as its originals. When either of them go through actual file data modification, the affected data blocks will be copied on write, and become new blocks after write, while the unchanged part of the files remains the same, still referencing the original blocks.

**Clones takes up both file system storage space and metadata engine storage space**, pay special attention when making clones on large size directories.

```shell
juicefs clone SRC DST

# Clone a file
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2
```

## Consistency {#consistency}

The `clone` subcommand provides consistency guarantees as follows:

- For file: The `clone` command ensures atomicity, meaning that the cloned file will always be in a correct and consistent state.
- For directory: The `clone` command does not guarantee atomicity for directories. In other words, if the source directory changes during the cloning process, the target directory may be different from the source directory.

However, the destination directory is not visible until the `clone` command is completed, if the command didn't manage to finish properly, there could be metadata leak and you should clean up using the [`juicefs gc --delete`](../reference/command_reference.md#gc) command.
