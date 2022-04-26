# 版本更新

:::tip 提示
所有历史版本请查看 [GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面
:::

## 升级到 JuiceFS v1.0.0 Beta3

JuiceFS 的客户端只有一个二进制文件，升级时只需要将用新版替换旧版即可。同时有以下几点需要注意。

### 调整 SQL 表结构

需要注意的是，JuiceFS v1.0.0 Beta3 变更了 SQL 类元数据引擎的表结构，对于已经创建的文件系统，应该先升级 SQL 表结构，然后再升级客户端。

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

```sql
create table jfs_edge_dg_tmp
(
    parent INTEGER not null,
    name   blob    not null,
    inode  INTEGER not null,
    type   INTEGER not null
);

insert into jfs_edge_dg_tmp(parent, name, inode, type)
select parent, name, inode, type
from jfs_edge;

drop table jfs_edge;

alter table jfs_edge_dg_tmp
    rename to jfs_edge;

create unique index UQE_jfs_edge_edge
    on jfs_edge (parent, name);

create table jfs_symlink_dg_tmp
(
    inode  INTEGER not null
        primary key,
    target blob    not null
);

insert into jfs_symlink_dg_tmp(inode, target)
select inode, target
from jfs_symlink;

drop table jfs_symlink;

alter table jfs_symlink_dg_tmp
    rename to jfs_symlink;
```

### 会话管理格式变更

JuiceFS v1.0.0 Beta3 使用了新的会话管理格式，历史版本客户端通过 `juicefs status` 或者 `juicefs destroy` 将无法看到 v1.0.0 Beta3 客户端产生的会话，新版客户端可以看到所有会话。
