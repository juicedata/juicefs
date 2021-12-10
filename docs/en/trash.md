## Trash

> **Note**: This feature requires JuiceFS v1.0.0 or higher

Data safety is always critical for a storage system. JuiceFS enables **trash** feature by default to keep user-removed files for a certain period in a hidden directory named `.trash`.

### Configure

When using `juicefs format` command to initialize JuiceFS volume, users may specify `--trash-days <val>` to configure the number of days during which files are kept in the `.trash` directory. Within this period, user-removed files are not actually purged, so you won't see decreased number in `df` output, and can still find blocks in the object storage.

- the default value of `trash-days` is 1, which means files in trash will be automatically purged after ONE day.
- use `--trash-days 0` to disable this feature; the trash will be emptied in a short time, and all files removed afterwards will be purged immediately.
- trash is disabled for older versions. If you want to enable it for an existed volume, please upgrade **ALL** clients first and then change `--trash-days` to a positive value manually.

After initializing a volume, you can still update `trash-days` with the `format` command, and please note that you have to make sure **all other arguments are included** as well, e.g:

```bash
$ juicefs format --trash-days 7 ... META-URL NAME
```

Then you can check new configurations through `status` command:

```bash
$ juicefs status META-URL
{
  "Setting": {
    ...
    "TrashDays": 7
  }
}
```

### Usage

The `.trash` directory is automatically created under root `/`.

#### Tree Structure

There are only two levels under the tree rooted by `.trash`. The first one is a list of directories that are automatically created by JuiceFS and named as `year-month-day-hour` (e.g. `2021-11-30-10`). All files removed in a certain hour will be moved into the corresponding directory. The second level is just a plain list of removed files and empty directories (usually `rm -rf <dir>` will remove files in `dir` first, and then remove the empty `dir`). Obviously, the original tree structure is lost when files are moved into the trash. To save as much information as possible of the original hierarchy without impact on the performance, JuiceFS renames files in trash to `{parentInode-fileInode-fileName}`. Here `inode` is an internal number used for file system organizing, and can be ignored if you only care about name of the original file.

> **Note**: UTC is used when naming directories in the first level.

> **Tips**: You can use `juicefs info` to check inode of a file or directory.

#### Access

All users are allowed to browse the trash and see the full list of removed files. However, files in trash keep their original modes, so users can only read files that they can read before. If JuiceFS is mounted with `--subdir <dir>` (mostly used as a CSI driver on Kubernetes), the whole trash will be hidden and can't be accessed.

It is not permitted to create files within the trash. Deleting or purging a file are forbidden as well for non-root users, even if he/she is owner of this file.

#### Recover／Purge

It is suggested to ask root user to recover files, since root is allowed to move them out of trash with a single `mv` command, and causes no data copy. Other users, however, can only recover a file by reading its content and write it to another new file.

JuiceFS client will check the trash every hour and purge old entries. At lease one active client is required to make it happen. Like recovering, only root user is allowed to purge entries manually.

