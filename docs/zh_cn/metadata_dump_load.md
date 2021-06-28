# JuiceFS 元数据备份和恢复

> **注意**：此特性需要使用 0.15.0 及以上版本的 JuiceFS。

JuiceFS 支持[多种元数据引擎](databases_for_metadata.md)。各个引擎内部管理格式各有不同，但都可以通过 JuiceFS 提供的 `dump` 命令将所有元数据写入到一个统一格式的 [JSON](https://www.json.org/json-en.html) 文件（[示例](../../pkg/meta/metadata.sample)）中；同时，JuiceFS 也提供了从此文件中 `load` 元数据的命令。通过这两个命令，可以实现元数据备份/恢复和跨引擎迁移的功能。命令的详细信息请参考[这里](command_reference.md#juicefs-dump)。

## 元数据备份

 `juicefs dump` 命令会生成一份元数据的逻辑备份，如：

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
```

其基本原理是从指定目录（默认为根目录 `/`）开始，深度优先遍历此目录树下所有文件，将每个文件的相关信息按 JSON 格式写入到输出流中。值得注意的是，`juicefs dump` 仅保证单个文件自身的完整性，但不提供全局时间点快照的功能，因此如果在 dump 过程中业务仍在写入，最终结果会包含不同时间点的文件。

JuiceFS 的引擎数据库一般有其对应的备份工具，如 [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) 和 [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html) 等，可以实现数据库层面的备份。使用 `juicefs dump` 的一大优势在于其导出的 JSON 格式可以非常方便地处理，而且不同的元数据引擎都可以识别并导入。在实际应用中，可以根据情况挑选一种或结合两种共同使用，相辅相成。

> **注意**：以上讨论的仅为元数据备份，完整的文件系统备份方案还应至少包含对象存储数据的备份，如延迟删除、多版本等。

## 元数据恢复

在需要时， 通过 `juicefs load` 命令可以将之前导出的 JSON 内容导入到一个新的**空数据库**中，实现元数据恢复，如：

```bash
$ juicefs load redis://192.168.1.6:6379 meta.dump
```

加载过程中 `juicefs load` 会自动处理好因包含不同时间点文件而产生的冲突问题，并重新计算文件系统的统计信息（空间使用量，inode 计数器等），最后在新数据库中生成一份全局一致的元数据。另外，如果你想自定义某些元数据（请务必小心），可以尝试在 load 前手动修改 JSON 文件。

## 元数据迁移

JSON 格式可以被所有的元数据引擎识别，因此它可以作为中介帮助元数据实现跨引擎迁移，如：

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump
$ juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

或：

```bash
$ juicefs dump redis://192.168.1.6:6379 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

为确保迁移前后文件系统内容一致，需要在迁移过程中停止业务写入。另外，由于迁移前后对象存储是同一套，在新元数据引擎上线前需确保旧引擎已下线或只有只读客户端，否则可能造成文件系统损坏。

## 元数据检视

在有些情况下，`juicefs dump` 还可以辅助定位问题，因为其导出的 JSON 内容可以让用户非常直观地查看到指定目录树下所有文件的内部信息。如：

```bash
$ juicefs dump redis://192.168.1.6:6379 meta.dump --subdir /path/in/juicefs
```

另外，也可以使用 `jq` 等工具对导出文件进行分析。

> **注意**：为保证服务稳定，请不要在线上环境 dump 过于大的目录。
