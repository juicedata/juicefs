---
sidebar_label: 元数据备份和恢复
sidebar_position: 2
slug: /metadata_dump_load
---
# 元数据备份和恢复

:::tip 提示
- JuiceFS v0.15.2 开始支持元数据手动备份、恢复和引擎间迁移。
- JuiceFS v1.0.0 开始支持元数据自动备份
:::

## 手动备份

JuiceFS 支持[多种元数据存储引擎](../guide/how_to_set_up_metadata_engine.md)，且各引擎内部的数据管理格式各有不同。为了便于管理，JuiceFS 提供了 `dump` 命令允许将所有元数据以统一格式写入到 [JSON](https://www.json.org/json-en.html) 文件进行备份。同时，JuiceFS 也提供了 `load` 命令，允许将备份恢复或迁移到任意元数据存储引擎。命令的详细信息请参考[这里](../reference/command_reference.md#juicefs-dump)。

### 元数据备份

使用 JuiceFS 客户端提供的 `dump` 命令可以将元数据导出到文件，例如：

```bash
juicefs dump redis://192.168.1.6:6379/1 meta.dump
```

该命令默认从根目录 `/` 开始，深度遍历目录树下所有文件，将每个文件的元数据信息按 JSON 格式写入到文件。

:::note 注意
`juicefs dump` 仅保证单个文件自身的完整性，不提供全局时间点快照的功能，如在 dump 过程中业务仍在写入，最终结果会包含不同时间点的信息。
:::

Redis、MySQL 等数据库都有其自带的备份工具，如 [Redis RDB](https://redis.io/topics/persistence#backing-up-redis-data) 和 [mysqldump](https://dev.mysql.com/doc/mysql-backup-excerpt/5.7/en/mysqldump-sql-format.html) 等，使用它们作为 JuiceFS 元数据存储，你仍然有必要用各个数据库自身的备份工具定期备份元数据。

`juicefs dump` 的价值在于它能将完整的元数据信息以统一的 JSON 格式导出，便于管理和保存，而且不同的元数据存储引擎都可以识别并导入。在实际应用中，`dump` 命令于数据库自带的备份工具应该共同使用，相辅相成。

:::note 注意
以上讨论的仅为元数据备份，完整的文件系统备份方案还应至少包含对象存储数据的备份，如异地容灾、回收站、多版本等。
:::

### 元数据恢复

:::tip 特别提示
JSON 备份只能恢复到 `新创建的数据库` 或 `空数据库` 中。
:::

使用 JuiceFS 客户端提供的 `load` 命令可以将已备份的 JSON 文件中的元数据导入到一个新的**空数据库**中，例如：

```bash
juicefs load redis://192.168.1.6:6379/1 meta.dump
```

该命令会自动处理因包含不同时间点文件而产生的冲突问题，并重新计算文件系统的统计信息（空间使用量，inode 计数器等），最后在数据库中生成一份全局一致的元数据。另外，如果你想自定义某些元数据（请务必小心），可以尝试在 load 前手动修改 JSON 文件。

:::note 注意
为了保证对象存储 SecretKey 与 SessionToken 的安全性，`juicefs dump` 得到的备份文件中的 SecretKey 与 SessionToken 会被改写为“removed”，所以在对其执行 `juicefs load` 恢复到元数据引擎后，需要使用 `juicefs config --secret-key xxxxx META-URL` 来重新设置 SecretKey。
:::

### 元数据迁移

:::tip 特别提示
元数据迁移操作要求目标数据库是 `新创建的` 或 `空数据库`。
:::

得益于 JSON 格式的通用性，JuiceFS 支持的所有元数据存储引擎都能识别，因此可以将元数据信息从一种引擎中导出为 JSON 备份，然后再导入到另外一种引擎，从而实现元数据在不同类型引擎间的迁移。例如：

```bash
juicefs dump redis://192.168.1.6:6379/1 meta.dump
```
```bash
juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs meta.dump
```

也可以通过系统的 Pipe 直接迁移：

```bash
juicefs dump redis://192.168.1.6:6379/1 | juicefs load mysql://user:password@(192.168.1.6:3306)/juicefs
```

:::caution 风险提示
为确保迁移前后文件系统内容一致，需要在迁移过程中停止业务写入。另外，由于迁移后仍使用原来的对象存储，在新的元数据引擎上线前，请确保旧的引擎已经下线或仅有对象存储的只读权限，否则可能造成文件系统损坏。
:::

### 元数据检视

除了可以导出完整的元数据信息，`dump` 命令还支持导出特定子目录中的元数据。因为导出的 JSON 内容可以让用户非常直观地查看到指定目录树下所有文件的内部信息，因此常被用来辅助排查问题。例如：

```bash
juicefs dump redis://192.168.1.6:6379/1 meta.dump --subdir /path/in/juicefs
```

另外，也可以使用 `jq` 等工具对导出文件进行分析。

:::note 注意
为保证服务稳定，请不要在线上环境 dump 过于大的目录。
:::

## 自动备份

从 JuiceFS v1.0.0 开始，不论文件系统通过 `mount` 命令挂载，还是通过 JuiceFS S3 网关及 Hadoop Java SDK 访问，客户端每小时都会自动备份元数据并拷贝到对象存储。

备份的文件存储在对象存储的 `meta` 目录中，它是一个独立于数据存储的目录，在挂载点中不可见，也不会与数据存储之间产生影响，用对象存储的文件浏览器即可查看和管理。

![](../images/meta-auto-backup-list.png)

默认情况下，JuiceFS 客户端每小时备份一次元数据，自动备份的频率可以在挂载文件系统时通过 `--backup-meta` 选项进行调整，例如，要设置为每 8 个小时执行一次自动备份：

```
sudo juicefs mount -d --backup-meta 8h redis://127.0.0.1:6379/1 /mnt
```

备份频率可以精确到秒，支持的单位如下：

- `h`：精确到小时，如 `1h`；
- `m`：精确到分钟，如 `30m`、`1h30m`；
- `s`：精确到秒，如 `50s`、`30m50s`、`1h30m50s`;

值得一提的是，备份操作耗时会随着文件系统内文件数的增多而增加，因此当文件数较多（默认为达到一百万）且自动备份频率为默认值 1 小时的情况下 JuiceFS 会自动跳过元数据备份，并打印相应的告警日志。此时可以选择挂载一个新客户端并设置较大的 `--backup-meta` 参数来重新启用自动备份。

作为参考，当使用 Redis 作为元数据引擎时，备份一百万文件的元数据大约需要 1 分钟，消耗约 1GB 内存。

### 自动备份策略

虽然自动备份元数据成为了客户端的默认动作，但在多主机共享挂载同一个文件系统时并不会发生备份冲突。

JuiceFS 维护了一个全局的时间戳，确保同一时刻只有一个客户端执行备份操作。当客户端之间设置了不同的备份周期，那么就会以周期最短的设置为准进行备份。

### 备份清理策略

JuiceFS 会按照以下规则定期清理备份：

- 保留 2 天以内全部的备份；
- 超过 2 天不足 2 周的，保留每天中的 1 个备份；
- 超过 2 周不足 2 月的，保留每周中的 1 个备份；
- 超过 2 个月的，保留每个月中的 1 个备份。
