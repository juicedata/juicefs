# JuiceFS 性能测试

### 基础测试

JuiceFS 提供了 `bench`  子命令来运行一些基本的基准测试，用以评估 JuiceFS 在当前环境的运行情况：

```
$ ./juicefs bench /jfs
Written a big file (1024.00 MiB): (113.67 MiB/s)
Read a big file (1024.00 MiB): (127.12 MiB/s)
Written 100 small files (102.40 KiB): 151.7 files/s, 6.6 ms for each file
Read 100 small files (102.40 KiB): 692.1 files/s, 1.4 ms for each file
Stated 100 files: 584.2 files/s, 1.7 ms for each file
FUSE operation: 19333, avg: 0.3 ms
Update meta: 436, avg: 1.4 ms
Put object: 356, avg: 4.8 ms
Get object first byte: 308, avg: 0.2 ms
Delete object: 356, avg: 0.2 ms
Used: 23.4s, CPU: 69.1%, MEM: 147.0 MiB
```

### 吞吐量

使用 [fio](https://github.com/axboe/fio) 在 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 上执行连续读写测试，结果如下：

[![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)](../images/sequential-read-write-benchmark.svg)

结果表明，JuiceFS 可以提供比另外两个工具大10倍的吞吐量，[了解更多](../en/fio.md)。

### 元数据 IOPS

使用 [mdtest](https://github.com/hpc/ior) 在 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 上执行简易的 mdtest  基准测试，结果如下：

[![Metadata Benchmark](../images/metadata-benchmark.svg)](../images/metadata-benchmark.svg)

结果表明，JuiceFS 可以提供比另外两个工具更高的元数据 IOPS，[了解更多](../en/mdtest.md)。