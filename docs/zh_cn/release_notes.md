# 版本更新

:::tip 提示
所有历史版本请查看 [GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面
:::

## 版本号 {#version-number}

JuiceFS 社区版采用[语义化版本号](https://semver.org/lang/zh-CN)标记方式，每个版本号都由三个数字组成 `x.y.z`，分别是主版本号（x）、次版本号（y）和修订号（z）。

1. **主版本号（x）**：主版本号大于等于 `1` 时，表示该版本已经适用于生产环境。当主版本号发生变化时，表明这个版本可能增加了不能向后兼容的重大功能、架构变化或数据格式变化。例如，`v0.8.3` → `v1.0.0` 代表生产就绪，`v1.0.0` → `v2.0.0` 代表架构或功能变化。
2. **次版本号（y）**：次版本号表示该版本增加了一些能够向后兼容的新功能、性能优化和 bug 修复等。例如，`v1.0.0` → `v1.1.0`。
3. **修订号（z）**：修订号表示软件的小更新或者 bug 修复，只是对现有功能的一些小的改动或者修复，不会影响软件兼容性。例如，`v1.0.3` → `v1.0.4`。

## 版本升级 {#changes}

JuiceFS 的客户端只有一个二进制文件，一般情况下升级时只需要用新版本软件替换旧版即可。

### JuiceFS v1.1

:::tip 提示
若您正在使用的版本小于 v1.0，请先[升级到 v1.0 版本](#juicefs-v10)。
:::

JuiceFS 在 v1.1（具体而言，是 v1.1.0-beta2）版本中新增了[**目录用量统计**](https://juicefs.com/docs/zh/community/guide/dir-stats)和[**目录配额**](https://juicefs.com/docs/zh/community/guide/quota#directory-quota)两个功能，且目录配额依赖于用量统计。这两项功能在旧版本客户端中没有，当它们被开启的情况下使用旧客户端写入会导致统计数值出现较大偏差。在升级到 v1.1 时，若您不打算启用这两项新功能，可以直接使用新版本客户端替换升级，无需额外操作。若您打算使用，则建议您在升级前了解以下内容。

#### 默认配置

目前这两项功能的默认配置为：

- 新创建的文件系统，会自动启用

- 已有的文件系统，默认均不启用
  - 目录用量统计可以通过 `juicefs config` 命令单独开启
  - 设置目录配额时，用量统计会自动开启

#### 推荐升级步骤

1. 升级所有客户端软件到 v1.1 版本
2. 拒绝 v1.1 之前的版本再次连接：`juicefs config META-URL --min-client-version 1.1.0-A`
3. 在合适的时间重启服务（重新挂载，重启 gateway 等）
4. 确保所有在线客户端版本都在 v1.1 或以上：`juicefs status META-URL | grep -w Version`
5. 启用新特性，具体参见[目录用量统计](https://juicefs.com/docs/zh/community/guide/dir-stats)和[目录配额](https://juicefs.com/docs/zh/community/guide/quota#directory-quota)

### JuiceFS v1.0

JuiceFS 在 v1.0（具体而言，是 v1.0.0-beta3）版本中有两项兼容性修改。若您原来使用的客户端版本较低，建议您在升级前先了解以下内容。

#### 调整 SQL 表结构以支持非 UTF-8 字符

JuiceFS v1.0 改进了 SQL 元数据引擎对非 UTF-8 字符集的支持。对于已有的文件系统，需要手动调整表结构才能支持非 UTF-8 字符集，建议在升级完所有客户端后再选择访问压力比较低的时候进行操作。

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

JuiceFS v1.0 使用了新的会话管理格式，历史版本客户端通过 `juicefs status` 或者 `juicefs destroy` 将无法看到 v1.0 客户端产生的会话，新版客户端可以看到所有会话。
