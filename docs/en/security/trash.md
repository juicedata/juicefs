---
sidebar_position: 2
---
# Trash

:::note
This feature requires JuiceFS v1.0.0 or higher
:::

Data security is crucial for storage system, therefore JuiceFS enables the trash feature by default: files deleted by user will be kept in a hidden directory named `.trash` under JuiceFS root, and wait for expiration.

With trash enabled, if application frequently delete or overwrite files, expect larger usage in object storage than the actual file system, because trash directory contain the following type of files:

1. Files deleted by user, they can be directly viewed and manipulated in the `.trash` directory
2. Data blocks created during file overwrites (see [FAQ](../faq.md#what-is-the-implementation-principle-of-juicefs-supporting-random-write)) are kept in trash as well, but users won't be able to see these files, thus cannot be force deleted by default, see [Recovery/Purge](#recover-purge)

## Configure {#configure}

When using `juicefs format` command to initialize JuiceFS volume, users are allowed to specify `--trash-days <val>` to set the number of days which files are kept in the `.trash` directory. Within this period, user-removed files are not actually purged, so the file system usage shown in the output of `df` command will not decrease, and the blocks in the object storage will still exist.

- the value of `trash-days` defaults to 1, which means files in trash will be automatically purged after ONE day.
- use `--trash-days 0` to disable this feature; the trash will be emptied in a short time, and all files removed afterwards will be purged immediately.
- For older versions of JuiceFS to use the trash, you need to manually set `--trash-days` to the desired positive integer value via the `config` command after upgrading all mount points.

For volumes already been initialized, you can still update `trash-days` with the `config` command, e.g:

```bash
juicefs config META-URL --trash-days 7
```

Verify using the `status` command:

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

The `.trash` directory resides under the root of the JuiceFS mount point, use it like this for example:

```shell
cd /jfs

# Purge files from trash directory
find .trash -name '*.tmp' | xargs rm -f

# Recover files from trash directory
# Note: original directory structure is lost, however inode info will be prefixed in the file name, continue reading for more
mv .trash/[parent inode]-[file inode]-[file name] .
```

When mounting a subdirectory, you will not be able to enter the trash directory.

### Tree Structure

There are only two levels under the tree rooted by `.trash`. The first one is a list of directories that are automatically created by JuiceFS and named as `year-month-day-hour` (e.g. `2021-11-30-10`). All files removed within an hour will be moved into the corresponding directory. The second level is just a plain list of removed files and empty directories (the usual `rm -rf <dir>` command removes files in `<dir>` first, and then removes the empty `<dir>` itself). **The original directory structure is lost when files are moved into the trash.** To save as much information about the original hierarchy as possible without impact on the performance, JuiceFS renames files in trash to `{parentInode-fileInode-fileName}`. Here `inode` is an internal number used for organizing file system (use [`juicefs info`](../reference/command_reference.md#info) to check file inode), and can be ignored if you only care about the name of the original file.

:::note
The first level directory is named after the UTC time.
:::

### Privileges

All users are allowed to browse the trash directory and see the full list of removed files. However, since JuiceFS keeps the original modes of the trashed files, normal users can only read files that they have permission to. The `.trash` directory is hidden if JuiceFS is mounted with `--subdir <dir>`.

User cannot create new files inside the trash directory, and only root are allowed to move or delete files in trash.

### Recover/Purge {#recover-purge}

Recover/Purge files in trash are only available for root users, simply use `mv` command to recover a file, or use `rm` to permanently delete a file. Normal users, however, can only recover a file by reading its content and write it to a new file.

JuiceFS Client is in charge of periodically checking trash and expire old entries (run every hour by default), so you need at least one active client mounted (without [`--no-bgjob`](../reference/command_reference.md#mount)). If you wish to quickly free up object storage, you can manually delete files in the `.trash` directory using the `rm` command.

Furthermore, garbage blocks created by file overwrites are not visible to users, if you must force delete them, you'll have to temporarily disable trash (setting [`--trash-days 0`](#configure)), and then manually run garbage collection using [`juicefs gc`](../reference/command_reference.md#gc). Remember to re-enable trash after done.
