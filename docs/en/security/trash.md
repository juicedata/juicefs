---
sidebar_position: 2
---
# Trash

:::note
This feature requires at least JuiceFS v1.0.0, for previous versions, you need to upgrade all JuiceFS clients, and then enable trash using the `config` subcommand, introduced in below sections.
:::

JuiceFS enables the trash feature by default, files deleted will be moved in a hidden directory named `.trash` under the file system root, and kept for specified period of time before expiration. Until actual expiration, file system usage (check using `df -h`) will not change, this is also true with the corresponding object storage data.

When using `juicefs format` command to initialize JuiceFS volume, users are allowed to specify `--trash-days <val>` to set the number of days which files are kept in the `.trash` directory. Within this period, user-removed files are not actually purged, so the file system usage shown in the output of `df` command will not decrease, and the blocks in the object storage will still exist.

To control the expiration settings, use the [`--trash-days`](../reference/command_reference.mdx#format) option which is available for both `juicefs format` and `juicefs config`:

```shell
# Creating a new file system
juicefs format META-URL myjfs --trash-days=7

# Modify an existing file system
juicefs config META-URL --trash-days=7

# Set to 0 to disable Trash
juicefs config META-URL --trash-days=0
```

In addition, the automatic cleaning of the trash relies on the background job of the JuiceFS client. To ensure that the background job can be executed properly, at least one online mount point is required, and the [`--no-bgjob`](../reference/command_reference.mdx#mount-metadata-options) parameter should not be used when mounting the file system.

## Recover files {#recover}

When files are deleted, they will be moved to a directory that takes up the format of `.trash/YYYY-MM-DD-HH/[parent inode]-[file inode]-[file name]`, where `YYYY-MM-DD-HH` is the UTC time of the deletion. You can locate the deleted files and recover them if you remember when they are deleted.

If you have found the desired files in Trash, you can recover them using `mv`:

```shells
mv .trash/2022-11-30-10/[parent inode]-[file inode]-[file name] .
```

Files within the Trash directory lost all their directory structure information, and are stored in a "flatten" style, however the parent directory inode is preserved in the file name, if you have forgotten the file name, look for parent directory inode using [`juicefs info`](../reference/command_reference.mdx#info), and then track down the desired files.

Assuming the mount point being `/jfs`, and you've accidentally deleted `/jfs/data/config.json`, but you cannot directly recover this `config.json` because you've forgotten its name, use the following procedure to locate the parent directory inode, and then locate the corresponding trash files.

```shell
# Use the info subcommand to locate the parent directory inode
juicefs info /jfs/data

# Note the "inode" field in above output, assuming the inode of /jfs/data is 3
# Find all its files within the Trash directory using the find command
find /jfs/.trash -name '3-*'

# Recover all files under that directory
mv /jfs/.trash/2022-11-30-10/3-* /jfs/data
```

Keep in mind that only the root user have write access to the Trash directory, so the method introduced above is only available to the root user. If a normal user happens to have read permission to these deleted files, they can also recover them via a read-only method like `cp`, although this obviously wastes storage capacity.

If you accidentally delete a complicated structured directory, using solely `mv` to recover can be a disaster, for example:

```shell
$ tree data
data
├── app1
│   └── config
│       └── config.json
└── app2
    └── config
        └── config.json

# Delete the above complicated data directory
$ juicefs rmr data

# Files will be flattened inside the Trash directory
$ tree .trash/2023-08-14-05
.trash/2023-08-14-05
├── 1-12-data
├── 12-13-app1
├── 12-15-app2
├── 13-14-config
├── 14-17-config.json
├── 15-16-config
└── 16-18-config.json
```

To resolve such inconvenience, JuiceFS v1.1 provides the [`restore`](../reference/command_reference.mdx#restore) subcommand to quickly restore deleted files, while preserving its original directory structure. Run this procedure as follows:

```shell
# Run the restore command to reconstruct directory structure within the Trash
$ juicefs restore $META_URL 2023-08-14-05

# Preview the rebuilt directory structure, and determine the recovery scope
# You can either recover the entire directory using the below --put-back command, or just a subdir using mv
$ tree .trash/2023-08-14-05
.trash/2023-08-14-05
└── 1-12-data
    ├── app1
    │   └── config
    │       └── config.json
    └── app2
        └── config
            └── config.json

# Add --put-back to recover deleted files
juicefs restore $META_URL 2023-08-14-05 --put-back
```

## Permanently delete files {#purge}

When files in the trash directory reach their expiration time, they will be automatically cleaned up. It is important to note that the file cleaning is performed by the background job of the JuiceFS client, which is scheduled to run every hour by default. Therefore, when there are a large number of expired files, the cleaning speed of the object storage may not be as fast as expected, and it may take some time to see the change in storage capacity.

If you want to permanently delete files before their expiration time, you need to have `root` privileges and use [`juicefs rmr`](../reference/command_reference.mdx#rmr) or the system's built-in `rm` command to delete the files in the `.trash` directory, so that storage space can be immediately released.

For example, to permanently delete a directory in the trash:

```shell
juicefs rmr .trash/2022-11-30-10/
```

If you want to delete expired files more quickly, you can mount multiple mount points to exceed the deletion speed limit of a single client.

## Trash and slices {#gc}

Apart from user deleted files, there's another type of data which also resides in Trash, which isn't directly visible from the `.trash` directory, they are stale slices created by file edits and overwrites. Read more in [How JuiceFS stores files](../introduction/architecture.md#how-juicefs-store-files). To sum up, if applications constantly delete or overwrite files, object storage usage will exceed file system usage.

Although stale slices cannot be browsed or manipulated, you can use [`juicefs status`](../reference/command_reference.mdx#status) to observe its scale:

```shell
# The Trash Slices field displayed below is the number of stale slices
$ juicefs status META-URL --more
...
           Trash Files: 0                     0.0/s
           Trash Files: 0.0 b   (0 Bytes)     0.0 b/s
 Pending Deleted Files: 0                     0.0/s
 Pending Deleted Files: 0.0 b   (0 Bytes)     0.0 b/s
          Trash Slices: 27                    26322.2/s
          Trash Slices: 783.0 b (783 Bytes)   753.1 KiB/s
Pending Deleted Slices: 0                     0.0/s
Pending Deleted Slices: 0.0 b   (0 Bytes)     0.0 b/s
...
```

Stale slices are also kept according to the expiration settings, this adds another layer of data security: if files are erroneously edited or overwritten, original state can be recovered through metadata backups (provided that you have already set up metadata backup). If you do need to rollback this type of accident overwrites, you need to obtain a copy of the metadata backup, and then mount using this copy, so that you can visit the file system in its older state, and recover any files before they are tampered. See [Metadata Backup & Recovery](../administration/metadata_dump_load.md) for more.

Due to its invisibility, stale slices can grow to a very large size, if you do need to delete them, follow below procedure:

```shell
# Temporarily disable Trash
juicefs config META-URL --trash-days 0

# Optionally run compaction
juicefs gc --compact

# Purge leaked objects
juicefs gc --delete

# Do not forget to re-enable Trash upon completion
```

## Access privileges {#permission}

All users are allowed to browse the trash directory and see the full list of removed files. However, only root has write privilege to the `.trash` directory. Since JuiceFS keeps the original permission modes even for the trashed files, normal users can read files that they have permission to.

Several caveats on Trash privileges:

* When JuiceFS Client is started by a non-root user, add the `-o allow_root` option or trash cannot be emptied normally.
* The `.trash` directory can only be accessed from the file system root, thus not available for sub-directory mount points.
* User cannot create new files inside the trash directory, and only root are allowed to move or delete files in trash.
