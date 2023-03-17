---
title: Metadata Backup & Recovery
sidebar_position: 2
slug: /metadata_dump_load
---

:::tip

- JuiceFS v0.15.2 started to support manual backup, recovery and inter-engine migration of metadata.
- JuiceFS v1.0.0 starts to support automatic metadata backup
:::

## Metadata backup {#backup}

JuiceFS supports [multiple metadata storage engines](../guide/how_to_set_up_metadata_engine.md), and each engine has a different data management format internally. To facilitate management, JuiceFS provides `dump` command which allows to write all metadata in a uniform format to [JSON](https://www.json.org/json-en.html) file for backup. Also, JuiceFS provides `load` command which allows to restore or migrate backups to any metadata storage engine. For more information on the command, please refer to [here](../reference/command_reference.md#dump).

### Manual backup {#backup-manually}

Using the `dump` command provided by JuiceFS client, you can export metadata to a JSON file, for example:

```bash
juicefs dump redis://192.168.1.6:6379 meta-dump.json
```

The JSON file exported by using the `dump` command provided by the JuiceFS client can have any filename and extension that you prefer, as shown in the example above. In particular, if the file extension is `.gz` (e.g. `meta-dump.json.gz`), the exported data will be compressed using the Gzip algorithm.

By default, the `dump` command starts from the root directory `/` and iterates deeply through all the files in the directory tree, and writes the metadata information of each file to a file in JSON format. However, for security reasons, the Secret Key information of the object storage will not be exported to the JSON file (it can be preserved through the `--keep-secret-key` option).

:::note
`juicefs dump` only guarantees the integrity of individual files and does not provide a global point-in-time snapshot. If transactions are being written during the dump process, the final exported files will contain information from different points in time.
:::

The value of `juicefs dump` is that it can export complete metadata information in a uniform JSON format for easy management and preservation, and it can be recognized and imported by different metadata storage engines.

In practice, the `dump` command should be used in conjunction with the backup tool that comes with the database to complement each other, such as [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) and [`mysqldump`](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html), etc.

:::note
The above discussion is for metadata backup only. A complete file system backup solution should also include object storage data backup at least, such as offsite disaster recovery, recycle bin, multiple versions, etc.
:::

### Automatic backup {#backup-automatically}

Starting with JuiceFS v1.0.0, the client automatically backs up metadata and copies it to the object storage every hour, regardless of whether the file system is mounted via the `mount` command or accessed via the JuiceFS S3 gateway and Hadoop Java SDK.

The backup files are stored in the `meta` directory of the object storage. It is a separate directory from the data store and not visible in the mount point and does not interact with the data store, and the directory can be viewed and managed using the file browser of the object storage.

![](../images/meta-auto-backup-list.png)

By default, the JuiceFS client backs up metadata once an hour. The frequency of automatic backups can be adjusted by the `--backup-meta` option when mounting the filesystem, for example, to set the auto-backup to be performed every 8 hours.

```shell
juicefs mount -d --backup-meta 8h redis://127.0.0.1:6379/1 /mnt
```

The backup frequency can be accurate to the second and it supports the following units.

- `h`: accurate to the hour, e.g. `1h`.
- `m`: accurate to the minute, e.g. `30m`, `1h30m`.
- `s`: accurate to the second, such as `50s`, `30m50s`, `1h30m50s`;

It is worth mentioning that the time cost of backup will increase with the number of files in the filesystem. Hence, when the number is too large (by default 1 million) with the automatic backup frequency 1 hour (by default), JuiceFS will automatically skip backup and print the corresponding warning log. At this point you may mount a new client with a bigger `--backup-meta` option value to re-enable automatic backups.

For reference, when using Redis as the metadata engine, backing up the metadata for one million files takes about 1 minute and consumes about 1GB of memory.

#### Automatic backup policy

Although automatic metadata backup becomes a default action for clients, backup conflicts do not occur when multiple hosts share the same file system mount.

JuiceFS maintains a global timestamp to ensure that only one client performs the backup operation at the same time. When different backup periods are set between clients, then it will back up based on the shortest period setting.

#### Backup cleanup policy

JuiceFS periodically cleans up backups according to the following rules.

- Keep all backups up to 2 days.
- For backups older than 2 days and less than 2 weeks, keep 1 backup for each day.
- For backups older than 2 weeks and less than 2 months, keep 1 backup for each week.
- For backups older than 2 months, keep 1 backup for each month.

## Metadata recovery and migration {#recovery-and-migration}

Use the [`load`](../reference/command_reference.md#load) command to restore the metadata exported by the `dump` command to the database, please note that the `load` command only supports restoring to **newly created or empty database**. For example, to restore a backup to a brand new Redis database:

:::tip
If the metadata backup file is compressed by Gzip (that is, the file extension is `.gz`), you need to use the `gzip -d` command to decompress it first.
:::

```bash
juicefs load redis://192.168.1.6:6379 meta-dump.json
```

This command automatically handles conflicts due to the inclusion of files from different points in time, recalculates the file system statistics (space usage, inode counters, etc.), and finally generates a globally consistent metadata in the database. Alternatively, if you want to customize some of the metadata (be careful), you can try to manually modify the JSON file before loading.

The JSON format data exported by the `dump` command is unified and generic, and can be recognized and imported by all metadata engines. Therefore, you can not only restore backups to the same type of database, but also to other databases, thus achieving the migration of metadata engines.

For instance, you can export the metadata backup from a Redis database and restore it to a brand new MySQL database.

1. Exporting metadata backup from Redis:

   ```bash
   juicefs dump redis://192.168.1.6:6379 meta-dump.json
   ```

1. Restoring metadata to a new MySQL database:

   ```bash
   juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta-dump.json
   ```

It is also possible to migrate directly through the system's pipe:

```bash
juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

It is important to note that since the API access key for object storage is excluded by default from the backup exported by `dump`, whether restoring or migrating metadata, you need to use the [`juicefs config`](../reference/command_reference.md#config) command to add the Secret Key associated with the file system back to the object storage after completing the operation. For example:

```bash
juicefs config --secret-key xxxxx mysql://user:password@(192.168.1.6:3306)/juicefs
```

:::caution
To ensure consistency of the file system content before and after migration, you need to stop transaction writing during the migration process. Also, since the original object storage is still used after migration, make sure that the old engine is offline or has read-only access to the object storage before the new metadata engine comes online; otherwise it may cause file system corruption.
:::

## Metadata inspection {#inspection}

In addition to exporting complete metadata information, the `dump` command also supports exporting metadata in specific subdirectories. The exported JSON content is often used to help troubleshoot problems because it allows users to view the internal information of all the files under a given directory tree intuitively. For example.

```bash
juicefs dump redis://192.168.1.6:6379 meta-dump.json --subdir /path/in/juicefs
```

Moreover, you can use tools like `jq` to analyze the exported file.

:::note
Please don't dump a directory that is too big in an online system as it may slow down the server.
:::
