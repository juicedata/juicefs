---
sidebar_position: 2
---
# Trash

:::note
This feature requires JuiceFS v1.0.0 or higher
:::

For storage system, data safety is always one of the key elements to concern. Therefore, JuiceFS enables **trash** feature by default, which will automatically move the user-removed files into a hidden directory named `.trash`, and keep the data for a period of time before cleaning.

## Configure

When using `juicefs format` command to initialize JuiceFS volume, users are allowed to specify `--trash-days <val>` to set the number of days which files are kept in the `.trash` directory. Within this period, user-removed files are not actually purged, so the file system usage shown in the output of `df` command will not decrease, and the blocks in the object storage will still exist.

- the value of `trash-days` defaults to 1, which means files in trash will be automatically purged after ONE day.
- use `--trash-days 0` to disable this feature; the trash will be emptied in a short time, and all files removed afterwards will be purged immediately.
- - For older versions of JuiceFS to use the trash, you need to manually set `--trash-days` to the desired positive integer value via the `config` command after upgrading all mount points.

For volumes already been initialized, you can still update `trash-days` with the `config` command, e.g:

```bash
juicefs config META-URL --trash-days 7
```

Then you can check new configurations by `status` command:

```bash
juicefs status META-URL

{
  "Setting": {
    ...
    "TrashDays": 7
  }
}
```

## Usage

The `.trash` directory is automatically created under the root of the JuiceFS mount point.

### Tree Structure

There are only two levels under the tree rooted by `.trash`. The first one is a list of directories that are automatically created by JuiceFS and named as `year-month-day-hour` (e.g. `2021-11-30-10`). All files removed within an hour will be moved into the corresponding directory. The second level is just a plain list of removed files and empty directories (the usual `rm -rf <dir>` command removes files in `<dir>` first, and then removes the empty `<dir>` itself). Obviously, the original tree structure is lost when files are moved into the trash. To save as much information about the original hierarchy as possible without impact on the performance, JuiceFS renames files in trash to `{parentInode-fileInode-fileName}`. Here `inode` is an internal number used for organizing file system, and can be ignored if you only care about the name of the original file.

:::note
The first level directory is named after the UTC time.
:::

:::tip
You can use `juicefs info` to check inode of a file or a directory.
:::

### Privileges

All users are allowed to browse the trash and see the full list of removed files. However, since JuiceFS keeps the original modes of the trashed files, users can only read files that they can read before. If JuiceFS is mounted with `--subdir <dir>` (mostly used as a CSI driver on Kubernetes), the whole trash will be hidden and can't be accessed.

It is not permitted to create files inside the trash. Deleting or purging a file are forbidden as well for non-root users, even if he/she is the owner of this file.

### Recover/Purge

It is suggested to recover files as root, since root is allowed to move them out of trash with a single `mv` command without any extra data copy. Other users, however, can only recover a file by reading its content and write it to another new file.

Since it is JuiceFS client which is in charge of checking the trash every hour and purging old entries, you need at least ONE active client. Like recovering, only the root user is allowed to manually purge entries by the `rm` command.

## Cautions

With the trash enabled, if the application needs to frequently delete or overwrite files, the usage of object storage will be much larger than that of the file system. There are two main reasons for this:

1. Deleted files remain in the trash
2. Data blocks that need to be garbage collected during frequent overwrites are kept in the trash

The first part can be cleaned up manually by the root user, while the second part is not directly visible to users, and by default cannot be force deleted. If you do want to actively clean them up, you need to disable the trash (setting `--trash-days 0`) and then mark these blocks as leaked and delete them with the `juicefs gc` command. **Please don't forget to reopen the trash after cleaning the data fragments.**

:::tip
For the specific reasons for generating these data fragments, please refer to the [FAQ](../faq.md#what-is-the-implementation-principle-of-juicefs-supporting-random-write) document.
:::
