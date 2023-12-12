---
title: Clone Files or Directories
sidebar_position: 6
---

This command makes a 1:1 clone of your data by creating a mere metadata copy, without creating any new data in the object storage, thus cloning is very fast regardless of target file / directory size. Under JuiceFS, this command is a better alternative to `cp`, moreover, for Linux clients using kernels with [`copy_file_range`](https://man7.org/linux/man-pages/man2/copy_file_range.2.html) support, then the `cp` command achieves the same result as `juicefs clone`.

![clone](../images/juicefs-clone.svg)

The clone result is a metadata copy only, where all the files are still referencing the same underlying object storage blocks, that's why a clone behaves the same in every way as its originals. When either of them go through actual file data modification, the affected data blocks will be copied on write, and become new blocks after write, while the unchanged part of the files remains the same, still referencing the original blocks.

Please note that system tools like disk-free or disk-usage (`df`, `du`) will report the space used by the cloned data, but the underlying object storage space will not grow as blocks remains the same. On the same way, as metadata is actually replicated, the clone will take the same metadata engine storage space as the original.

**Clones takes up both file system storage space, inodes and metadata engine storage space**. Pay special attention when making clones on large size directories.

```shell
juicefs clone SRC DST

# Clone a file
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2
```

## Consistency {#consistency}

In terms of transaction consistency, cloning behaves as follows:

- Before `clone` command finishes, destination file is not visible.
- For file: The `clone` command ensures atomicity, meaning that the cloned file will always be in a correct and consistent state.
- For directory: The `clone` command does not guarantee atomicity for directories. In other words, if the source directory changes during the cloning process, the target directory may be different from the source directory.
- Only one `clone` can be successfully created from the same location at the same time. The failed clone will clean up the temporarily created directory tree.

The clone is done by the mount process, it will be interrupted if `clone` command is terminated. If the clone fails or is interrupted, `mount` process will cleanup any created inodes. If the mount process fails to do that, there could be some leaking the metadata engine and object storage, because the dangling tree still hold the references to underlying data blocks. They could be cleaned up by the [`juicefs gc --delete`](../reference/command_reference.md#gc) command.
