---
title: 常规测试
sidebar_position: 1
slug: .
description: 本文介绍使用 FIO、mdtest 以及 JuiceFS 自带的 bench 命令对文件系统进行性能测试。
---

本章介绍的测试中使用 [Redis](https://redis.io) 作为元数据存储引擎。在该测试条件下，JuiceFS 拥有十倍于 Amazon [EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 的性能表现。

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

如遇性能问题，阅读[「实时性能监控」](../administration/fault_diagnosis_and_analysis.md#performance-monitor)了解如何排查。
