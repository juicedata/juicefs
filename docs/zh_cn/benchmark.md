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

