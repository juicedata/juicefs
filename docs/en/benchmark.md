# Performance Benchmark

## Basic benchmark

JuiceFS provides a subcommand to run a few basic benchmarks to understand how it works in your environment:

![JuiceFS Bench](../images/juicefs-bench.png)

## Throughput

Performed a sequential read/write benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio), here is the result:

[![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)](../images/sequential-read-write-benchmark.svg)

It shows JuiceFS can provide 10X more throughput than the other two, read [more details](fio.md).

## Metadata IOPS

Performed a simple mdtest benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior), here is the result:

[![Metadata Benchmark](../images/metadata-benchmark.svg)](../images/metadata-benchmark.svg)

It shows JuiceFS can provide significantly more metadata IOPS than the other two, read [more details](../en/mdtest.md).

## Analyze performance

There is a virtual file called `.accesslog` in the root of JuiceFS to show all the operations and the time they takes, for example:

```
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

The last number on each line is the time (in seconds) current operation takes. You can use this directly to debug and analyze performance issues, or try `./juicefs profile /jfs` to monitor real time statistics. Please run `./juicefs profile -h` or refer to [here](operations_profiling.md) to learn more about this subcommand.
