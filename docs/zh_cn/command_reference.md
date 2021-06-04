# JuiceFS 命令参考

有许多命令可帮助您管理文件系统，该页面提供了有关这些命令的详细参考。

## 概览

在终端输入 `juicefs` 并执行，你就会看到所有可用的子命令。另外，你可以在每个子命令后面添加 `-h/--help` 标记获得该命令的详细帮助信息。

```
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.14-dev (2021-06-04 d9485fb)

COMMANDS:
   format   format a volume
   mount    mount a volume
   umount   unmount a volume
   gateway  S3-compatible gateway
   sync     sync between two storage
   rmr      remove directories recursively
   info     show internal information for paths or inodes
   bench    run benchmark to read/write/stat big/small files
   gc       collect any leaked objects
   fsck     Check consistency of file system
   profile  analyze access log (Experimental)
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              Disable pprof (:6060) and gops (:6070) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   AGPLv3
```

用法：`juicefs [global options] command [command options] [arguments...]`

在所有命令后面添加 `-h` 或 `--help`，即可获得该命令的参数列表和帮助信息。

***Note:*** If `juicefs` is not placed in your `$PATH`, you should run the script with the path to the script. For example, if `juicefs` is placed in current directory, you should use `./juicefs`. It is recommended to place `juicefs` in your `$PATH` for the convenience.

**注意**：如果 `juicefs` 不在 `$PATH` 中，你需要指定程序所在的路径才能执行。例如，`juicefs` 如果在当前目录中，则可以使用 `./juicefs`。为了方便使用，建议将 `juicefs` 添加到  `$PATH` 中。可以参考 [快速上手指南](quick_start_guide.md) 了解安装相关内容。

以下文档为您提供有关每个子命令的详细信息。

## juicefs format

### Description

Format a volume. It's the first step for initializing a new file system volume.

### Synopsis

```
juicefs format [command options] REDIS-URL NAME
```

### Options

`--block-size value`\
size of block in KiB (default: 4096)

`--capacity value`\
the limit for space in GiB (default: unlimited)

`--inodes value`\
the limit for number of inodes (default: unlimited)

`--compress value`\
compression algorithm (lz4, zstd, none) (default: "none")

`--shards value`\
store the blocks into N buckets by hash of key (default: 0)

`--storage value`\
Object storage type (e.g. s3, gcs, oss, cos) (default: "file")

`--bucket value`\
A bucket URL to store data (default: `"$HOME/.juicefs/local"` or `"/var/jfs"`)

`--access-key value`\
Access key for object storage (env `ACCESS_KEY`)

`--secret-key value`\
Secret key for object storage (env `SECRET_KEY`)

`--encrypt-rsa-key value`\
A path to RSA private key (PEM)

`--force`\
overwrite existing format (default: false)

`--no-update`\
don't update existing volume (default: false)

## juicefs mount

### Description

Mount a volume. The volume shoud be formatted first.

### Synopsis

```
juicefs mount [command options] REDIS-URL MOUNTPOINT
```

### Options

`--metrics value`\
address to export metrics (default: "127.0.0.1:9567")

`--no-usage-report`\
do not send usage report (default: false)

`-d, --background`\
run in background (default: false)

`--no-syslog`\
disable syslog (default: false)

`-o value`\
other FUSE options (see [this document](fuse_mount_options.md) for more information)

`--attr-cache value`\
attributes cache timeout in seconds (default: 1)

`--entry-cache value`\
file entry cache timeout in seconds (default: 1)

`--dir-entry-cache value`\
dir entry cache timeout in seconds (default: 1)

`--enable-xattr`\
enable extended attributes (xattr) (default: false)

`--get-timeout value`\
the max number of seconds to download an object (default: 60)

`--put-timeout value`\
the max number of seconds to upload an object (default: 60)

`--io-retries value`\
number of retries after network failure (default: 30)

`--max-uploads value`\
number of connections to upload (default: 20)

`--buffer-size value`\
total read/write buffering in MiB (default: 300)

`--prefetch value`\
prefetch N blocks in parallel (default: 3)

`--writeback`\
upload objects in background (default: false)

`--cache-dir value`\
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `"/var/jfsCache"`)

`--cache-size value`\
size of cached objects in MiB (default: 1024)

`--free-space-ratio value`\
min free space (ratio) (default: 0.1)

`--cache-partial-only`\
cache only random/small read (default: false)

## juicefs umount

### Description

Unmount a volume.

### Synopsis

```
juicefs umount [options] MOUNTPOINT
```

### Options

