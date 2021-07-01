# JuiceFS 故障诊断和分析

## 错误日志

当 JuiceFS 通过 `-d` 选项在后台运行时，日志会输出到系统日志和 `/var/log/juicefs.log`（v0.15+，参见 [`--log` 选项](command_reference.md#juicefs-mount)）。取决于你使用的操作系统，你可以通过不同的命令获取日志：

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

```
$ cat /var/log/syslog | grep 'juicefs' | grep '<INFO>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<WARNING>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<ERROR>'
$ cat /var/log/syslog | grep 'juicefs' | grep '<FATAL>'
```

## 访问日志

JuiceFS 的根目录中有一个名为`.accesslog` 的虚拟文件，它记录了文件系统上的所有操作及其花费的时间，例如：

```
$ cat /jfs/.accesslog
2021.01.15 08:26:11.003330 [uid:0,gid:0,pid:4403] write (17669,8666,4993160): OK <0.000010>
2021.01.15 08:26:11.003473 [uid:0,gid:0,pid:4403] write (17675,198,997439): OK <0.000014>
2021.01.15 08:26:11.003616 [uid:0,gid:0,pid:4403] write (17666,390,951582): OK <0.000006>
```

每行的最后一个数字是当前操作花费的时间（以秒为单位）。 您可以用它调试和分析性能问题，或者尝试使用 `juicefs profile /jfs` 查看实时统计信息。运行 `juicefs profile -h` 或[点此](operations_profiling.md)了解该命令的更多信息。
