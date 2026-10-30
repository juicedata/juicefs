---
title: JuiceFS Metrics
sidebar_position: 4
---

If you haven't yet set up monitoring for JuiceFS, read [monitoring and data visualization"](../administration/monitoring.md) to learn how.

## Global labels {#global-labels}

| Name       | Description      |
| ----       | -----------      |
| `vol_name` | Volume name      |
| `instance` | Client host name in format `<host>:<port>`. Refer to [official document](https://prometheus.io/docs/concepts/jobs_instances) for more information |
| `mp`       | Mount point path, if metrics are reported through [Prometheus Pushgateway](https://github.com/prometheus/pushgateway), for example, [JuiceFS Hadoop Java SDK](../administration/monitoring.md#hadoop), `mp` will be `sdk-<PID>` |

## File system {#file-system}

### Metrics

| Name                          | Description                            | Unit |
|-------------------------------|----------------------------------------|------|
| `juicefs_used_space`          | Total used space                       | byte |
| `juicefs_used_inodes`         | Total number of used inodes            |      |
| `juicefs_total_space`         | Total space of the volume              | byte |
| `juicefs_total_inodes`        | Total number of inodes                 |      |

## Operating system {#operating-system}

### Metrics

| Name                | Description           | Unit   |
| ----                | -----------           | ----   |
| `juicefs_uptime`    | Total running time    | second |
| `juicefs_cpu_usage` | Accumulated CPU usage | second |
| `juicefs_memory`    | Used memory           | byte   |

## Metadata engine {#metadata-engine}

### Labels

| Name     | Description                                  |
| ----     | -----------                                  |
| `method` | Metadata engine operation (e.g. getattr, setattr, lookup) |

### Metrics

| Name                                              | Description                                | Unit   |
| ----                                              | -----------                                | ----   |
| `juicefs_transaction_durations_histogram_seconds` | Transactions latency distributions         | second |
| `juicefs_transaction_restart`                     | Number of times a transaction restarted    |        |
| `juicefs_meta_ops_durations_histogram_seconds`    | Metadata engine operations latency distributions | second |
| `juicefs_meta_ops_total`                          | Number of metadata engine operations       |        |
| `juicefs_meta_ops_duration_seconds`               | Accumulated time of metadata engine operations | second |

### Redis connection pools

When using Redis or KeyDB as the metadata engine, the following client-side metrics describe the main command connection pool used by standalone and Sentinel clients. Redis Cluster pool metrics are not collected. These metrics are provided by the official `go-redis` Prometheus collector and have no additional labels. Sentinel control connections and Pub/Sub pools are excluded. Scraping reads local pool statistics without sending Redis commands.

| Name | Type | Description |
| ---- | ---- | ----------- |
| `juicefs_redis_pool_hit_total` | Counter | Total number of times an idle connection was reused from the pool |
| `juicefs_redis_pool_miss_total` | Counter | Total number of times no reusable idle connection was found in the pool |
| `juicefs_redis_pool_timeout_total` | Counter | Total number of timeouts acquiring a connection from the pool |
| `juicefs_redis_pool_conn_total_current` | Gauge | Current number of connections in the command pool |
| `juicefs_redis_pool_conn_idle_current` | Gauge | Current number of idle connections in the command pool |
| `juicefs_redis_pool_conn_stale_total` | Counter | Total number of stale connections removed from the pool |

A pool miss triggers an attempt to create a new connection. Counters accumulate over the lifetime of the client's pool and can reset when the pool is recreated. Use `rate()` or `increase()` to observe changes over a specified time window.

### TiKV client

For the TiKV metadata engine, the following metrics expose TiKV Go client's RPC and request queue activity. They read in-process statistics without querying TiKV.

These collectors are shared by all TiKV clients in the process. When the Java or Python SDK opens multiple volumes, each volume's metrics export the same process totals. The `vol_name` and `mp` labels identify the exporter context, not the owner of these statistics; do not sum these metrics across volumes in the same process.

| Name | Type | Description |
| ---- | ---- | ----------- |
| `juicefs_tikv_client_go_request_seconds` | Histogram | RPC request duration in seconds; labels: `type`, `store`, `stale_read`, `scope` |
| `juicefs_tikv_client_go_batch_pending_requests` | Histogram | Number of requests pending in the batch channel; label: `store` |
| `juicefs_tikv_client_go_batch_client_wait_connection_establish` | Histogram | Time spent waiting for a batch connection to be established, in seconds |
| `juicefs_tikv_client_go_batch_client_no_available_connection_total` | Counter | Number of times no batch client is available to send requests |

The metric names and types follow the TiKV Go client. The connection establishment histogram uses seconds despite not having a `_seconds` suffix. The unavailable batch client counter can increase because of concurrency limits or a busy sender; it is not a SQL-style connection pool timeout counter.

When the SDK pushes metrics to Pushgateway, Graphite, or a remote write endpoint, a custom push label that conflicts with a TiKV metric label causes that TiKV collector to be skipped with a warning. Existing JuiceFS metrics and non-conflicting TiKV collectors continue to be exported.

## Quota {#quota}

### Metrics

Directory quotas carry an `inode` label, user quotas a `uid` label, and group quotas a `gid` label.

| Name                                        | Description                                   | Unit |
|---------------------------------------------|-----------------------------------------------|------|
| `juicefs_dir_quota_max_space_bytes`         | Directory quota maximum space                 | byte |
| `juicefs_dir_quota_max_inodes`              | Directory quota maximum number of inodes      |      |
| `juicefs_dir_quota_used_space_bytes`        | Directory quota used space                    | byte |
| `juicefs_dir_quota_used_inodes`             | Directory quota used number of inodes         |      |
| `juicefs_user_quota_max_space_bytes`        | User quota maximum space                      | byte |
| `juicefs_user_quota_max_inodes`             | User quota maximum number of inodes           |      |
| `juicefs_user_quota_used_space_bytes`       | User quota used space                         | byte |
| `juicefs_user_quota_used_inodes`            | User quota used number of inodes              |      |
| `juicefs_group_quota_max_space_bytes`       | Group quota maximum space                     | byte |
| `juicefs_group_quota_max_inodes`            | Group quota maximum number of inodes          |      |
| `juicefs_group_quota_used_space_bytes`      | Group quota used space                        | byte |
| `juicefs_group_quota_used_inodes`           | Group quota used number of inodes             |      |

### SQL connection pool

For MySQL, PostgreSQL, and SQLite metadata engines, the following metrics describe each client's connection pool. They use the Prometheus Go SQL collector and have the additional label `db_name="metadata"`. Scraping reads local pool statistics without querying the database. These are client-side statistics, not database server totals.

| Name | Type | Description |
| ---- | ---- | ----------- |
| `juicefs_go_sql_max_open_connections` | Gauge | Maximum number of open connections; 0 means unlimited |
| `juicefs_go_sql_open_connections` | Gauge | Number of open connections, including in-use and idle connections |
| `juicefs_go_sql_in_use_connections` | Gauge | Number of connections currently in use |
| `juicefs_go_sql_idle_connections` | Gauge | Number of idle connections |
| `juicefs_go_sql_wait_count_total` | Counter | Total number of waits for a connection |
| `juicefs_go_sql_wait_duration_seconds_total` | Counter | Total time spent waiting for a connection, in seconds |
| `juicefs_go_sql_max_idle_closed_total` | Counter | Total connections closed because of the idle connection limit |
| `juicefs_go_sql_max_idle_time_closed_total` | Counter | Total connections closed because of the maximum idle time |
| `juicefs_go_sql_max_lifetime_closed_total` | Counter | Total connections closed because of the maximum connection lifetime |

The wait counters help identify pool contention when metadata operations slow down. Counters accumulate over the lifetime of the client's pool; use `rate()` or `increase()` to observe changes over a time window.

## FUSE {#fuse}

### Labels

| Name     | Description                             |
| ----     | -----------                             |
| `method` | FUSE operation (e.g. read, write, lookup) |
| `errno`  | Error name of failed operations (e.g. EIO, ENOENT) |

### Metrics

| Name                                           | Description                          | Unit   |
| ----                                           | -----------                          | ----   |
| `juicefs_fuse_read_size_bytes`                 | Size distributions of read request   | byte   |
| `juicefs_fuse_written_size_bytes`              | Size distributions of write request  | byte   |
| `juicefs_fuse_ops_durations_histogram_seconds` | Operations latency distributions     | second |
| `juicefs_fuse_ops_total`                       | Number of operations                 |        |
| `juicefs_fuse_ops_durations_seconds`           | Accumulated time of operations       | second |
| `juicefs_fuse_ops_io_errors`                   | Number of I/O errors                 |        |
| `juicefs_fuse_open_handlers`                   | Number of open files and directories |        |

## SDK {#sdk}

### Metrics

| Name                                          | Description                         | Unit   |
| ----                                          | -----------                         | ----   |
| `juicefs_sdk_read_size_bytes`                 | Size distributions of read request  | byte   |
| `juicefs_sdk_written_size_bytes`              | Size distributions of write request | byte   |
| `juicefs_sdk_ops_durations_histogram_seconds` | Latency distributions of internal SDK operations (one request may issue many) | second |

## Cache {#cache}

### Metrics

| Name                                    | Description                                 | Unit   |
|:----------------------------------------|---------------------------------------------|--------|
| `juicefs_blockcache_blocks`             | Number of cached blocks                     |        |
| `juicefs_blockcache_bytes`              | Size of cached blocks                       | byte   |
| `juicefs_blockcache_hits`               | Count of cached block hits                  |        |
| `juicefs_blockcache_miss`               | Count of cached block miss                  |        |
| `juicefs_blockcache_writes`             | Count of cached block writes                |        |
| `juicefs_blockcache_drops`              | Count of cached block drops                 |        |
| `juicefs_blockcache_evicts`             | Count of cached block evicts               |        |
| `juicefs_blockcache_hit_bytes`          | Size of cached block hits                   | byte   |
| `juicefs_blockcache_miss_bytes`         | Size of cached block miss                   | byte   |
| `juicefs_blockcache_write_bytes`        | Size of cached block writes                 | byte   |
| `juicefs_blockcache_read_hist_seconds`  | Latency distributions of read cached block  | second |
| `juicefs_blockcache_write_hist_seconds` | Latency distributions of write cached block | second |
| `juicefs_staging_blocks`                | Number of blocks in the staging path        |        |
| `juicefs_staging_block_bytes`           | Total bytes of blocks in the staging path   | byte   |
| `juicefs_staging_block_delay_seconds`   | Total seconds of delay for staging blocks   | second |
| `juicefs_staging_block_errors`          | Count of errors when staging blocks         |        |
| `juicefs_staging_write_bytes`           | Total bytes written to the staging path     | byte   |
| `juicefs_staging_writing_blocks`        | Number of blocks being written to the staging path | |

## Object storage {#object-storage}

### Labels

| Name            | Description                                                    |
| ----            | -----------                                                    |
| `method`        | Method to request object storage (e.g. GET, PUT, HEAD, DELETE) |
| `storage_class` | Storage class of the requested objects, when configured        |

### Metrics

| Name                                                 | Description                                  | Unit   |
| ----                                                 | -----------                                  | ----   |
| `juicefs_object_request_durations_histogram_seconds` | Object storage request latency distributions | second |
| `juicefs_object_request_errors`                      | Count of failed requests to object storage   |        |
| `juicefs_object_request_data_bytes`                  | Size of requests to object storage           | byte   |
| `juicefs_object_request_uploading`                   | Number of on-going upload requests           |        |

## Internal {#internal}

### Metrics

| Name                                   | Description                          | Unit |
|----------------------------------------| -----------                          | ---- |
| `juicefs_compact_size_histogram_bytes` | Size distributions of compacted data | byte |
| `juicefs_used_read_buffer_size_bytes`  | Size of currently used buffer for read | byte |
| `juicefs_used_buffer_size_bytes`       | Size of currently used buffer for write | byte |
| `juicefs_store_cache_size_bytes`       | Size of in-memory store cache        | byte |
| `juicefs_last_successful_backup`       | Unix timestamp of the last successful metadata backup (only exported when `--backup-meta` is enabled) | second |
| `juicefs_last_backup_duration`         | Duration of the last metadata backup (only exported when `--backup-meta` is enabled) | second |
| `juicefs_bgjob_duration_seconds` | Duration of background cleanup jobs (labels: `job`, `status`) | second |
| `juicefs_bgjob_deletions_total` | Total number of deletions performed by background jobs (label: `job`) | |

## Mount configuration {#mount-configuration}

### Metrics

| Name | Description | Unit |
|-|-|-|
| `juicefs_subdir_info` | Indicates the subdir configuration for the current mount (label: `subdir`, value is always `1`) | |

The `subdir` label contains the mounted subdirectory path. An empty string indicates a root mount.

## Data synchronization {#sync}

### Metrics

| Name | Description | Unit |
|-|-|-|
| `juicefs_sync_scanned` | Number of all objects scanned from the source | |
| `juicefs_sync_handled` | Number of objects from the source that have been processed | |
| `juicefs_sync_pending` | Number of objects waiting to be synchronized | |
| `juicefs_sync_copied` | Number of objects that have been synchronized | |
| `juicefs_sync_copied_bytes` | Total size of data that has been synchronized | byte |
| `juicefs_sync_skipped` | Number of objects that skipped during synchronization | |
| `juicefs_sync_skipped_bytes` | Total size of data that skipped during synchronization | byte |
| `juicefs_sync_excluded` | Number of objects excluded by rules during synchronization | |
| `juicefs_sync_excluded_bytes` | Total size of objects excluded by rules during synchronization | byte |
| `juicefs_sync_extra` | Number of extra objects found in the destination | |
| `juicefs_sync_extra_bytes` | Total size of extra objects found in the destination | byte |
| `juicefs_sync_failed` | Number of objects that failed during synchronization | |
| `juicefs_sync_deleted` | Number of objects that deleted during synchronization | |
| `juicefs_sync_checked` | Number of objects that have been verified by checksum during synchronization | |
| `juicefs_sync_checked_bytes` | Total size of data that has been verified by checksum during synchronization | byte |
| `juicefs_sync_listed_prefix` | Number of listed prefixes during synchronization | |
