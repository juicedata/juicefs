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
   0.15-dev (2021-06-16 b5d0cd8)

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
   profile  analyze access log
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump JuiceFS metadata into a standalone file
   load     load JuiceFS metadata from a previously dumped file
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

**注意**：如果 `juicefs` 不在 `$PATH` 中，你需要指定程序所在的路径才能执行。例如，`juicefs` 如果在当前目录中，则可以使用 `./juicefs`。为了方便使用，建议将 `juicefs` 添加到  `$PATH` 中。可以参考 [快速上手指南](quick_start_guide.md) 了解安装相关内容。

以下文档为您提供有关每个子命令的详细信息。

## juicefs format

### 描述

格式化文件系统；这是使用新文件系统的第一步。

### 使用

```
juicefs format [command options] REDIS-URL NAME
```

### 选项

`--block-size value`\
块大小；单位为 KiB (默认: 4096)

`--capacity value`\
容量配额；单位为 GiB (默认: 不限制)

`--inodes value`\
文件数配额 (默认: 不限制)

`--compress value`\
压缩算法 (lz4, zstd, none) (默认: "none")

`--shards value`\
将数据块根据名字哈希存入 N 个桶中 (默认: 0)

`--storage value`\
对象存储类型 (例如 s3, gcs, oss, cos) (默认: "file")

`--bucket value`\
存储数据的桶路径 (默认: `"$HOME/.juicefs/local"` 或 `"/var/jfs"`)

`--access-key value`\
对象存储的 Access key (env `ACCESS_KEY`)

`--secret-key value`\
对象存储的 Secret key (env `SECRET_KEY`)

`--encrypt-rsa-key value`\
RSA 私钥的路径 (PEM)

`--force`\
强制覆盖当前的格式化配置 (默认: false)

`--no-update`\
不要修改已有的格式化配置 (默认: false)

## juicefs mount

### 描述

挂载一个已经格式化的文件系统。

### 使用

```
juicefs mount [command options] REDIS-URL MOUNTPOINT
```

### 选项

`--metrics value`\
监控数据导出地址 (默认: "127.0.0.1:9567")

`--no-usage-report`\
不发送使用量信息 (默认: false)

`-d, --background`\
后台运行 (默认: false)

`--no-syslog`\
禁用系统日志 (默认: false)

`-o value`\
其他 FUSE 选项 (参见[此文档](fuse_mount_options.md)来了解更多信息)

`--attr-cache value`\
属性缓存过期时间；单位为秒 (默认: 1)

`--entry-cache value`\
文件项缓存过期时间；单位为秒 (默认: 1)

`--dir-entry-cache value`\
目录项缓存过期时间；单位为秒 (默认: 1)

`--enable-xattr`\
启用扩展属性 (xattr) 功能 (默认: false)

`--read-only`\
只读模式 (默认: false)

`--get-timeout value`\
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`\
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`\
网络异常时的重试次数 (默认: 30)

`--max-uploads value`\
上传对象的连接数 (默认: 20)

`--buffer-size value`\
读写缓存的总大小；单位为 MiB (默认: 300)

`--prefetch value`\
并发预读 N 个块 (默认: 1)

`--writeback`\
后台异步上传对象 (默认: false)

`--cache-dir value`\
本地缓存目录路径；使用冒号隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)

`--cache-size value`\
缓存对象的总大小；单位为 MiB (默认: 1024)

`--free-space-ratio value`\
最小剩余空间比例 (默认: 0.1)

`--cache-partial-only`\
仅缓存随机小块读 (默认: false)

## juicefs umount

### 描述

卸载一个文件文件系统。

### 使用

```
juicefs umount [command options] MOUNTPOINT
```

### 选项

`-f, --force`\
强制卸载一个忙碌的文件系统 (默认: false)

## juicefs gateway

### 描述

启动一个 S3 兼容的网关。

### 使用

```
juicefs gateway [command options] REDIS-URL ADDRESS
```

### 选项

`--get-timeout value`\
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`\
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`\
网络异常时的重试次数 (默认: 30)

`--max-uploads value`\
上传对象的连接数 (默认: 20)

`--buffer-size value`\
读写缓存的总大小；单位为 MiB (默认: 300)

`--prefetch value`\
并发预读 N 个块 (默认: 3)

`--writeback`\
后台异步上传对象 (默认: false)

`--cache-dir value`\
本地缓存目录路径；使用冒号隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `/var/jfsCache`)

`--cache-size value`\
缓存对象的总大小；单位为 MiB (默认: 1024)

