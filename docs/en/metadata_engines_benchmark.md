# Metadata Engines Benchmark

Conclusion first:

- For pure metadata operations, MySQL costs about 3 ~ 5x times of Redis
- For small I/O (~100 KiB) workloads, total time costs with MySQL are about 1 ~ 3x of those with Redis
- For large I/O (~4 MiB) workloads, total time costs with different metadata engines show no obvious difference (object storage becomes the bottleneck)


Details are provided below. Please note all the tests are run with the same object storage (to save data), client and metadata hosts; only metadata engines differ.

## Environment

### Object Storage

Amazon S3

### Client Hosts

- Amazon c5.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network
- Ubuntu 18.04.4 LTS

### Meta Hosts

- Amazon c5d.xlarge: 4 vCPUs, 8 GiB Memory, Up to 10 Gigabit Network, 100 GB SSD (local storage for metadata engines)
- Ubuntu 18.04.4 LTS
- SSD is formated as ext4 and mounted on `/data`

### Meta Engines

#### Redis

- Version: [6.2.3](https://download.redis.io/releases/redis-6.2.3.tar.gz)
- Configuration:
  - appendonly: yes
  - appendfsync: everysec
  - dir: `/data/redis`

#### MySQL

- Version: 8.0.25
- `/var/lib/mysql` is bind mounted on `/data/mysql`

## Tools

All the following tests are run for each metadata engine.

### Golang Benchmark

Simple benchmarks within the source code:  `pkg/meta/benchmarks_test.go`.

### JuiceFS Bench

JuiceFS provides a basic benchmark command:

```bash
$ ./juicefs bench /mnt/jfs
```

### mdtest

- Version: mdtest-3.4.0+dev

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
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -d /mnt/jfs

# 12000 * 100KiB files
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -d /mnt/jfs
```

### fio

- Version: fio-3.1

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## Results

### Golang Benchmark

- Shows time cost (us/op), smaller is better
- Number in parentheses is the multiple of Redis cost

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

- Shows rate (ops/sec), bigger is better

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
