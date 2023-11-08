---
title: JuiceFS Metrics
sidebar_position: 4
---

If you haven't yet set up monitoring for JuiceFS, read [monitoring and data visualization"](../administration/monitoring.md) to learn how.

## Global labels

| Name       | Description      |
| ----       | -----------      |
| `vol_name` | Volume name      |
| `instance` | Client host name in format `<host>:<port>`. Refer to [official document](https://prometheus.io/docs/concepts/jobs_instances) for more information |
| `mp`       | Mount point path, if metrics are reported through [Prometheus Pushgateway](https://github.com/prometheus/pushgateway), for example, [JuiceFS Hadoop Java SDK](../administration/monitoring.md#hadoop), `mp` will be `sdk-<PID>` |

## File system

### Metrics

| Name                          | Description                            | Unit |
|-------------------------------|----------------------------------------|------|
| `juicefs_used_space`          | Total used space                       | byte |
| `juicefs_used_inodes`         | Total number of inodes                 |      |

## Operating system

### Metrics

| Name                | Description           | Unit   |
| ----                | -----------           | ----   |
| `juicefs_uptime`    | Total running time    | second |
| `juicefs_cpu_usage` | Accumulated CPU usage | second |
| `juicefs_memory`    | Used memory           | byte   |

## Metadata engine

### Metrics

| Name                                              | Description                                | Unit   |
| ----                                              | -----------                                | ----   |
| `juicefs_transaction_durations_histogram_seconds` | Transactions latency distributions         | second |
| `juicefs_transaction_restart`                     | Number of times a transaction restarted |        |

## FUSE

### Metrics

| Name                                           | Description                          | Unit   |
| ----                                           | -----------                          | ----   |
| `juicefs_fuse_read_size_bytes`                 | Size distributions of read request   | byte   |
| `juicefs_fuse_written_size_bytes`              | Size distributions of write request  | byte   |
| `juicefs_fuse_ops_durations_histogram_seconds` | Operations latency distributions     | second |
| `juicefs_fuse_open_handlers`                   | Number of open files and directories |        |

## SDK

### Metrics

| Name                                          | Description                         | Unit   |
| ----                                          | -----------                         | ----   |
| `juicefs_sdk_read_size_bytes`                 | Size distributions of read request  | byte   |
| `juicefs_sdk_written_size_bytes`              | Size distributions of write request | byte   |
| `juicefs_sdk_ops_durations_histogram_seconds` | Operations latency distributions    | second |

## Cache

### Metrics

| Name                                    | Description                                 | Unit   |
|:----------------------------------------|---------------------------------------------|--------|
| `juicefs_blockcache_blocks`             | Number of cached blocks                     |        |
| `juicefs_blockcache_bytes`              | Size of cached blocks                       | byte   |
| `juicefs_blockcache_hits`               | Count of cached block hits                  |        |
| `juicefs_blockcache_miss`               | Count of cached block miss                  |        |
| `juicefs_blockcache_writes`             | Count of cached block writes                |        |
| `juicefs_blockcache_drops`              | Count of cached block drops                 |        |
| `juicefs_blockcache_evicts`             | Count of cached block evicts                |        |
| `juicefs_blockcache_hit_bytes`          | Size of cached block hits                   | byte   |
| `juicefs_blockcache_miss_bytes`         | Size of cached block miss                   | byte   |
| `juicefs_blockcache_write_bytes`        | Size of cached block writes                 | byte   |
| `juicefs_blockcache_read_hist_seconds`  | Latency distributions of read cached block  | second |
| `juicefs_blockcache_write_hist_seconds` | Latency distributions of write cached block | second |
| `juicefs_staging_blocks`                | Number of blocks in the staging path        |        |
| `juicefs_staging_block_bytes`           | Total bytes of blocks in the staging path   | byte   |
| `juicefs_staging_block_delay_seconds`   | Total seconds of delay for staging blocks   | second |

## Object storage

### Labels

| Name     | Description                                                    |
| ----     | -----------                                                    |
| `method` | Method to request object storage (e.g. GET, PUT, HEAD, DELETE) |

### Metrics

| Name                                                 | Description                                  | Unit   |
| ----                                                 | -----------                                  | ----   |
| `juicefs_object_request_durations_histogram_seconds` | Object storage request latency distributions | second |
| `juicefs_object_request_errors`                      | Count of failed requests to object storage   |        |
| `juicefs_object_request_data_bytes`                  | Size of requests to object storage           | byte   |

## Internal

### Metrics

| Name                                   | Description                          | Unit |
|----------------------------------------| -----------                          | ---- |
| `juicefs_compact_size_histogram_bytes` | Size distributions of compacted data | byte |
| `juicefs_used_read_buffer_size_bytes`  | size of currently used buffer for read |      |
