---
sidebar_label: 常规测试
sidebar_position: 1
slug: .
---
# JuiceFS 常规测试

### 基础测试

JuiceFS 提供了 `bench`  子命令来运行一些基本的基准测试，用以评估 JuiceFS 在当前环境的运行情况：

![JuiceFS Bench](../images/juicefs-bench.png)

### 吞吐量

使用 [fio](https://github.com/axboe/fio) 在 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 上执行连续读写测试，结果如下：

[![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)](../images/sequential-read-write-benchmark.svg)

结果表明，JuiceFS 可以提供比另外两个工具大 10 倍的吞吐量，[了解更多](fio.md)。

### 元数据 IOPS

使用 [mdtest](https://github.com/hpc/ior) 在 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 上执行简易的 mdtest  基准测试，结果如下：

[![Metadata Benchmark](../images/metadata-benchmark.svg)](../images/metadata-benchmark.svg)

结果表明，JuiceFS 可以提供比另外两个工具更高的元数据 IOPS，[了解更多](mdtest.md)。

### 分析测试结果

假定在 JuiceFS 的根目录下有一个名为 `.accesslog` 的文件，它保存了所有操作对应的时间，例如：

```shell
cat /jfs/.accesslog
```

```output
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

每行最后一个数表示当前操作所消耗的时间（单位：秒）。你可以直接参考这些数值来调试和分析性能问题，也可以试试 `./juicefs profile /jfs` 命令来实时监测性能统计数据。你也可以运行 `./juicefs profile -h` 或者参考[这里](../benchmark/operations_profiling.md)了解这个子命令。
