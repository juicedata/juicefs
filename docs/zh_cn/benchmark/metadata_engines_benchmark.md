---
sidebar_label: 元数据引擎性能测试
sidebar_position: 6
slug: /metadata_engines_benchmark
---

# 元数据引擎性能对比测试

首先展示结论：

- 对于纯元数据操作，MySQL 耗时约为 Redis 的 2～4 倍；TiKV 性能与 MySQL 接近，大部分场景下略优于 MySQL；etcd 的耗时约为 TiKV 的 1.5 倍
- 对于小 IO（～100 KiB）压力，使用 MySQL 引擎的操作总耗时大约是使用 Redis 引擎总耗时的 1～3 倍；TiKV 和 etcd 的耗时与 MySQL 接近
- 对于大 IO（～4 MiB）压力，使用不同元数据引擎的总耗时未见明显差异（此时对象存储成为瓶颈）

:::note 注意
1. Redis 可以通过将 `appendfsync` 配置项由 `always` 改为 `everysec`，牺牲少量可靠性来换取一定的性能提升。更多信息可参见[这里](https://redis.io/docs/manual/persistence)。
2. 测试中 Redis 和 MySQL 数据均仅在本地存储单副本，TiKV 和 etcd 数据会在三个节点间通过 Raft 协议存储三副本。
:::

以下提供了测试的具体细节。这些测试都运行在相同的对象存储（用来存放数据）、客户端和元数据节点上，只有元数据引擎不同。

## 测试环境

### JuiceFS 版本

1.0.0-dev+2022-04-07.50fc234e

### 对象存储

Amazon S3

### 客户端节点

- Amazon c5.xlarge：4 vCPUs，8 GiB 内存，最高 10 Gigabit 网络
- Ubuntu 18.04.4 LTS

### 元数据节点

- Amazon c5d.xlarge：4 vCPUs，8 GiB 内存，最高 10 Gigabit 网络，100 GB SSD（为元数据引擎提供本地存储）
- Ubuntu 20.04.1 LTS
- SSD 数据盘被格式化为 ext4 文件系统并挂载到 `/data` 目录

### 元数据引擎

#### Redis

- 版本：[6.2.6](https://download.redis.io/releases/redis-6.2.6.tar.gz)
- 配置：
  - `appendonly`：`yes`
  - `appendfsync`：分别测试了 `always` 和 `everysec`
  - `dir`：`/data/redis`

#### MySQL

- 版本：8.0.25
- `/var/lib/mysql` 目录被绑定挂载到 `/data/mysql`

#### TiKV

- 版本：5.4.0
- 配置：
  - `deploy_dir`：`/data/tikv-deploy`
  - `data_dir`：`/data/tikv-data`

#### etcd

- 版本：3.5.2
- 配置：
  - `data-dir`：`/data/etcd`

## 测试工具

每种元数据引擎都会运行以下所有测试。

### Golang Benchmark

在源码中提供了简单的元数据基准测试：[`pkg/meta/benchmarks_test.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/benchmarks_test.go)

### JuiceFS Bench

JuiceFS 提供了一个基础的性能测试命令：

```bash
juicefs bench /mnt/jfs -p 4
```

### mdtest

- 版本：mdtest-3.4.0+dev

在 3 个客户端节点上并发执行测试：

```bash
$ cat myhost
client1 slots=4
client2 slots=4
client3 slots=4
```

测试命令：

meta only
```shell
mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -u -d /mnt/jfs
```
12000 * 100KiB files
```shell
mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -u -d /mnt/jfs
```

### fio

- 版本：fio-3.1

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## 测试结果

### Golang Benchmark

- 展示了操作耗时（单位为 微秒/op），数值越小越好
- 括号内数字是该指标对比 Redis-Always 的倍数（`always` 和 `everysec` 均是 Redis 配置项 `appendfsync` 的可选值）
- 由于元数据缓存缘故，目前 `Read` 接口测试数据均小于 1 微秒，暂无对比意义

|              | Redis-Always | Redis-Everysec | MySQL        | TiKV       | etcd         |
|--------------|--------------|----------------|--------------|------------|--------------|
| mkdir        | 600          | 471 (0.8)      | 2121 (3.5)   | 1614 (2.7) | 2203 (3.7)   |
| mvdir        | 878          | 756 (0.9)      | 3372 (3.8)   | 1854 (2.1) | 3000 (3.4)   |
| rmdir        | 785          | 673 (0.9)      | 3065 (3.9)   | 2097 (2.7) | 3634 (4.6)   |
| readdir_10   | 302          | 303 (1.0)      | 1011 (3.3)   | 1232 (4.1) | 2171 (7.2)   |
| readdir_1k   | 1668         | 1838 (1.1)     | 16824 (10.1) | 6682 (4.0) | 17470 (10.5) |
| mknod        | 584          | 498 (0.9)      | 2117 (3.6)   | 1561 (2.7) | 2232 (3.8)   |
| create       | 591          | 468 (0.8)      | 2120 (3.6)   | 1565 (2.6) | 2206 (3.7)   |
| rename       | 860          | 736 (0.9)      | 3391 (3.9)   | 1799 (2.1) | 2941 (3.4)   |
| unlink       | 709          | 580 (0.8)      | 3052 (4.3)   | 1881 (2.7) | 3080 (4.3)   |
| lookup       | 99           | 97 (1.0)       | 423 (4.3)    | 731 (7.4)  | 1286 (13.0)  |
| getattr      | 91           | 89 (1.0)       | 343 (3.8)    | 371 (4.1)  | 661 (7.3)    |
| setattr      | 501          | 357 (0.7)      | 1258 (2.5)   | 1358 (2.7) | 1480 (3.0)   |
| access       | 90           | 89 (1.0)       | 348 (3.9)    | 370 (4.1)  | 646 (7.2)    |
| setxattr     | 404          | 270 (0.7)      | 1152 (2.9)   | 1116 (2.8) | 757 (1.9)    |
| getxattr     | 91           | 89 (1.0)       | 298 (3.3)    | 365 (4.0)  | 655 (7.2)    |
| removexattr  | 219          | 95 (0.4)       | 882 (4.0)    | 1554 (7.1) | 1461 (6.7)   |
| listxattr_1  | 88           | 88 (1.0)       | 312 (3.5)    | 374 (4.2)  | 658 (7.5)    |
| listxattr_10 | 94           | 91 (1.0)       | 397 (4.2)    | 390 (4.1)  | 694 (7.4)    |
| link         | 605          | 461 (0.8)      | 2436 (4.0)   | 1627 (2.7) | 2237 (3.7)   |
| symlink      | 602          | 465 (0.8)      | 2394 (4.0)   | 1633 (2.7) | 2244 (3.7)   |
| write        | 613          | 371 (0.6)      | 2565 (4.2)   | 1905 (3.1) | 2350 (3.8)   |
| read_1       | 0            | 0 (0.0)        | 0 (0.0)      | 0 (0.0)    | 0 (0.0)      |
| read_10      | 0            | 0 (0.0)        | 0 (0.0)      | 0 (0.0)    | 0 (0.0)      |

### JuiceFS Bench

|                  | Redis-Always     | Redis-Everysec   | MySQL           | TiKV            | etcd            |
| ---------------- | ---------------- | ---------------- | --------------- | --------------- | --------------- |
| Write big file   | 565.07 MiB/s     | 556.92 MiB/s     | 557.93 MiB/s    | 553.58 MiB/s    | 542.93 MiB/s    |
| Read big file    | 664.82 MiB/s     | 652.18 MiB/s     | 673.55 MiB/s    | 679.07 MiB/s    | 672.91 MiB/s    |
| Write small file | 102.30 files/s   | 105.80 files/s   | 87.20 files/s   | 95.00 files/s   | 95.75 files/s   |
| Read small file  | 2200.30 files/s  | 1894.45 files/s  | 1360.85 files/s | 1394.90 files/s | 1017.30 files/s |
| Stat file        | 11607.40 files/s | 15032.90 files/s | 5470.05 files/s | 3283.20 files/s | 2827.80 files/s |
| FUSE operation   | 0.41 ms/op       | 0.42 ms/op       | 0.46 ms/op      | 0.45 ms/op      | 0.42 ms/op      |
| Update meta      | 3.63 ms/op       | 3.19 ms/op       | 8.91 ms/op      | 7.04 ms/op      | 4.46 ms/op      |

### mdtest

- 展示了操作速率（每秒 OPS 数），数值越大越好

|                    | Redis-Always | Redis-Everysec | MySQL     | TiKV      | etcd     |
|--------------------|--------------|----------------|-----------|-----------|----------|
| **EMPTY FILES**    |              |                |           |           |          |
| Directory creation | 5322.061     | 10182.743      | 1622.571  | 3134.935  | 2316.622 |
| Directory stat     | 302016.015   | 261650.268     | 14359.378 | 22584.101 | 9186.274 |
| Directory removal  | 5268.779     | 10663.498      | 1299.126  | 2511.035  | 1668.792 |
| File creation      | 5277.414     | 10043.012      | 1647.383  | 3062.820  | 2305.468 |
| File stat          | 300142.547   | 349101.889     | 16166.343 | 22464.020 | 9334.466 |
| File read          | 45753.419    | 47342.346      | 13502.136 | 15163.344 | 9378.590 |
| File removal       | 4172.033     | 11076.660      | 1148.675  | 2316.635  | 1457.711 |
| Tree creation      | 80.353       | 84.677         | 43.656    | 41.390    | 59.275   |
| Tree removal       | 110.291      | 118.374        | 48.283    | 72.240    | 60.040   |
| **SMALL FILES**    |              |                |           |           |          |
| File creation      | 314.787      | 320.041        | 307.489   | 293.323   | 293.029  |
| File stat          | 57502.060    | 56546.511      | 14096.102 | 11517.863 | 7432.247 |
| File read          | 46251.763    | 47537.783      | 17030.913 | 14345.960 | 4890.890 |
| File removal       | 3615.253     | 7808.427       | 898.631   | 1884.315  | 1228.742 |
| Tree creation      | 53.523       | 51.871         | 25.276    | 36.511    | 24.960   |
| Tree removal       | 62.676       | 53.384         | 25.782    | 22.074    | 13.652   |


### fio

|                 | Redis-Always | Redis-Everysec | MySQL     | TiKV      | etcd      |
|-----------------|--------------|----------------|-----------|-----------|-----------|
| Write bandwidth | 555 MiB/s    | 532 MiB/s      | 553 MiB/s | 537 MiB/s | 555 MiB/s |
