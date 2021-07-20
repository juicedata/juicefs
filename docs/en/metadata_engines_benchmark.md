# Metadata Engines Benchmark

Conclusion first:

- For pure metadata operations, MySQL costs about 2 ~ 4x times of Redis; TiKV has similar performance to MySQL, and in most cases it costs a bit less
- For small I/O (~100 KiB) workloads, total time costs with MySQL are about 1 ~ 3x of those with Redis; TiKV performs similarly to MySQL
- For large I/O (~4 MiB) workloads, total time costs with different metadata engines show no obvious difference (object storage becomes the bottleneck)

>**Note**:
>
>1. By changing `appendfsync` from `always` to `everysec`, Redis gains performance boost but loses a bit of data reliability; more information can be found [here](https://redis.io/topics/persistence)
>2. Both Redis and MySQL store only one replica locally, while TiKV stores three replicas in three different hosts using Raft protocol


Details are provided below. Please note all the tests are run with the same object storage (to save data), client and metadata hosts; only metadata engines differ.

## Environment

### JuiceFS Version

juicefs version 0.16-dev (2021-07-20 9efa870)

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
  - appendfsync: always or everysec
  - dir: `/data/redis`

#### MySQL

- Version: 8.0.25
- `/var/lib/mysql` is bind mounted on `/data/mysql`

### TiKV

- Version: 5.1.0
- Configuration:
  - deploy_dir: `/data/tikv-deploy`
  - data_dir: `/data/tikv-data`

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
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -b 3 -z 1 -I 100 -u -d /mnt/jfs

# 12000 * 100KiB files
$ mpirun --use-hwthread-cpus --allow-run-as-root -np 12 --hostfile myhost --map-by slot /root/mdtest -F -w 102400 -I 1000 -z 0 -u -d /mnt/jfs
```

### fio

- Version: fio-3.1

```bash
fio --name=big-write --directory=/mnt/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=4 --end_fsync=1 --group_reporting
```

## Results

### Golang Benchmark

- Shows time cost (us/op), smaller is better
- Number in parentheses is the multiple of Redis-Always cost (`always` and `everysec` are candidates for Redis configuration `appendfsync`)
- Because of metadata cache, the results of `Read` are all less than 1us, which are not comparable for now

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

- Shows rate (ops/sec), bigger is better

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

