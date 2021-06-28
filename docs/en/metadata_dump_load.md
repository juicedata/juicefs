# JuiceFS Metadata Backup & Recovery

> **Note**: This feature requires JuiceFS >= 0.15.0.

JuiceFS supports [multiple metadata engines](databases_for_metadata.md). Each engine has its own internal format, but with the `dump` command it can export all the metadata to an identical [JSON](https://www.json.org/json-en.html) file ([example](../../pkg/meta/metadata.sample)). Meanwhile, there is also a `load` command to import metadata from the JSON file. Metadata backup, recovery and migration are achieved with these two [commands](command_reference.md#juicefs-dump).

## Metadata Backup

The `juicefs dump` command performs logical backups, producing a JSON file containing all metadata:

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
```

Basically, starting from a root directory (default to `/`), it does a depth-first walk over the tree underneath the root, writing information of each file to an output stream. Please note that `juicefs dump` can only ensure completeness of a single file, but not the whole tree because it does not support point-in-time snapshot. In other words, if there is write or delete during dumping, the output will contain files from different time points.

Metadata engines of JuiceFS usually have corresponding backup tools, such as [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) and [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html), which implement database backups. One advantage of `juicefs dump` is that the JSON format can be handled very easily, and can be loaded by different engines. In practice, you may pick one or use two backup strategies together.

> **Note**: Only metadata backup is discussed here; a complete solution to file system backup should at least include backup strategy for object storage as well, like delayed deletion, multi-version, etc.

## Metadata Recovery

When needed, metadata can be recovered from a former dumped JSON file, e.g:

```bash
$ juicefs load redis://192.168.1.6:6379 meta.dump
```

`juicefs load` will automatically resolve conflicts caused by files of different time points, and recalculate file system internal statistics (space usage, inode counter, etc.), generating globally complete and consistent metadata in the new database. Moreover, it you want to customize some metadata (BE CAREFUL), it is feasible to edit the JSON file before loading.

## Metadata Migration Between Engines

Since the JSON format can be recognized by all metadata engines, it can serve as an intermediary to migrate metadata between engines. For example:

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
$ juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

Or:

```bash
$ juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

Write and delete must be disabled during dumping to make sure the migrated file system is identical to the original one. Another thing to keep in mind is that the object storage knows nothing about the migration, so the old metadata engine should be offline or read-only before the new one go online, otherwise the file system might be broken.

## Metadata Inspection

Sometimes `juicefs dump` can be used to help debugging since the dumped JSON file is human-friendly:

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump --subdir /path/in/juicefs
```

Moreover, you can use tools like `jq` to analyze the exported file.

> **Note**: Please don't dump a too big directory in online system as it may slow down the server.
