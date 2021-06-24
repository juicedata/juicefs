# JuiceFS 元数据导出和导入

JuiceFS 支持[多种元数据引擎](databases_for_metadata.md)；同时，JuiceFS 提供了元数据的导出和导入功能，以 [JSON](https://www.json.org/json-en.html) 文件为中介可以实现跨存储引擎的元数据迁移。相关命令的详细信息请参考[这里](command_reference.md#juicefs-dump)。以下介绍一些此功能的可能应用场景。

## 元数据检视

借助于 `juicefs dump` 命令，用户可以将指定目录导出到一个 JSON 文件，然后非常直观地查看此目录树下所有文件的详细信息，如：

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump --subdir /path/in/juicefs
```

另外，也可以使用 `jq` 等工具对导出文件进行分析。

> **注意**：为保证服务稳定，请不要在线上环境 dump 过于大的目录。

## 元数据跨引擎迁移

将元数据 dump 到一个 JSON 文件后，还可以通过 `juicefs load` 命令将其中信息导入到一个**空的**数据库中，实现离线迁移功能。例如：

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
$ juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

或者针对较小的文件系统：

```bash
$ juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

请注意，在 dump 过程中并没有限制客户端对元数据的修改，因此导出的 JSON 文件内容不保证是完全合法的。欲完整迁移元数据，需要在此过程中停止业务写入。在 load 元数据时，仅所有文件的信息会被直接读取，而一些文件系统状态和统计信息（如客户端会话，Inode 号使用等）会被重新计算，因此导入后的元数据与原先并不严格一致（用户使用上不会感受到区别）。

另外，由于迁移前后文件系统使用的对象存储是同一套，在新引擎上线前需确保旧引擎已下线，否则可能造成文件系统损坏。

## 元数据备份

通过 dump 获得的 JSON 文件还可以作为一份人类友好的简单备份，方便用户有需要时离线查看。但正如之前提到的，此文件内容无法保证正确性。欲获得完整的元数据备份，请使用各个引擎对应的带有快照功能的备份工具，如 [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) 和 [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html)。
