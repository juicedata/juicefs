# 元数据引擎性能对比测试

首先展示结论：

- 对于纯元数据操作，MySQL 耗时约为 Redis 的 2～4 倍；TiKV 性能与 MySQL 接近，大部分场景下略优于 MySQL
- 对于小 IO（～100 KiB）压力，使用 MySQL 引擎的操作总耗时大约是使用 Redis 引擎总耗时的 1～3 倍；TiKV 耗时与 MySQL 接近
- 对于大 IO（～4 MiB）压力，使用不同元数据引擎的总耗时未见明显差异（此时对象存储成为瓶颈）

> **注意**：
>
> 1. Redis 可以通过将 `appendfsync` 配置项由 `always` 改为 `everysec`，牺牲少量可靠性来换取一定的性能提升；更多信息可参见[这里](https://redis.io/topics/persistence)
> 2. 测试中 Redis 和 MySQL 数据均仅在本地存储单副本，TiKV 数据会在三个节点间通过 Raft 协议存储三副本

以下提供了测试的具体细节。这些测试都运行在相同的对象存储（用来存放数据），客户端和元数据节点上；只有元数据引擎不同。

## 测试环境

### JuiceFS 版本

juicefs version 0.16-dev (2021-07-20 9efa870)

### 对象存储

Amazon S3

### 客户端节点

- Amazon c5.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network
- Ubuntu 18.04.4 LTS

### 元数据节点

- Amazon c5d.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network, 100 GB SSD（为元数据引擎提供本地存储）
- Ubuntu 18.04.4 LTS
- SSD 数据盘被格式化为 ext4 文件系统并挂载到 `/data` 目录

### 元数据引擎

#### Redis

- 版本: [6.2.3](https://download.redis.io/releases/redis-6.2.3.tar.gz)
- 配置:
  - appendonly: yes
  - appendfsync: 分别测试了 always 和 everysec
  - dir: `/data/redis`

#### MySQL

- 版本: 8.0.25
- `/var/lib/mysql` 目录被绑定挂载到 `/data/mysql`

### TiKV

- 版本: 5.1.0
- 配置:
  - deploy_dir: `/data/tikv-deploy`
  - data_dir: `/data/tikv-data`

## 测试工具

每种元数据引擎都会运行以下所有测试。

### Golang Benchmark

在源码中提供了简单的元数据基准测试: `pkg/meta/benchmarks_test.go`。

### JuiceFS Bench

JuiceFS 提供了一个基础的性能测试命令：

```bash
$ ./juicefs bench /mnt/jfs
```

### mdtest

- 版本: mdtest-3.4.0+dev

在3个客户端节点上并发执行测试：

```bash
$ cat myhost
client1 slots=4
client2 slots=4
client3 slots=4
```

测试命令:

```bash
# meta only
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -u -d /mnt/jfs

# 12000 * 100KiB files
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -u -d /mnt/jfs
```

### fio

- 版本: fio-3.1

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## 测试结果

### Golang Benchmark

- 展示了操作耗时（单位为 微秒/op），数值越小越好
- 括号内数字是该指标对比 Redis-Always 的倍数（`always` 和 `everysec` 均是 Redis 配置项 `appendfsync` 的可选值）
- 由于元数据缓存缘故，目前 `Read` 接口测试数据均小于 1 微秒，暂无对比意义

|              | Redis-Always | Redis-Everysec | MySQL | TiKV |
| ------------ | ------------ | -------------- | ----- | ---- |
| mkdir | 986 | 700 (0.7) | 2274 (2.3) | 1961 (2.0) |
| mvdir | 1116 | 940 (0.8) | 3690 (3.3) | 2145 (1.9) |
| rmdir | 981 | 817 (0.8) | 2980 (3.0) | 2300 (2.3) |
| readdir_10 | 376 | 378 (1.0) | 1365 (3.6) | 965 (2.6) |
| readdir_1k | 1804 | 1819 (1.0) | 15449 (8.6) | 6776 (3.8) |
| mknod | 968 | 665 (0.7) | 2325 (2.4) | 1997 (2.1) |
| create | 957 | 703 (0.7) | 2291 (2.4) | 1971 (2.1) |
| rename | 1082 | 1040 (1.0) | 3701 (3.4) | 2162 (2.0) |
| unlink | 842 | 710 (0.8) | 3293 (3.9) | 2217 (2.6) |
| lookup | 118 | 127 (1.1) | 409 (3.5) | 571 (4.8) |
| getattr | 108 | 120 (1.1) | 358 (3.3) | 285 (2.6) |
| setattr | 568 | 490 (0.9) | 1239 (2.2) | 1720 (3.0) |
| access | 109 | 116 (1.1) | 354 (3.2) | 283 (2.6) |
| setxattr | 237 | 113 (0.5) | 1197 (5.1) | 1508 (6.4) |
| getxattr | 110 | 108 (1.0) | 326 (3.0) | 279 (2.5) |
| removexattr | 244 | 116 (0.5) | 847 (3.5) | 1856 (7.6) |
| listxattr_1 | 111 | 106 (1.0) | 336 (3.0) | 286 (2.6) |
| listxattr_10 | 112 | 111 (1.0) | 376 (3.4) | 303 (2.7) |
| link | 715 | 574 (0.8) | 2610 (3.7) | 1949 (2.7) |
| symlink | 952 | 702 (0.7) | 2583 (2.7) | 1960 (2.1) |
| newchunk | 235 | 113 (0.5) | 1 (0.0) | 1 (0.0) |
| write | 816 | 564 (0.7) | 2788 (3.4) | 2138 (2.6) |
| read_1 | 0 | 0 (0.0) | 0 (0.0) | 0 (0.0) |
| read_10 | 0 | 0 (0.0) | 0 (0.0) | 0 (0.0) |

### JuiceFS Bench

|                | Redis-Always   | Redis-Everysec | MySQL          | TiKV           |
| -------------- | -------------- | -------------- | -------------- | -------------- |
| Write big      | 312.81 MiB/s   | 303.45 MiB/s   | 310.26 MiB/s   | 310.90 MiB/s   |
| Read big       | 348.06 MiB/s   | 525.78 MiB/s   | 493.45 MiB/s   | 477.78 MiB/s   |
| Write small    | 26.0 files/s   | 27.5 files/s   | 22.7 files/s   | 24.2 files/s   |
| Read small     | 1431.6 files/s | 1113.4 files/s | 608.0 files/s  | 415.7 files/s  |
| Stat file      | 6713.7 files/s | 6885.8 files/s | 2144.9 files/s | 1164.5 files/s |
| FUSE operation | 0.45 ms        | 0.32 ms        | 0.41 ms        | 0.40 ms        |
| Update meta    | 1.04 ms        | 0.79 ms        | 3.36 ms        | 1.74 ms        |

### mdtest

- 展示了操作速率（每秒 OPS 数），数值越大越好

|                    | Redis-Always | Redis-Everysec | MySQL     | TiKV      |
| ------------------ | ------------ | -------------- | --------- | --------- |
| **EMPTY FILES**    |              |                |           |           |
| Directory creation | 4149.645     | 9261.190       | 1603.298  | 2023.177  |
| Directory stat     | 172665.701   | 243307.527     | 15678.643 | 15029.717 |
| Directory removal  | 4687.027     | 9575.706       | 1420.124  | 1772.861  |
| File creation      | 4257.367     | 8994.232       | 1632.225  | 2119.616  |
| File stat          | 158793.214   | 287425.368     | 15598.031 | 14466.477 |
| File read          | 38872.116    | 47938.792      | 14004.083 | 17149.941 |
| File removal       | 3831.421     | 10538.675      | 983.338   | 1497.474  |
| Tree creation      | 100.403      | 108.657        | 44.154    | 15.615    |
| Tree removal       | 127.257      | 143.625        | 51.804    | 21.005    |
| **SMALL FILES**    |              |                |           |           |
| File creation      | 317.999      | 317.925        | 272.272   | 280.493   |
| File stat          | 54063.617    | 57798.963      | 13882.940 | 10984.141 |
| File read          | 56891.010    | 57548.889      | 16038.716 | 7155.426  |
| File removal       | 3638.809     | 8490.490       | 837.510   | 1184.253  |
| Tree creation      | 54.523       | 119.317        | 23.336    | 5.233     |
| Tree removal       | 73.366       | 82.195         | 22.783    | 4.918     |

### fio

|                 | Redis-Always | Redis-Everysec | MySQL     | TiKV      |
| --------------- | ------------ | -------------- | --------- | --------- |
| Write bandwidth | 350 MiB/s    | 360 MiB/s      | 360 MiB/s | 358 MiB/s |
