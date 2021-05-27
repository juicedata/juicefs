# 元数据引擎性能对比测试

首先展示结论：

- 对于纯元数据操作，MySQL 耗时约为 Redis 的 3～5 倍
- 对于小 IO（～100 KiB）压力，使用 MySQL 引擎的操作总耗时大约是使用 Redis 引擎总耗时的 1～3 倍
- 对于大 IO（～4 MiB）压力，使用不同元数据引擎的总耗时未见明显差异（此时对象存储成为瓶颈）

以下提供了测试的具体细节。这些测试都运行在相同的对象存储（用来存放数据），客户端和元数据节点上；只有元数据引擎不同。

## 测试环境

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
  - appendfsync: everysec
  - dir: `/data/redis`

#### MySQL

- 版本: 8.0.25
- `/var/lib/mysql` 目录被绑定挂载到 `/data/mysql`

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
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -d /mnt/jfs

# 12000 * 100KiB files
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -d /mnt/jfs
```

### fio

- 版本: fio-3.1

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## 测试结果

### Golang Benchmark

- 展示了操作耗时（单位为 微秒/op），数值越小越好
- 括号内数字是该指标对比 Redis 的倍数

|              | Redis | MySQL       |
| ----         | ----- | -----       |
| mkdir        | 421   | 1820 (4.3)  |
| mvdir        | 586   | 2872 (4.9)  |
| rmdir        | 504   | 2248 (4.5)  |
| readdir_10   | 220   | 1047 (4.8)  |
| readdir_1k   | 1506  | 14354 (9.5) |
| mknod        | 442   | 1821 (4.1)  |
| create       | 437   | 1768 (4.0)  |
| rename       | 580   | 2840 (4.9)  |
| unlink       | 456   | 2525 (5.5)  |
| lookup       | 76    | 310 (4.1)   |
| getattr      | 69    | 269 (3.9)   |
| setattr      | 283   | 1023 (3.6)  |
| access       | 69    | 269 (3.9)   |
| setxattr     | 71    | 921 (13.0)  |
| getxattr     | 68    | 242 (3.6)   |
| removexattr  | 76    | 711 (9.4)   |
| listxattr_1  | 68    | 259 (3.8)   |
| listxattr_10 | 70    | 290 (4.1)   |
| link         | 360   | 2058 (5.7)  |
| symlink      | 429   | 2013 (4.7)  |
| newchunk     | 69    | 0 (0.0)     |
| write        | 368   | 2720 (7.4)  |
| read_1       | 71    | 236 (3.3)   |
| read_10      | 87    | 301 (3.5)   |

### JuiceFS Bench

|                | Redis          | MySQL          |
| -------------- | -------------- | -------------- |
| Write big      | 318.84 MiB/s   | 306.77 MiB/s   |
| Read big       | 469.94 MiB/s   | 507.13 MiB/s   |
| Write small    | 23.4 files/s   | 24.6 files/s   |
| Read small     | 2155.4 files/s | 1714.7 files/s |
| Stat file      | 6015.8 files/s | 2867.9 files/s |
| FUSE operation | 0.4 ms         | 0.4 ms         |
| Update meta    | 0.9 ms         | 2.5 ms         |

### mdtest

- 展示了操作速率（每秒 OPS 数），数值越大越好

|                    | Redis     | MySQL     |
| ------------------ | --------- | -----     |
| **EMPTY FILES**    |           |           |
| Directory creation | 282.694   | 215.366   |
| Directory stat     | 47474.718 | 12632.878 |
| Directory removal  | 330.430   | 198.588   |
| File creation      | 222.603   | 226.587   |
| File stat          | 45960.505 | 13012.763 |
| File read          | 49088.346 | 15622.533 |
| File removal       | 334.759   | 195.183   |
| Tree creation      | 956.797   | 390.026   |
| Tree removal       | 295.399   | 284.733   |
| **SMALL FILES**    |           |           |
| File creation      | 255.077   | 245.659   |
| File stat          | 51799.065 | 14191.255 |
| File read          | 47091.975 | 16794.314 |
| File removal       | 631.046   | 194.810   |
| Tree creation      | 749.869   | 339.375   |
| Tree removal       | 282.643   | 165.118   |

### fio

|                 | Redis     | MySQL     |
| --------------- | --------- | --------- |
| Write bandwidth | 350 MiB/s | 360 MiB/s |
