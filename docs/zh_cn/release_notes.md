# 版本更新

:::tip 提示
所有历史版本请查看 [GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面
:::

## 版本号

JuiceFS 社区版采用语义化版本号标记方式，每个版本号都由三个数字组成 `x.y.z`，分别是主版本号（x）、次版本号（y）和修订号（z）。

1. **主版本号（x）**：主版本号表示该版本已经适用于生产环境。当主版本号发生变化时，表明这个版本可能增加了不能向后兼容的重大功能、架构变化或数据格式变化。例如，`v0.8.3` -> `v1.0.0` 代表生产就绪。`v1.0.0` -> `v2.0.0` 代表架构或功能变化。

2. **次版本号（y）**：次版本号表示该版本增加了一些能够向后兼容的新功能、性能优化和 bug 修复等。例如，`v1.0.0` -> `v1.1.0`。

3. **修订号（z）**：修订号表示软件的小更新或者 bug 修复，只是对现有功能的一些小的改动或者修复，不会影响软件兼容性。例如，`v1.0.3` -> `v1.0.4`。

## 版本变化

### JuiceFS v1.0.0 Beta3

JuiceFS 的客户端只有一个二进制文件，升级时只需要将用新版替换旧版即可。

#### 调整 SQL 表结构以支持非 UTF-8 字符

JuiceFS v1.0.0 Beta3 改进了 SQL 引擎对非 UTF-8 字符集的支持。对于已有的文件系统，需要手动调整表结构才能支持非 UTF-8 字符集，建议在升级完所有客户端后再选择访问压力比较低的时候进行操作。

:::note 注意
调整 SQL 表结构时数据库性能可能会下降，影响正在运行的服务。
:::

##### MySQL/MariaDB

```sql
alter table jfs_edge
    modify name varbinary(255) not null;
alter table jfs_symlink
    modify target varbinary(4096) not null;
```

##### PostgreSQL

```sql
alter table jfs_edge
    alter column name type bytea using name::bytea;
alter table jfs_symlink
    alter column target type bytea using target::bytea;
```

##### SQLite

由于 SQLite 不支持修改字段，可以通过 dump 和 load 命令进行迁移，详情参考：[JuiceFS 元数据备份和恢复](administration/metadata_dump_load.md)。

#### 会话管理格式变更

JuiceFS v1.0.0 Beta3 使用了新的会话管理格式，历史版本客户端通过 `juicefs status` 或者 `juicefs destroy` 将无法看到 v1.0.0 Beta3 客户端产生的会话，新版客户端可以看到所有会话。
