# JuiceFS Metadata Dump & Load

JuiceFS supports [multiple metadata engines](databases_for_metadata.md); meanwhile, JuiceFS provides `dump` and `load` commands to migrate metadata between different engines with the help of an intermediary [JSON](https://www.json.org/json-en.html) file. Details of the commands can be found [here](command_reference.md#juicefs-dump). Followings are a few of examples that might be helpful.

## Metadata Inspection

With the `juicefs dump` command, you can export a directory to a JSON file, and directly inspect information of all files underneath this directory. For example:

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump --subdir /path/in/juicefs
```

Moreover, you can use tools like `jq` to analyze the exported file.

> **Note**: Please don't dump a too big directory in online system as it may slow the server.

## Metadata Migration Between Engines

After dumping metadata to a JSON file, you can load the information into an **empty** database by `juicefs load` command, i.e. migrate metadata between engines. For example:

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
$ juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

For small file systems you can also run:

```bash
$ juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

Please note the modification is still allowed during dumping, so contents in the exported JSON file is not guaranteed to be right. If you want a full valid migration, disable all writes and deletes. When loading metadata, information of files will be used directly, but other stats (e.g. client sessions, inode counter) will be recalculated. Thus, the loaded metadata is not exactly the same as the origin, though users will not feel it.

Another thing to keep in mind is that the object storage knows nothing about the migration, so the old metadata engine should be offline before the new one go online, otherwise the file system might be broken.

## Metadata Backup

The dumped JSON file can also serve as a human-friendly backup, which can be inspected offline whenever needed. But as mentioned before, the content is not guaranteed to be right. If you want a full valid backup, please use corresponding tools with snapshot, such as [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) and [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html).

