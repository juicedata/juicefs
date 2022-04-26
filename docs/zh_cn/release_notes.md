# 版本更新

:::tip 提示
所有历史版本请查看 [GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面
:::

## 升级到 JuiceFS v1.0.0 Beta3

JuiceFS 的客户端只有一个二进制文件，升级时只需要将用新版替换旧版即可。

### 调整 SQL 表结构以支持非 UTF-8 字符

JuiceFS v1.0.0 Beta3 改进了 SQL 引擎对非 UTF-8 字符集的支持。对于已有的文件系统，需要手动调整表结构才能支持非 UTF-8 字符集，建议在升级完所有客户端后再选择访问压力比较低的时候进行操作。

:::note 注意
调整 SQL 表结构时数据库性能可能会下降，影响正在运行的服务。
:::

#### MySQL/MariaDB

```sql
alter table jfs_edge
    modify name varbinary(255) not null;
alter table jfs_symlink
    modify target varbinary(4096) not null;
```

#### PostgreSQL

```sql
alter table jfs_edge
    alter column name type bytea using name::bytea;
alter table jfs_symlink
    alter column target type bytea using target::bytea;
```

#### SQLite

由于 SQLite 不支持修改字段，可以通过 dump 和 load 命令进行迁移，详情参考：[JuiceFS 元数据备份和恢复](administration/metadata_dump_load.md)。

### 会话管理格式变更

JuiceFS v1.0.0 Beta3 使用了新的会话管理格式，历史版本客户端通过 `juicefs status` 或者 `juicefs destroy` 将无法看到 v1.0.0 Beta3 客户端产生的会话，新版客户端可以看到所有会话。
