---
sidebar_label: Performance Statistics Watcher
sidebar_position: 4
slug: /stats_watcher
---
# JuiceFS Performance Statistics Monitor

JuiceFS exposes a lot of [Promethues metrics](../administration/monitoring.md) for monitoring system internal performance. However, when diagnosing performance issues in practice, users may need a more real-time monitoring tool to know what is actually going on within a certain time range. Thus, we provide a command `stats` to print metrics every second, just like what the Linux command `dstat` does. The output is like:

![stats_watcher](../images/juicefs_stats_watcher.png)

By default, this command will print the following metrics of the JuiceFS process corresponding to the given mount point.

#### usage

- cpu: CPU usage of the process
- mem: physical memory used by the process
- buf: current buffer size of JuiceFS, limited by mount option `--buffer-size`

#### fuse

- ops/lat: operations processed by FUSE per second, and their average latency (in milliseconds)
- read/write: read/write bandwidth usage of FUSE

#### meta

- ops/lat: metadata operations processed per second, and their average latency (in milliseconds). Please note that, operations returned directly from cache are not counted in, in order to show a more accurate latency of clients actually interacting with metadata engine.

#### blockcache

- read/write: read/write bandwidth of client local data cache

#### object

- get/put: Get/Put bandwidth between client and object storage

Moreover, users can acquire verbose statistics (like read/write ops and the average latency) by setting `--verbosity 1`, or customize displayed metrics by changing `--schema`. For more information, please check `juicefs stats -h`.