`-f, --force`\
unmount a busy mount point by force (default: false)

## juicefs gateway

### Description

S3-compatible gateway.

### Synopsis

```
juicefs gateway [command options] REDIS-URL ADDRESS
```

### Options

`--get-timeout value`\
the max number of seconds to download an object (default: 60)

`--put-timeout value`\
the max number of seconds to upload an object (default: 60)

`--io-retries value`\
number of retries after network failure (default: 30)

`--max-uploads value`\
number of connections to upload (default: 20)

`--buffer-size value`\
total read/write buffering in MiB (default: 300)

`--prefetch value`\
prefetch N blocks in parallel (default: 3)

`--writeback`\
upload objects in background (default: false)

`--cache-dir value`\
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`)

`--cache-size value`\
size of cached objects in MiB (default: 1024)

`--free-space-ratio value`\
min free space (ratio) (default: 0.1)

`--cache-partial-only`\
cache only random/small read (default: false)

`--access-log value`\
path for JuiceFS access log

`--no-usage-report`\
do not send usage report (default: false)

`--no-banner`\
disable MinIO startup information (default: false)

## juicefs sync

### Description

Sync between two storage.

### Synopsis

```
juicefs sync [command options] SRC DST
```

### Options

`--start KEY, -s KEY`\
the first KEY to sync

`--end KEY, -e KEY`\
the last KEY to sync

`--threads value, -p value`\
number of concurrent threads (default: 10)

`--http-port PORT`\
HTTP PORT to listen to (default: 6070)

`--update, -u`\
update existing file if the source is newer (default: false)

`--force-update, -f`\
always update existing file (default: false)

`--perms`\
preserve permissions (default: false)

`--dirs`\
Sync directories or holders (default: false)

`--dry`\
don't copy file (default: false)

`--delete-src, --deleteSrc`\
delete objects from source after synced (default: false)

`--delete-dst, --deleteDst`\
delete extraneous objects from destination (default: false)

`--exclude PATTERN`\
exclude keys containing PATTERN (POSIX regular expressions)

`--include PATTERN`\
only include keys containing PATTERN (POSIX regular expressions)

`--manager value`\
manager address

`--worker value`\
hosts (seperated by comma) to launch worker

`--bwlimit value`\
limit bandwidth in Mbps (0 means unlimited) (default: 0)

`--no-https`\
do not use HTTPS (default: false)

## juicefs rmr

### Description

Remove all files in directories recursively.

### Synopsis

```
juicefs rmr PATH ...
```

## juicefs info

### Description

Show internal information for given paths or inodes.

### Synopsis

```
juicefs info [command options] PATH or INODE
```

### Options

`--inode, -i`\
use inode instead of path (current dir should be inside JuiceFS) (default: false)


## juicefs bench

### Description

Run benchmark, include read/write/stat big and small files.

### Synopsis

```
juicefs bench [command options] PATH
```

### Options

`--block-size value`\
block size in MiB (default: 1)

`--big-file-size value`\
size of big file in MiB (default: 1024)

`--small-file-size value`\
size of small file in MiB (default: 0.1)

`--small-file-count value`\
number of small files (default: 100)

## juicefs gc

### Description

Collect any leaked objects.

### Synopsis

```
juicefs gc [command options] REDIS-URL
```

### Options

`--delete`\
deleted leaked objects (default: false)

`--compact`\
compact all chunks with more than 1 slices (default: false).

`--threads value`\
number threads to delete leaked objects (default: 10)

## juicefs fsck

### Description

Check consistency of file system.

### Synopsis

```
juicefs fsck [command options] REDIS-URL
```

## juicefs profile

### Description

Analyze access log (Experimental).

### Synopsis

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

### Options

`--uid value, -u value`\
track only specified UIDs(separated by comma ,)

`--gid value, -g value`\
track only specified GIDs(separated by comma ,)

`--pid value, -p value`\
track only specified PIDs(separated by comma ,)

`--interval value`\
flush interval in seconds (default: 2)

## juicefs status

### Description

show status of JuiceFS

### Synopsis

```
juicefs status [command options] REDIS-URL
```

### Options

`--session value, -s value`\
show detailed information (sustained inodes, locks) of the specified session (sid) (default: 0)

## juicefs warmup

### Description

build cache for target directories/files

### Synopsis

```
juicefs warmup [command options] [PATH ...]
```

### Options

`--file value, -f value`\
file containing a list of paths

`--threads value, -p value`\
number of concurrent workers (default: 50)

`--background, -b`\
run in background (default: false)
