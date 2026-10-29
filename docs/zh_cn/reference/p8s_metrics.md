---
title: JuiceFS 监控指标
sidebar_position: 4
---

如果你尚未搭建监控系统、收集 JuiceFS 客户端指标，阅读[「监控」](../administration/monitoring.md)文档了解如何收集这些指标以及可视化。

## 全局标签 {#global-labels}

| 名称       | 描述        |
| ----       | ----------- |
| `vol_name` | Volume 名称 |
| `instance` | 客户端主机名，格式为 `<host>:<port>`。详见[官方文档](https://prometheus.io/docs/concepts/jobs_instances) |
| `mp`       | 挂载点路径，如果是通过 [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) 上报，例如 [JuiceFS Hadoop Java SDK](../administration/monitoring.md#hadoop)，那么 `mp` 标签的值为 `sdk-<PID>` |

## 文件系统 {#file-system}

### 指标

| 名称                            | 描述            | 单位 |
|-------------------------------|---------------|----|
| `juicefs_used_space`          | 总使用空间         | 字节 |
| `juicefs_used_inodes`         | 已使用的 inodes 数量 |    |
| `juicefs_total_space`         | 文件系统总容量       | 字节 |
| `juicefs_total_inodes`        | 总 inodes 数量   |    |

## 操作系统 {#operating-system}

### 指标

| 名称                | 描述        | 单位 |
| ----                | ----------- | ---- |
| `juicefs_uptime`    | 总运行时间  | 秒   |
| `juicefs_cpu_usage` | CPU 使用量  | 秒   |
| `juicefs_memory`    | 内存使用量  | 字节 |

## 元数据引擎 {#metadata-engine}

### 标签

| 名称     | 描述                                  |
| ----     | -----------                           |
| `method` | 元数据引擎操作（例如 getattr、setattr、lookup） |

### 指标

| 名称                                              | 描述           | 单位 |
| ----                                              | -----------    | ---- |
| `juicefs_transaction_durations_histogram_seconds` | 事务的延时分布 | 秒   |
| `juicefs_transaction_restart`                     | 事务重启的次数 |      |
| `juicefs_meta_ops_durations_histogram_seconds`    | 元数据引擎操作的延时分布 | 秒 |
| `juicefs_meta_ops_total`                          | 元数据引擎操作的次数 |    |
| `juicefs_meta_ops_duration_seconds`               | 元数据引擎操作的累计耗时 | 秒 |

### Redis 连接池

使用 Redis 或 KeyDB 元数据引擎时，以下客户端统计反映主命令连接池的状态。单机和 Sentinel 客户端的 `node` 标签为空，Redis Cluster 的 `node` 标签为节点地址。不包含 Sentinel 控制连接和 Pub/Sub 连接池。采集只读取各连接池的本地统计，不发送 Redis 命令或刷新集群拓扑。

| 名称 | 类型 | 描述 |
| ---- | ---- | ---- |
| `juicefs_redis_pool_connections` | Gauge | 命令连接池的连接数 |
| `juicefs_redis_pool_idle_connections` | Gauge | 命令连接池的空闲连接数 |
| `juicefs_redis_pool_pending_requests` | Gauge | 正在获取命令连接的请求数 |
| `juicefs_redis_pool_waits_total` | Counter | 等待后取得命令连接池许可的累计次数 |
| `juicefs_redis_pool_wait_duration_seconds_total` | Counter | 等待取得命令连接池许可的累计时间，单位为秒 |
| `juicefs_redis_pool_timeouts_total` | Counter | 等待命令连接超时的累计次数 |

Redis 的等待次数和时间在请求等待后取得连接池许可时累计，不计入超时或取消的等待；已计数的等待仍可能在后续建连时失败。连接池超时单独计数。连接池重建时 Counter 可能重置，包括 Cluster 拓扑变更的场景。跨节点聚合应先计算 `rate()`，例如 `sum by (instance, mp, vol_name) (rate(juicefs_redis_pool_timeouts_total[5m]))`。连接池关闭后连接数归零，其客户端被垃圾回收后，对应时间序列在后续采集中消失。自定义 `node` 标签冲突时，该 collector 注册失败并记录警告，公共元数据指标仍可使用。

## 配额 {#quota}

### 指标

目录配额带有 `inode` 标签，用户配额带有 `uid` 标签，组配额带有 `gid` 标签。

| 名称                                        | 描述                       | 单位 |
|---------------------------------------------|---------------------------|------|
| `juicefs_dir_quota_max_space_bytes`         | 目录配额的空间上限          | 字节 |
| `juicefs_dir_quota_max_inodes`              | 目录配额的 inodes 上限     |      |
| `juicefs_dir_quota_used_space_bytes`        | 目录配额已使用的空间        | 字节 |
| `juicefs_dir_quota_used_inodes`             | 目录配额已使用的 inodes 数量 |     |
| `juicefs_user_quota_max_space_bytes`        | 用户配额的空间上限          | 字节 |
| `juicefs_user_quota_max_inodes`             | 用户配额的 inodes 上限     |      |
| `juicefs_user_quota_used_space_bytes`       | 用户配额已使用的空间        | 字节 |
| `juicefs_user_quota_used_inodes`            | 用户配额已使用的 inodes 数量 |     |
| `juicefs_group_quota_max_space_bytes`       | 组配额的空间上限            | 字节 |
| `juicefs_group_quota_max_inodes`            | 组配额的 inodes 上限       |      |
| `juicefs_group_quota_used_space_bytes`      | 组配额已使用的空间          | 字节 |
| `juicefs_group_quota_used_inodes`           | 组配额已使用的 inodes 数量  |     |

### SQL 连接池

使用 MySQL、PostgreSQL 或 SQLite 元数据引擎时，以下指标反映各客户端的连接池状态。它们由 Prometheus Go SQL collector 提供，带有额外标签 `db_name="metadata"`。采集只读取本地连接池统计，不向数据库发起查询。这些是客户端统计，而非数据库服务端的总量。

| 名称 | 类型 | 描述 |
| ---- | ---- | ---- |
| `juicefs_go_sql_max_open_connections` | Gauge | 最大连接数，0 表示不限制 |
| `juicefs_go_sql_open_connections` | Gauge | 已打开的连接数，包括使用中和空闲连接 |
| `juicefs_go_sql_in_use_connections` | Gauge | 正在使用的连接数 |
| `juicefs_go_sql_idle_connections` | Gauge | 空闲连接数 |
| `juicefs_go_sql_wait_count_total` | Counter | 等待获取连接的累计次数 |
| `juicefs_go_sql_wait_duration_seconds_total` | Counter | 等待获取连接的累计时间，单位为秒 |
| `juicefs_go_sql_max_idle_closed_total` | Counter | 因空闲连接数限制而关闭的连接总数 |
| `juicefs_go_sql_max_idle_time_closed_total` | Counter | 因达到最大空闲时间而关闭的连接总数 |
| `juicefs_go_sql_max_lifetime_closed_total` | Counter | 因达到最大连接生命周期而关闭的连接总数 |

元数据操作变慢时，等待指标可用于判断是否存在连接池争用。Counter 在客户端连接池的生命周期内累计，可用 `rate()` 或 `increase()` 查看指定时间窗口内的变化。

## FUSE {#fuse}

### 标签

| 名称     | 描述                            |
| ----     | -----------                     |
| `method` | FUSE 操作（例如 read、write、lookup） |
| `errno`  | 失败操作的错误名（例如 EIO、ENOENT） |

### 指标

| 名称                                           | 描述                 | 单位 |
| ----                                           | -----------          | ---- |
| `juicefs_fuse_read_size_bytes`                 | 读请求的大小分布     | 字节 |
| `juicefs_fuse_written_size_bytes`              | 写请求的大小分布     | 字节 |
| `juicefs_fuse_ops_durations_histogram_seconds` | 所有请求的延时分布   | 秒   |
| `juicefs_fuse_ops_total`                       | 操作的次数           |      |
| `juicefs_fuse_ops_durations_seconds`           | 操作的累计耗时       | 秒   |
| `juicefs_fuse_ops_io_errors`                   | IO 错误的次数        |      |
| `juicefs_fuse_open_handlers`                   | 打开的文件和目录数量 |      |

## SDK {#sdk}

### 指标

| 名称                                          | 描述               | 单位 |
| ----                                          | -----------        | ---- |
| `juicefs_sdk_read_size_bytes`                 | 读请求的大小分布   | 字节 |
| `juicefs_sdk_written_size_bytes`              | 写请求的大小分布   | 字节 |
| `juicefs_sdk_ops_durations_histogram_seconds` | SDK 内部操作的延时分布（一次请求可能产生多次操作） | 秒   |

## 缓存 {#cache}

### 指标

| 名称                                      | 描述          | 单位 |
|-----------------------------------------|-------------|----|
| `juicefs_blockcache_blocks`             | 缓存块的总个数     |    |
| `juicefs_blockcache_bytes`              | 缓存块的总大小     | 字节 |
| `juicefs_blockcache_hits`               | 命中缓存块的总次数   |    |
| `juicefs_blockcache_miss`               | 没有命中缓存块的总次数 |    |
| `juicefs_blockcache_writes`             | 写入缓存块的总次数   |    |
| `juicefs_blockcache_drops`              | 丢弃缓存块的总次数   |    |
| `juicefs_blockcache_evicts`             | 淘汰缓存块的总次数   |    |
| `juicefs_blockcache_hit_bytes`          | 命中缓存块的总大小   | 字节 |
| `juicefs_blockcache_miss_bytes`         | 没有命中缓存块的总大小 | 字节 |
| `juicefs_blockcache_write_bytes`        | 写入缓存块的总大小   | 字节 |
| `juicefs_blockcache_read_hist_seconds`  | 读缓存块的延时分布   | 秒  |
| `juicefs_blockcache_write_hist_seconds` | 写缓存块的延时分布   | 秒  |
| `juicefs_staging_blocks`                | 暂存路径中的块数    |    |
| `juicefs_staging_block_bytes`           | 暂存路径中块的总字节数 | 字节 |
| `juicefs_staging_block_delay_seconds`   | 暂存块延迟的总秒数 | 秒  |
| `juicefs_staging_block_errors`          | 暂存块出错的次数 |    |
| `juicefs_staging_write_bytes`           | 写入暂存路径的总字节数 | 字节 |
| `juicefs_staging_writing_blocks`        | 正在写入暂存路径的块数 |    |

## 对象存储 {#object-storage}

### 标签

| 名称            | 描述                                              |
| ----            | -----------                                       |
| `method`        | 请求对象存储的方法（例如 GET、PUT、HEAD、DELETE） |
| `storage_class` | 请求对象所属的存储类型（配置了存储类型时存在）     |

### 指标

| 名称                                                 | 描述                     | 单位 |
| ----                                                 | -----------              | ---- |
| `juicefs_object_request_durations_histogram_seconds` | 请求对象存储的延时分布   | 秒   |
| `juicefs_object_request_errors`                      | 请求失败的总次数         |      |
| `juicefs_object_request_data_bytes`                  | 请求对象存储的总数据大小 | 字节 |
| `juicefs_object_request_uploading`                   | 正在进行中的上传请求数   |      |

## 内部特性 {#internal}

### 指标

| 名称                                     | 描述               | 单位 |
|----------------------------------------| -----------        | ---- |
| `juicefs_compact_size_histogram_bytes` | 合并数据的大小分布 | 字节 |
| `juicefs_used_read_buffer_size_bytes`  | 当前用于读取的缓冲区的大小 | 字节 |
| `juicefs_used_buffer_size_bytes`       | 当前用于写入的缓冲区的大小 | 字节 |
| `juicefs_store_cache_size_bytes`       | 内存中的数据缓存大小 | 字节 |
| `juicefs_last_successful_backup`       | 上次成功备份元数据的时间戳（仅在启用 `--backup-meta` 时导出） | 秒 |
| `juicefs_last_backup_duration`         | 上次备份元数据的耗时（仅在启用 `--backup-meta` 时导出） | 秒 |
| `juicefs_bgjob_duration_seconds` | 后台清理任务耗时（标签：`job`、`status`） | 秒 |
| `juicefs_bgjob_deletions_total` | 后台任务执行的删除总数（标签：`job`） | |

## 挂载配置 {#mount-configuration}

### 指标

| 名称 | 描述 | 单位 |
|-|-|-|
| `juicefs_subdir_info` | 当前挂载的子目录配置信息（标签：`subdir`，值恒为 `1`） | |

`subdir` 标签包含挂载的子目录路径，空字符串表示根挂载。

## 数据同步 {#sync}

### 指标

| 名称 | 描述 | 单位 |
|-|-|-|
| `juicefs_sync_scanned` | 从源端扫描的所有对象数量 | |
| `juicefs_sync_handled` | 已经处理过的来自源端的对象数量 | |
| `juicefs_sync_pending` | 等待同步的对象数量 | |
| `juicefs_sync_copied` | 已经同步过的对象数量 | |
| `juicefs_sync_copied_bytes` | 已经同步过的数据总大小 | 字节 |
| `juicefs_sync_skipped` | 同步时被跳过的对象数量 | |
| `juicefs_sync_skipped_bytes` | 同步时被跳过的数据总大小 | 字节 |
| `juicefs_sync_excluded` | 同步时被规则排除的对象数量 | |
| `juicefs_sync_excluded_bytes` | 同步时被规则排除的数据总大小 | 字节 |
| `juicefs_sync_extra` | 目标端多出的对象数量 | |
| `juicefs_sync_extra_bytes` | 目标端多出的数据总大小 | 字节 |
| `juicefs_sync_failed` | 同步时失败的对象数量 | |
| `juicefs_sync_deleted` | 同步时被删除的对象数量 | |
| `juicefs_sync_checked` | 同步时校验过 checksum 的对象数量 | |
| `juicefs_sync_checked_bytes` | 同步时校验过 checksum 的数据总大小 | 字节 |
| `juicefs_sync_listed_prefix` | 同步时列出的前缀数量 | |
