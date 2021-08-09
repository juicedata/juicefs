# Operations Profiling

## Introduction

JuiceFS has a special virtual file named `.accesslog` to track every operation occurred within its client. This file may generate thousands of log entries per second when under pressure, making it hard to find out what is actually going on at a certain time. Thus, we made a simple tool called `juicefs profile` to show an overview of recently completed operations. The basic idea is to aggregate all logs in the past interval and display statistics periodically, like:

![juicefs-profiling](../images/juicefs-profiling.gif)

## Profiling Modes

For now there are 2 modes of profiling: real time and replay.

### Real Time Mode

By executing the following command you can watch real time operations under the mount point:

```bash
$ juicefs profile MOUNTPOINT
```

> **Tip**: The result is sorted in a descending order by total time.

### Replay Mode

Running the `profile` command on an existing log file enables the **replay mode**:

```bash
$ juicefs profile LOGFILE
```

When debugging or analyzing perfomance issues, it is usually more practical to record access log first and then replay it (multiple times). For example:

```bash
$ cat /jfs/.accesslog > /tmp/jfs-oplog
# later
$ juicefs profile /tmp/jfs-oplog
```

> **Tip 1**: The replay could be paused anytime by <kbd>Enter/Return</kbd>, and continues by pressing it again.
>
> **Tip 2**: Setting `--interval 0` will replay the whole log file as fast as possible, and show the result as if it was within one interval.

## Filter

Sometimes we are only interested in a certain user or process, then we can filter others out by specifying its IDs, e.g:

```bash
$ juicefs profile /tmp/jfs-oplog --uid 12345
```

For more information, please run `juicefs profile -h`.
