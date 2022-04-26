# 版本更新

:::tip 提示
所有历史版本请查看 [GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面
:::

## 升级到 JuiceFS v1.0.0 Beta3

JuiceFS 的客户端只有一个二进制文件，升级时只需要将用新版替换旧版即可。同时有以下几点需要注意。

需要注意的是，JuiceFS v1.0.0 Beta3 变更了 SQL 类元数据引擎的表结构用以支持非 UTF-8 字符，对于已经创建的文件系统或者正在运行的文件系统，要在升级完所有客户端后再做表结构变更。

### 调整 SQL 表结构以支持非 UTF-8 字符

:::note 注意
表结构升级不是强制要求，只有当你需要使用非 UTF-8 字符时才需要升级。
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
