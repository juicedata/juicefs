---
title: Metadata Engines Benchmark
sidebar_position: 6
slug: /metadata_engines_benchmark
description: This article describes how to test and evaluate the performance of various metadata engines for JuiceFS using a real-world environment.
---

Conclusion first:

- For pure metadata operations, MySQL costs about 2~4x times of Redis; TiKV has similar performance to MySQL, and in most cases it costs a bit less; etcd costs about 1.5x times of TiKV.
- For small I/O (~100 KiB) workloads, total time costs with MySQL are about 1~3x of those with Redis; TiKV and etcd performs similarly to MySQL.
- For large I/O (~4 MiB) workloads, total time costs with different metadata engines show no significant difference (object storage becomes the bottleneck).

:::note

1. By changing `appendfsync` from `always` to `everysec`, Redis gains performance boost but loses a bit of data reliability. More information can be found [here](https://redis.io/docs/manual/persistence).
2. Both Redis and MySQL store only one replica locally, while TiKV and etcd stores three replicas on three different hosts using Raft protocol.
:::

Details are provided below. Please note all the tests are run with the same object storage (to save data), clients and metadata hosts, only metadata engines differ.

## Environment

### JuiceFS Version

1.1.0-beta1+2023-06-08.5ef17ba0

### Object Storage

Amazon S3

### Client Hosts

- Amazon c5.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network
- Ubuntu 20.04.1 LTS

### Metadata Hosts

- Amazon c5d.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network, 100 GB SSD (local storage for metadata engines)
- Ubuntu 20.04.1 LTS
- SSD is formatted as ext4 and mounted on `/data`

### Metadata Engines

#### Redis

- Version: [7.0.9](https://download.redis.io/releases/redis-7.0.9.tar.gz)
- Configuration:
  - `appendonly`: `yes`
  - `appendfsync`: `always` or `everysec`
  - `dir`: `/data/redis`

#### MySQL

- Version: 8.0.25
- `/var/lib/mysql` is bind mounted on `/data/mysql`

#### TiKV

- Version: 6.5.3
- Configuration:
  - `deploy_dir`: `/data/tikv-deploy`
  - `data_dir`: `/data/tikv-data`

#### etcd

- Version: 3.3.25
- Configuration:
  - `data-dir`: `/data/etcd`

#### FoundationDB

- Version：6.3.23
- Configuration：
  - `data-dir`：`/data/fdb`

## Tools

All the following tests are run for each metadata engine.

### Golang Benchmark

Simple benchmarks within the source code: [`pkg/meta/benchmarks_test.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/benchmarks_test.go)

### JuiceFS Bench

JuiceFS provides a basic benchmark command:

```bash
./juicefs bench /mnt/jfs -p 4
```

### mdtest

- Version: mdtest-3.3.0

Run parallel tests on 3 client nodes:

```bash
$ cat myhost
client1 slots=4
client2 slots=4
client3 slots=4
```

Test commands:

```bash
# metadata only
mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -u -d /mnt/jfs

# 12000 * 100KiB files
mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -u -d /mnt/jfs
```

### fio

- Version: fio-3.28

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## Results

### Golang Benchmark

- Shows time cost (us/op). Smaller is better.
- Number in parentheses is the multiple of Redis-Always cost (`always` and `everysec` are candidates for Redis configuration `appendfsync`).
- Because of enabling metadata cache, the results of `read` are all less than 1us, which are not comparable for now.

|              | Redis-Always | Redis-Everysec | TiKV       | MySQL        | etcd         | FoundationDB  |
|--------------|--------------|----------------|------------|--------------|--------------|---------------|
| mkdir        | 558          | 468 (0.8)      | 1237 (2.2) | 2042 (3.7)   | 1916 (3.4)   | 1847 (3.3)    |
| mvdir        | 693          | 621 (0.9)      | 1414 (2.0) | 2693 (3.9)   | 2486 (3.6)   | 2115 (3.1)    |
| rmdir        | 717          | 648 (0.9)      | 1641 (2.3) | 3050 (4.3)   | 2980 (4.2)   | 2278 (3.2)    |
| readdir_10   | 280          | 288 (1.0)      | 995 (3.6)  | 1350 (4.8)   | 1757 (6.3)   | 3179 (11.4)   |
| readdir_1k   | 1490         | 1547 (1.0)     | 5834 (3.9) | 18779 (12.6) | 15809 (10.6) | 143025 (96.0) |
| mknod        | 562          | 464 (0.8)      | 1211 (2.2) | 1547 (2.8)   | 1838 (3.3)   | 1849 (3.3)    |
| create       | 570          | 455 (0.8)      | 1209 (2.1) | 1570 (2.8)   | 1849 (3.2)   | 1897 (3.3)    |
| rename       | 728          | 627 (0.9)      | 1419 (1.9) | 2735 (3.8)   | 2445 (3.4)   | 2197 (3.0)    |
| unlink       | 658          | 567 (0.9)      | 1443 (2.2) | 2365 (3.6)   | 2461 (3.7)   | 2146 (3.3)    |
| lookup       | 173          | 178 (1.0)      | 608 (3.5)  | 557 (3.2)    | 1054 (6.1)   | 1067 (6.2)    |
| getattr      | 87           | 86 (1.0)       | 306 (3.5)  | 530 (6.1)    | 536 (6.2)    | 528 (6.1)     |
| setattr      | 471          | 345 (0.7)      | 1001 (2.1) | 1029 (2.2)   | 1279 (2.7)   | 1629 (3.5)    |
| access       | 87           | 89 (1.0)       | 307 (3.5)  | 518 (6.0)    | 534 (6.1)    | 531 (6.1)     |
| setxattr     | 393          | 262 (0.7)      | 800 (2.0)  | 992 (2.5)    | 717 (1.8)    | 1293 (3.3)    |
| getxattr     | 84           | 87 (1.0)       | 303 (3.6)  | 494 (5.9)    | 529 (6.3)    | 523 (6.2)     |
| removexattr  | 215          | 96 (0.4)       | 1007 (4.7) | 697 (3.2)    | 1336 (6.2)   | 1668 (7.8)    |
| listxattr_1  | 85           | 87 (1.0)       | 303 (3.6)  | 516 (6.1)    | 531 (6.2)    | 547 (6.4)     |
| listxattr_10 | 87           | 91 (1.0)       | 322 (3.7)  | 561 (6.4)    | 565 (6.5)    | 540 (6.2)     |
| link         | 680          | 545 (0.8)      | 1732 (2.5) | 2435 (3.6)   | 3058 (4.5)   | 2663 (3.9)    |
| symlink      | 580          | 448 (0.8)      | 1224 (2.1) | 1785 (3.1)   | 1897 (3.3)   | 1833 (3.2)    |
| newchunk     | 0            | 0 (0.0)        | 1 (0.0)    | 1 (0.0)      | 1 (0.0)      | 2 (0.0)       |
| write        | 553          | 369 (0.7)      | 1573 (2.8) | 2352 (4.3)   | 1788 (3.2)   | 1940 (3.5)    |
| read_1       | 0            | 0 (0.0)        | 0 (0.0)    | 0 (0.0)      | 0 (0.0)      | 0 (0.0)       |
| read_10      | 0            | 0 (0.0)        | 0 (0.0)    | 0 (0.0)      | 0 (0.0)      | 0 (0.0)       |

### JuiceFS Bench

|                  | Redis-Always     | Redis-Everysec   | TiKV            | MySQL           | etcd            | FoundationDB    |
|------------------|------------------|------------------|-----------------|-----------------|-----------------|-----------------|
| Write big file   | 730.84 MiB/s     | 731.93 MiB/s     | 730.01 MiB/s    | 729.00 MiB/s    | 746.07 MiB/s    | 744.70 MiB/s    |
| Read big file    | 923.98 MiB/s     | 892.99 MiB/s     | 918.19 MiB/s    | 905.93 MiB/s    | 939.63 MiB/s    | 948.81 MiB/s    |
| Write small file | 95.20 files/s    | 109.10 files/s   | 101.20 files/s  | 82.30 files/s   | 95.80 files/s   | 94.60 files/s   |
| Read small file  | 1242.80 files/s  | 937.30 files/s   | 681.50 files/s  | 752.40 files/s  | 1229.10 files/s | 1301.40 files/s |
| Stat file        | 12313.80 files/s | 11989.50 files/s | 4211.20 files/s | 3583.10 files/s | 2836.60 files/s | 3400.00 files/s |
| FUSE operation   | 0.41 ms/op       | 0.40 ms/op       | 0.41 ms/op      | 0.46 ms/op      | 0.41 ms/op      | 0.44 ms/op      |
| Update meta      | 2.45 ms/op       | 1.76 ms/op       | 3.76 ms/op      | 2.46 ms/op      | 3.40 ms/op      | 2.87 ms/op      |

### mdtest

- Shows rate (ops/sec). Bigger is better.

|                    | Redis-Always | Redis-Everysec | TiKV      | MySQL    | etcd     | FoundationDB |
|--------------------|--------------|----------------|-----------|----------|----------|--------------|
| **EMPTY FILES**    |              |                |           |          |          |              |
| Directory creation | 4901.342     | 9990.029       | 4041.304  | 1252.421 | 1910.768 | 3065.578     |
| Directory stat     | 289992.466   | 379692.576     | 49465.223 | 9359.278 | 6500.178 | 17746.670    |
| Directory removal  | 5131.614     | 10356.293      | 3210.518  | 902.077  | 1450.842 | 2460.604     |
| File creation      | 5472.628     | 9984.824       | 4053.610  | 1326.613 | 1801.956 | 2908.526     |
| File stat          | 288951.216   | 253218.558     | 50432.658 | 9135.571 | 6276.787 | 14939.411    |
| File read          | 64560.148    | 60861.397      | 18411.280 | 8445.953 | 9094.627 | 11087.931    |
| File removal       | 6084.791     | 12221.083      | 3742.269  | 1073.063 | 1648.734 | 2214.311     |
| Tree creation      | 80.121       | 83.546         | 77.875    | 34.420   | 56.299   | 74.982       |
| Tree removal       | 218.535      | 95.599         | 114.414   | 42.330   | 76.002   | 64.036       |
| **SMALL FILES**    |              |                |           |          |          |              |
| File creation      | 295.067      | 312.182        | 307.121   | 275.588  | 275.578  | 263.487      |
| File stat          | 54069.827    | 52800.108      | 14076.214 | 8760.709 | 8214.318 | 10009.670    |
| File read          | 62341.568    | 57998.398      | 23376.733 | 4639.571 | 5477.754 | 6533.787     |
| File removal       | 5615.018     | 11573.415      | 3411.663  | 1061.600 | 1024.421 | 1750.613     |
| Tree creation      | 57.860       | 57.080         | 44.590    | 23.723   | 19.998   | 11.243       |
| Tree removal       | 96.756       | 65.279         | 27.616    | 23.227   | 17.868   | 10.571       |

### fio

|                 | Redis-Always | Redis-Everysec | MySQL     | TiKV      | etcd      | FoundationDB |
|-----------------|--------------|----------------|-----------|-----------|-----------|--------------|
| Write bandwidth | 729 MiB/s    | 737 MiB/s      | 736 MiB/s | 731 MiB/s | 738 MiB/s | 745 MiB/s    |
