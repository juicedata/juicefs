# 性能统计监控

JuiceFS 预定义了许多监控指标来监测系统运行时的内部性能情况，并通过 Prometheus API [暴露对外接口](./p8s_metrics.md)。然而， 在分析一些实际问题时，用户往往需要更实时的性能统计监控。因此，我们开发了 `stats` 命令，以类似 Linux `dstat` 工具的形式实时打印各个指标的每秒变化情况，如下图所示：

![stats_watcher](../images/juicefs_stats_watcher.png)

默认参数下，此命令会监控指定挂载点对应的 JuiceFS 进程的以下几个指标：

#### usage

- cpu：进程的 CPU 使用率
- mem：进程的物理内存使用量
- buf：进程已使用的 Buffer 大小；此值受限于 mount 参数 `--buffer-size`

#### fuse

- ops/lat：通过 FUSE 接口处理的每秒请求数及其平均时延
- read/write：通过 FUSE 接口处理的读写带宽

#### meta

- ops/lat：每秒处理的元数据请求数和平均时延；注意部分能在缓存中直接处理的元数据请求未列入统计，以更好地体现客户端与元数据引擎交互的耗时

#### blockcache

- read/write：客户端本地数据缓存的每秒读写流量

#### object

- get/put：客户端与对象存储交互的 Get/Put 每秒流量

此外，可以通过设置 `--verbosity 1` 来获取更详细的统计信息（如读写请求的个数和平均时延统计等），也可以通过修改 `--schema` 来自定义监控内容与格式。更多的命令信息请通过执行 `juicefs stats -h` 查看。
