---
sidebar_label: JuiceFS Metrics
sidebar_position: 2
slug: /p8s_metrics
---
# JuiceFS Metrics

JuiceFS provides a [Prometheus](https://prometheus.io) API for each file system. The default API address is `http://localhost:9567/metrics`, you could custom the address through `--metrics` option when execute [`juicefs mount`](../reference/command_reference.md#juicefs-mount) or [`juicefs gateway`](../reference/command_reference.md#juicefs-gateway) command.

JuiceFS also provides a [dashboard template](grafana_template.json) for [Grafana](https://grafana.com), which can be
imported to show the collected metrics in Prometheus.

## Use Consul as registration center

JuiceFS support use Consul as registration center for metrics API. You could custom the address through `--consul` option when execute [`juicefs mount`](../reference/command_reference.md#juicefs-mount) or [`juicefs gateway`](../reference/command_reference.md#juicefs-gateway) command.

When the Consul address is configured, the `--metrics` option does not need to be configured. JuiceFS will automatically configure metrics URL according to its own network and port conditions. If `--metrics` is set at the same time, it will first try to listen on the configured metrics URL.

For each instance registered to Consul, its `serviceName` is `juicefs`, and the format of `serviceId` is `<IP>:<mount-point>`, for example: `127.0.0.1:/tmp/jfs`.

The meta of each instance contains two aspects: `hostname` and `mountpoint`. When `mountpoint` is `s3gateway`, which means that the instance is an S3 gateway.

Below are descriptions of each metrics.

## Global labels

| Name       | Description      |
| ----       | -----------      |
| `vol_name` | Volume name      |
| `mp`       | Mount point path |

> **Tip**: When Prometheus scrapes a target, it attaches `instance` label automatically to the scraped time series which serve to identify the scraped target, its format is `<host>:<port>`. Refer to [official document](https://prometheus.io/docs/concepts/jobs_instances) for more information.

> **Tip**: If the monitoring metrics are reported through [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) (for example, [JuiceFS Hadoop Java SDK](hadoop_java_sdk.md#monitoring-metrics-collection)), the value of the `mp` label is `sdk-<PID>`, and the value of the `instance` label is the host name.

## File system

### Metrics

| Name                  | Description            | Unit |
| ----                  | -----------            | ---- |
| `juicefs_used_space`  | Total used space       | byte |
| `juicefs_used_inodes` | Total number of inodes |      |

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
| `juicefs_transaction_restart`                     | Number of times a transaction is restarted |        |

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
| ----                                    | -----------                                 | ----   |
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

## Object storage

### Labels

| Name     | Description                                                    |
| ----     | -----------                                                    |
| `method` | Request method to object storage (e.g. GET, PUT, HEAD, DELETE) |

### Metrics

| Name                                                 | Description                                  | Unit   |
| ----                                                 | -----------                                  | ----   |
| `juicefs_object_request_durations_histogram_seconds` | Object storage request latency distributions | second |
| `juicefs_object_request_errors`                      | Count of failed requests to object storage   |        |
| `juicefs_object_request_data_bytes`                  | Size of requests to object storage           | byte   |

## Internal

### Metrics

| Name                                   | Description                          | Unit |
| ----                                   | -----------                          | ---- |
| `juicefs_compact_size_histogram_bytes` | Size distributions of compacted data | byte |
