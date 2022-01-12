---
sidebar_label: JuiceFS 监控指标
sidebar_position: 2
slug: /p8s_metrics
---

# JuiceFS 监控指标

:::tip 提示
请查看[「监控」](../administration/monitoring.md)文档了解如何收集及展示 JuiceFS 监控指标
:::

## 全局标签

| 名称       | 描述        |
| ----       | ----------- |
| `vol_name` | Volume 名称 |
| `mp`       | 挂载点路径  |

:::info 说明
Prometheus 在抓取监控指标时会自动附加 `instance` 标签以帮助识别不同的抓取目标，格式为 `<host>:<port>`。详见[官方文档](https://prometheus.io/docs/concepts/jobs_instances)。
:::

:::info 说明
如果是通过 [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) 的方式上报监控指标（例如 [JuiceFS Hadoop Java SDK](../administration/monitoring.md#hadoop)），`mp` 标签的值为 `sdk-<PID>`，`instance` 标签的值为主机名。
:::

## 文件系统

### 指标

| 名称                  | 描述           | 单位 |
| ----                  | -----------    | ---- |
| `juicefs_used_space`  | 总使用空间     | 字节 |
| `juicefs_used_inodes` | 总 inodes 数量 |      |

## 操作系统

### 指标

| 名称                | 描述        | 单位 |
| ----                | ----------- | ---- |
| `juicefs_uptime`    | 总运行时间  | 秒   |
| `juicefs_cpu_usage` | CPU 使用量  | 秒   |
| `juicefs_memory`    | 内存使用量  | 字节 |

## 元数据引擎

### 指标

| 名称                                              | 描述           | 单位 |
| ----                                              | -----------    | ---- |
| `juicefs_transaction_durations_histogram_seconds` | 事务的延时分布 | 秒   |
| `juicefs_transaction_restart`                     | 事务重启的次数 |      |

## FUSE

### 指标

| 名称                                           | 描述                 | 单位 |
| ----                                           | -----------          | ---- |
| `juicefs_fuse_read_size_bytes`                 | 读请求的大小分布     | 字节 |
| `juicefs_fuse_written_size_bytes`              | 写请求的大小分布     | 字节 |
| `juicefs_fuse_ops_durations_histogram_seconds` | 所有请求的延时分布   | 秒   |
| `juicefs_fuse_open_handlers`                   | 打开的文件和目录数量 |      |

## SDK

### 指标

| 名称                                          | 描述               | 单位 |
| ----                                          | -----------        | ---- |
| `juicefs_sdk_read_size_bytes`                 | 读请求的大小分布   | 字节 |
| `juicefs_sdk_written_size_bytes`              | 写请求的大小分布   | 字节 |
| `juicefs_sdk_ops_durations_histogram_seconds` | 所有请求的延时分布 | 秒   |

## 缓存

### 指标

| 名称                                    | 描述                   | 单位 |
| ----                                    | -----------            | ---- |
| `juicefs_blockcache_blocks`             | 缓存块的总个数         |      |
| `juicefs_blockcache_bytes`              | 缓存块的总大小         | 字节 |
| `juicefs_blockcache_hits`               | 命中缓存块的总次数     |      |
| `juicefs_blockcache_miss`               | 没有命中缓存块的总次数 |      |
| `juicefs_blockcache_writes`             | 写入缓存块的总次数     |      |
| `juicefs_blockcache_drops`              | 丢弃缓存块的总次数     |      |
| `juicefs_blockcache_evicts`             | 淘汰缓存块的总次数     |      |
| `juicefs_blockcache_hit_bytes`          | 命中缓存块的总大小     | 字节 |
| `juicefs_blockcache_miss_bytes`         | 没有命中缓存块的总大小 | 字节 |
| `juicefs_blockcache_write_bytes`        | 写入缓存块的总大小     | 字节 |
| `juicefs_blockcache_read_hist_seconds`  | 读缓存块的延时分布     | 秒   |
| `juicefs_blockcache_write_hist_seconds` | 写缓存块的延时分布     | 秒   |

## 对象存储

### 标签

| 名称     | 描述                                              |
| ----     | -----------                                       |
| `method` | 请求对象存储的方法（例如 GET、PUT、HEAD、DELETE） |

### 指标

| 名称                                                 | 描述                     | 单位 |
| ----                                                 | -----------              | ---- |
| `juicefs_object_request_durations_histogram_seconds` | 请求对象存储的延时分布   | 秒   |
| `juicefs_object_request_errors`                      | 请求失败的总次数         |      |
| `juicefs_object_request_data_bytes`                  | 请求对象存储的总数据大小 | 字节 |

## 内部特性

### 指标

| 名称                                   | 描述               | 单位 |
| ----                                   | -----------        | ---- |
| `juicefs_compact_size_histogram_bytes` | 合并数据的大小分布 | 字节 |
