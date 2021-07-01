# Fault Diagnosis and Analysis

## Error Log

When JuiceFS run in background (through [`-d` option](command_reference.md#juicefs-mount) when mount volume), logs will output to syslog and `/var/log/juicefs.log` (v0.15+, refer to [`--log` option](command_reference.md#juicefs-mount)). Depending on your operating system, you can get the logs through different commands:

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

The last number on each line is the time (in seconds) current operation takes. You can use this to know information of every operation, or try `juicefs profile /jfs` to monitor aggregated statistics. Please run `juicefs profile -h` or refer to [here](operations_profiling.md) to learn more about this subcommand.