`--free-space-ratio value`\
最小剩余空间比例 (默认: 0.1)

`--cache-partial-only`\
仅缓存随机小块读 (默认: false)

`--access-log value`\
访问日志的路径

`--no-usage-report`\
不发送使用量信息 (默认: false)

`--no-banner`\
禁用 MinIO 的启动信息 (默认: false)

## juicefs sync

### 描述

在两个存储系统之间同步数据。

### 使用

```
juicefs sync [command options] SRC DST
```

### 选项

`--start KEY, -s KEY`\
同步的第一个对象名

`--end KEY, -e KEY`\
同步的最后一个对象名

`--threads value, -p value`\
并发线程数 (默认: 10)

`--http-port PORT`\
监听的 HTTP 端口 (默认: 6070)

`--update, -u`\
当源文件更新时修改已存在的文件 (默认: false)

`--force-update, -f`\
强制修改已存在的文件 (默认: false)

`--perms`\
保留权限设置 (默认: false)

`--dirs`\
同步目录 (默认: false)

`--dry`\
不拷贝文件 (默认: false)

`--delete-src, --deleteSrc`\
同步后删除源存储的对象 (默认: false)

`--delete-dst, --deleteDst`\
删除目标存储下的不相关对象 (默认: false)

`--exclude PATTERN`\
跳过包含 PATTERN (POSIX正则表达式) 的对象名

`--include PATTERN`\
仅同步包含 PATTERN (POSIX正则表达式) 的对象名

`--manager value`\
管理者地址

`--worker value`\
工作节点列表 (使用逗号分隔)

`--bwlimit value`\
限制最大带宽；单位为 Mbps (0 表示不限制) (默认: 0)

`--no-https`\
不要使用 HTTPS (默认: false)

## juicefs rmr

### 描述

递归删除指定目录下的所有文件。

### 使用

```
juicefs rmr PATH ...
```

## juicefs info

### 描述

显示指定路径或 inode 的内部信息。

### 使用

```
juicefs info [command options] PATH or INODE
```

### 选项

`--inode, -i`\
使用 inode 号而不是路径 (当前目录必须在 JuiceFS 挂载点内) (默认: false)


## juicefs bench

### 描述

跑一轮基准性能测试，包括对大文件和小文件的读/写/获取属性操作。

### 使用

```
juicefs bench [command options] PATH
```

### 选项

`--block-size value`\
块大小；单位为 MiB (默认: 1)

`--big-file-size value`\
大文件大小；单位为 MiB (默认: 1024)

`--small-file-size value`\
小文件大小；单位为 MiB (默认: 0.1)

`--small-file-count value`\
小文件数量 (默认: 100)

## juicefs gc

### 描述

收集泄漏的对象。

### 使用

```
juicefs gc [command options] REDIS-URL
```

### 选项

`--delete`\
删除泄漏的对象 (默认: false)

`--compact`\
整理所有文件的碎片 (默认: false).

`--threads value`\
用于删除泄漏对象的线程数 (默认: 10)

## juicefs fsck

### 描述

检查文件系统一致性。

### 使用

```
juicefs fsck [command options] REDIS-URL
```

## juicefs profile

### 描述

分析访问日志。

### 使用

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

### 选项

`--uid value, -u value`\
仅跟踪指定 UIDs (用逗号 , 分隔)

`--gid value, -g value`\
仅跟踪指定 GIDs (用逗号 , 分隔)

`--pid value, -p value`\
仅跟踪指定 PIDs (用逗号 , 分隔)

`--interval value`\
显示间隔；单位为秒 (默认: 2)

## juicefs status

### 描述

显示 JuiceFS 的状态。

### 使用

```
juicefs status [command options] REDIS-URL
```

### 选项

`--session value, -s value`\
展示指定会话 (sid) 的具体信息 (默认: 0)

## juicefs warmup

### 描述

主动为指定目录/文件建立缓存。

### 使用

```
juicefs warmup [command options] [PATH ...]
```

### 选项

`--file value, -f value`\
指定一个包含一组路径的文件

`--threads value, -p value`\
并发的工作线程数 (默认: 50)

`--background, -b`\
后台运行 (默认: false)

## juicefs dump

### 描述

将元数据导出到一个 JSON 文件中。

### 使用

```
juicefs dump [command options] META-ADDR FILE
```

## juicefs load

### 描述

从之前导出的 JSON 文件中加载元数据。

### 使用

```
juicefs load [command options] META-ADDR FILE
```
