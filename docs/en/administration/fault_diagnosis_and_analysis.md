---
sidebar_label: Fault Diagnosis and Analysis
sidebar_position: 9
slug: /fault_diagnosis_and_analysis
---

# Fault Diagnosis and Analysis

## Error Log

When JuiceFS run in background (through [`-d` option](../reference/command_reference.md#juicefs-mount) when mount volume), logs will output to syslog and `/var/log/juicefs.log` (v0.15+, refer to [`--log` option](../reference/command_reference.md#juicefs-mount)). Depending on your operating system, you can get the logs through different commands:

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

There are 4 log levels. You can use the `grep` command to filter different levels of logs for performance analysis or troubleshooting:

```
$ cat /var/log/syslog | grep 'juicefs' | grep '<INFO>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<WARNING>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<ERROR>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<FATAL>'
```

## Access Log

There is a virtual file called `.accesslog` in the root of JuiceFS to show all the operations and the time they takes, for example:

```bash
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

The last number on each line is the time (in seconds) current operation takes. You can use this to know information of every operation, or try `juicefs profile /jfs` to monitor aggregated statistics. Please run `juicefs profile -h` or refer to [here](../benchmark/operations_profiling.md) to learn more about this subcommand.

## Runtime Information

By default, JuiceFS clients will listen to a TCP port locally via [pprof](https://pkg.go.dev/net/http/pprof) to get runtime information such as Goroutine stack information, CPU performance statistics, memory allocation statistics. You can see the specific port number that the current JuiceFS client is listening on by using the system command (e.g. `lsof`):

:::note
If JuiceFS is mounted via the root user, then you need to add `sudo` before the `lsof` command.
:::

```bash
$ lsof -i -nP | grep LISTEN | grep juicefs
juicefs   32666 user    8u  IPv4 0x44992f0610d9870b      0t0  TCP 127.0.0.1:6061 (LISTEN)
juicefs   32666 user    9u  IPv4 0x44992f0619bf91cb      0t0  TCP 127.0.0.1:6071 (LISTEN)
juicefs   32666 user   15u  IPv4 0x44992f062886fc5b      0t0  TCP 127.0.0.1:9567 (LISTEN)
```

By default, pprof listens on port numbers starting at 6060 and ending at 6099, so the actual port number in the above example is 6061. Once you get the listening port number, you can view all the available runtime information at `http://localhost:<port>/debug/pprof`, and some important runtime information is as follows:

- Goroutine stack information: `http://localhost:<port>/debug/pprof/goroutine?debug=1`
- CPU performance statistics: `http://localhost:<port>/debug/pprof/profile?seconds=30`
- Memory allocation statistics: `http://localhost:<port>/debug/pprof/heap`

To make it easier to analyze this runtime information, you can save it locally, e.g.:

```bash
$ curl 'http://localhost:<port>/debug/pprof/goroutine?debug=1' > juicefs.goroutine.txt
$ curl 'http://localhost:<port>/debug/pprof/profile?seconds=30' > juicefs.cpu.pb.gz
$ curl 'http://localhost:<port>/debug/pprof/heap' > juicefs.heap.pb.gz
```

If you have the `go` command installed, you can analyze it directly with the `go tool pprof` command, for example to analyze CPU performance statistics:

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

Runtime information can also be exported to visual charts for a more intuitive analysis. The visual charts support exporting to various formats such as HTML, PDF, SVG, PNG, etc. For example, the command to export memory allocation statistics as a PDF file is as follows:

:::note
The export to visual chart function relies on [Graphviz](https://graphviz.org), so please install it first.
:::

```bash
$ go tool pprof -pdf 'http://localhost:<port>/debug/pprof/heap' > juicefs.heap.pdf
```

For more information about pprof, please see the [official documentation](https://github.com/google/pprof/blob/master/doc/README.md).
