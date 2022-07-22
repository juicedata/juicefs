---
sidebar_label: Metadata Backup & Recovery
sidebar_position: 2
slug: /metadata_dump_load
---
# Metadata Backup & Recovery

:::tip
- JuiceFS v0.15.2 started to support manual backup, recovery and inter-engine migration of metadata.
- JuiceFS v1.0.0 starts to support automatic metadata backup
:::

## Manual Backup

JuiceFS supports [multiple metadata storage engines](../reference/how_to_setup_metadata_engine.md), and each engine has a different data management format internally. To facilitate management, JuiceFS provides `dump` command to allow writing all metadata in a uniform format to [JSON](https://www.json.org/json-en.html) file for backup. Also, JuiceFS provides `load` command to allow restoring or migrating backups to any metadata storage engine. For more information on the command, please refer to [here](../reference/command_reference.md#juicefs-dump).

### Metadata Backup

Metadata can be exported to a file using the `dump` command provided by the JuiceFS client, e.g.

```bash
juicefs dump redis://192.168.1.6:6379 meta.dump
```

By default, this command starts from the root directory `/` and iterates deeply through all the files in the directory tree, writing the metadata information of each file to the file in JSON format.

:::note
`juicefs dump` only guarantees the integrity of individual files themselves and does not provide a global point-in-time snapshot. If the business is still writing during the dump process, the final result will contain information from different points in time.
:::

Redis, MySQL and other databases have their own backup tools, such as [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) and [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html), etc. Use them as JuiceFS metadata storage, you still need to backup metadata regularly with each database's own backup tool.

The value of `juicefs dump` is that it can export complete metadata information in a uniform JSON format for easy management and preservation, and it can be recognized and imported by different metadata storage engines. In practice, the `dump` command should be used in conjunction with the backup tool that comes with the database to complement each other.

:::note
The above discussion is for metadata backup only. A complete file system backup solution should also include at least object storage data backup, such as offsite disaster recovery, recycle bin, multiple versions, etc.
:::

### Metadata Recovery

:::tip
JSON backups can only be restored to a `newly created database` or an `empty database`.
:::

Metadata from a backed up JSON file can be imported into a new **empty database** using the `load` command provided by the JuiceFS client, e.g.

```bash
juicefs load redis://192.168.1.6:6379 meta.dump
```

This command automatically handles conflicts due to the inclusion of files from different points in time, recalculates the file system statistics (space usage, inode counters, etc.), and finally generates a globally consistent metadata in the database. Alternatively, if you want to customize some of the metadata (be careful), you can try to manually modify the JSON file before loading.

### Metadata Migration Between Engines

:::tip
The metadata migration operation requires the target database to be `newly created database` or `empty database`.
:::

Thanks to the commonality of the JSON format, which is recognized by all metadata storage engines supported by JuiceFS, it is possible to export metadata information from one engine as a JSON backup and then import it to another engine, thus enabling the migration of metadata between different types of engines. Example.

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
$ juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

It is also possible to migrate directly through the system's Pipe.

```bash
$ juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

:::caution
To ensure consistent file system content before and after migration, you need to stop business writes during the migration process. Also, since the original object storage is still used after migration, make sure the old engine is offline or has read-only access to the object storage only before the new metadata engine comes online, otherwise it may cause file system corruption.
:::

### Metadata Inspection

In addition to exporting complete metadata information, the `dump` command also supports exporting metadata in specific subdirectories. The exported JSON content is often used to help troubleshoot problems because it gives the user a very visual view of the internal information of all the files under a given directory tree. For example.

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump --subdir /path/in/juicefs
```

Moreover, you can use tools like `jq` to analyze the exported file.

:::note
Please don't dump a too big directory in online system as it may slow down the server.
:::

## Automatic Backup

Starting with JuiceFS v1.0.0, the client automatically backups metadata and copies it to the object storage every hour, regardless of whether the file system is mounted via the `mount` command or accessed via the JuiceFS S3 gateway and Hadoop Java SDK.

The backup files are stored in the `meta` directory of the object storage, which is a separate directory from the Data Store and is not visible in the mount point and does not interact with the Data Store, and can be viewed and managed using the File Browser of the object storage.

![](../images/meta-auto-backup-list.png)

By default, the JuiceFS client backs up metadata once an hour. The frequency of automatic backups can be adjusted with the `--backup-meta` option when mounting the filesystem, for example, to set the auto-backup to be performed every 8 hours.

```shell
sudo juicefs mount -d --backup-meta 8h redis://127.0.0.1:6379/1 /mnt
```

The backup frequency can be accurate to the second and the units supported are as follows.

- `h`: accurate to the hour, e.g. `1h`.
- `m`: accurate to the minute, e.g. `30m`, `1h30m`.
- `s`: accurate to the second, such as `50s`, `30m50s`, `1h30m50s`;

It is worth mentioning that the time cost of backup will increase with the number of files in the filesystem, so when the number is too large (by default 1 million) and the automatic backup frequency is the default value of 1 hour, JuiceFS will automatically skip backup and print the corresponding warning log. At this point you may mount a new client with bigger `--backup-meta` option to re-enable automatic backups.

For reference, when using Redis as the metadata engine, backing up the metadata for one million files takes about 1 minute and consumes about 1GB of memory.

### Automatic Backup Policy

Although automatic metadata backup becomes the default action for clients, backup conflicts do not occur when multiple hosts share the same filesystem mount.

JuiceFS maintains a global timestamp to ensure that only one client performs the backup operation at the same time. When different backup periods are set between clients, then the backup is performed with the shortest period setting.

### Backup Cleanup Policy

JuiceFS periodically cleans up backups according to the following rules.

- Keep all backups up to 2 days.
- For more than 2 days and less than 2 weeks, keep 1 backup per day.
- For more than 2 weeks and less than 2 months, keep 1 backup per week.
- For more than 2 months, keep 1 backup for each month.
