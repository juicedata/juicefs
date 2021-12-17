---
sidebar_label: 故障诊断和分析
sidebar_position: 9
slug: /fault_diagnosis_and_analysis
---

# JuiceFS 故障诊断和分析

## 错误日志

当 JuiceFS 通过 `-d` 选项在后台运行时，日志会输出到系统日志和 `/var/log/juicefs.log`（v0.15+，参见 [`--log` 选项](../reference/command_reference.md#juicefs-mount)）。取决于你使用的操作系统，你可以通过不同的命令获取日志：

```bash
# macOS
$ syslog | grep 'juicefs'

# Debian based system
$ cat /var/log/syslog | grep 'juicefs'

# CentOS based system
$ cat /var/log/messages | grep 'juicefs'

# v0.15+
$ tail -n 100 /var/log/juicefs.log
```

日志等级有 4 种。你可以使用 `grep` 命令过滤显示不同等级的日志信息，从而进行性能统计和故障追踪。

```bash
$ cat /var/log/syslog | grep 'juicefs' | grep '<INFO>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<WARNING>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<ERROR>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<FATAL>'
```

## 访问日志

JuiceFS 的根目录中有一个名为 `.accesslog` 的虚拟文件，它记录了文件系统上的所有操作及其花费的时间，例如：

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

每行的最后一个数字是当前操作花费的时间（以秒为单位）。 您可以用它调试和分析性能问题，或者尝试使用 `juicefs profile /jfs` 查看实时统计信息。运行 `juicefs profile -h` 或[点此](../benchmark/operations_profiling.md)了解该命令的更多信息。

## 运行时信息

JuiceFS 客户端默认会通过 [pprof](https://pkg.go.dev/net/http/pprof) 在本地监听一个 HTTP 端口用以获取运行时信息，如 Goroutine 堆栈信息、CPU 性能统计、内存分配统计。默认监听的端口号范围是从 6060 开始至 6099 结束，你可以通过系统命令查看当前 JuiceFS 客户端监听的具体端口号：

:::note 注意
如果 JuiceFS 是通过 root 用户挂载，那么需要在 `lsof` 命令前加上 `sudo`。
:::

```bash
$ lsof -i -nP | grep LISTEN | grep juicefs
juicefs   32666 user    8u  IPv4 0x44992f0610d9870b      0t0  TCP 127.0.0.1:6061 (LISTEN)
...
```

在获取到监听端口号以后就可以通过 `http://localhost:<port>/debug/pprof` 地址查看所有可供查询的运行时信息，一些重要的运行时信息如下：

- Goroutine 堆栈信息：`http://localhost:<port>/debug/pprof/goroutine?debug=1`
- CPU 性能统计：`http://localhost:<port>/debug/pprof/profile?seconds=30`
- 内存分配统计：`http://localhost:<port>/debug/pprof/heap`

为了便于分析这些运行时信息，可以将它们保存到本地，例如：

```bash
$ curl 'http://localhost:<port>/debug/pprof/goroutine?debug=1' > juicefs.goroutine.txt
$ curl 'http://localhost:<port>/debug/pprof/profile?seconds=30' > juicefs.cpu.pb.gz
$ curl 'http://localhost:<port>/debug/pprof/heap' > juicefs.heap.pb.gz
```

如果你安装了 `go` 命令，那么可以通过 `go tool pprof` 命令直接分析，例如分析 CPU 性能统计：

```bash
$ go tool pprof 'http://localhost:<port>/debug/pprof/profile?seconds=30'
Fetching profile over HTTP from http://localhost:<port>/debug/pprof/profile?seconds=30
Saved profile in /Users/xxx/pprof/pprof.samples.cpu.001.pb.gz
Type: cpu
Time: Dec 17, 2021 at 1:41pm (CST)
Duration: 30.12s, Total samples = 32.06s (106.42%)
Entering interactive mode (type "help" for commands, "o" for options)
(pprof) top
Showing nodes accounting for 30.57s, 95.35% of 32.06s total
Dropped 285 nodes (cum <= 0.16s)
Showing top 10 nodes out of 192
      flat  flat%   sum%        cum   cum%
    14.73s 45.95% 45.95%     14.74s 45.98%  runtime.cgocall
     7.39s 23.05% 69.00%      7.41s 23.11%  syscall.syscall
     2.92s  9.11% 78.10%      2.92s  9.11%  runtime.pthread_cond_wait
     2.35s  7.33% 85.43%      2.35s  7.33%  runtime.pthread_cond_signal
     1.13s  3.52% 88.96%      1.14s  3.56%  runtime.nanotime1
     0.77s  2.40% 91.36%      0.77s  2.40%  syscall.Syscall
     0.49s  1.53% 92.89%      0.49s  1.53%  runtime.memmove
     0.31s  0.97% 93.86%      0.31s  0.97%  runtime.kevent
     0.27s  0.84% 94.70%      0.27s  0.84%  runtime.usleep
     0.21s  0.66% 95.35%      0.21s  0.66%  runtime.madvise
```

也可以将运行时信息导出为可视化图表，以更加直观的方式进行分析，例如导出内存分配统计信息为 PDF 文件：

:::note 注意
导出为可视化图表功能依赖 [Graphviz](https://graphviz.org)，请先将它安装好。
:::

```bash
$ go tool pprof -pdf 'http://localhost:<port>/debug/pprof/heap' > juicefs.heap.pdf
```

关于 pprof 的更多信息，请查看[官方文档](https://github.com/google/pprof/blob/master/doc/README.md)。
