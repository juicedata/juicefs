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

In terms of transaction consistency, cloning behaves as follows:

- Before `clone` command finishes, destination file is not visible.
- For file: The `clone` command ensures atomicity, meaning that the cloned file will always be in a correct and consistent state.
- For directory: The `clone` command does not guarantee atomicity for directories. In other words, if the source directory changes during the cloning process, the target directory may be different from the source directory.

Considering the atomicity of cloning a single file, when multiple process tries to clone into a same target, only one will eventually succeed. To be specific, during the concurrent cloning, there'll be N unfinished file trees (with different tree root inode) where N is the number of concurrently running `clone` commands. When any of the clone process reaches the ending step, it'll check for the existence of the target edge, and either create the edge or fails if it already exists. Therefore, there can be only one successful clone when there're conflicts.

It's also mentioned above that a clone command could fail, and failures can cause metadata leak, you can clean up using the [`juicefs gc --delete`](../reference/command_reference.md#gc) command.

To discuss the anatomy of a metadata leak, first understand that if a clone runs into errors, the program will try to perform cleanup, which could also run into abnormity, depending on the cleanup result, the possibilities are:

1. Cloning itself fails, but cleanup succeeds, in this case, the unfinished metadata tree is cleaned up before becoming a leak, there'll be no side effects.
1. Cloning fails, along with the subsequent cleanup, this leaks the unfinished metadata tree and can cause object storage leak as well. Meaning that if its corresponding files are deleted, the object storage blocks will not be released (since it's still being referenced in the unfinished clone results). The leak will persist until it's cleaned by `juicefs gc --delete`. If a clone process is aborted halfway, this is probably the case because it never reaches the cleanup stage.
