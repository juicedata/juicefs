---
sidebar_label: 命令参考
sidebar_position: 1
slug: /command_reference
---
# JuiceFS 命令参考

有许多命令可帮助您管理文件系统，该页面提供了有关这些命令的详细参考。

## 概览

在终端输入 `juicefs` 并执行，你就会看到所有可用的命令。另外，你可以在每个命令后面添加 `-h/--help` 标记获得该命令的详细帮助信息。

```shell
$ juicefs -h
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.0-dev (2021-12-27 3462bdbf)

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
   stats    show runtime statistics
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump metadata into a JSON file
   load     load metadata from a previously dumped JSON file
   config   change config of a volume
   destroy  destroy an existing volume
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              Disable pprof (:6060) and gops (:6070) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::tip 提示
如果 `juicefs` 不在 `$PATH` 中，你需要指定程序所在的路径才能执行。例如，`juicefs` 如果在当前目录中，则可以使用 `./juicefs`。为了方便使用，建议将 `juicefs` 添加到  `$PATH` 中。可以参考 [安装&升级](../getting-started/installation.md) 了解安装相关内容。
:::

:::note 注意
如果命令选项是布尔（boolean）类型，例如 `--debug` ，无需设置任何值，只要在命令中添加 `--debug` 即代表启用该功能，反之则代表不启用。
:::

## 自动补全

:::note 注意
此特性需要使用 0.15.2 及以上版本的 JuiceFS。它基于 `github.com/urfave/cli/v2` 实现，更多信息请参见[这里](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling)。
:::

通过加载 `hack/autocomplete` 下的对应脚本可以启用命令的自动补全，例如：

### Bash

```shell
source hack/autocomplete/bash_autocomplete
```

### Zsh

```shell
source hack/autocomplete/zsh_autocomplete
```

请注意自动补全功能仅对当前会话有效。如果你希望对所有新会话都启用此功能，请将 `source` 命令添加到 `.bashrc` 或 `.zshrc` 中：

```shell
echo "source path/to/bash_autocomplete" >> ~/.bashrc
```

或

```shell
echo "source path/to/zsh_autocomplete" >> ~/.zshrc
```

另外，如果你是在 Linux 系统上使用 bash，也可以直接将脚本拷贝到 `/etc/bash_completion.d` 目录并将其重命名为 `juicefs`：

```shell
sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
```

```shell
source /etc/bash_completion.d/juicefs
```

## 命令列表

### juicefs format

#### 描述

格式化文件系统；这是使用新文件系统的第一步。

#### 使用

```
juicefs format [command options] META-URL NAME
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](how_to_setup_metadata_engine.md)」。
- **NAME**：文件系统名称

#### 选项

`--block-size value`<br />
块大小；单位为 KiB (默认: 4096)

`--capacity value`<br />
容量配额；单位为 GiB (默认: 不限制)

`--inodes value`<br />
文件数配额 (默认: 不限制)

`--compress value`<br />
压缩算法 (lz4, zstd, none) (默认: "none")

`--shards value`<br />
将数据块根据名字哈希存入 N 个桶中 (默认: 0)

`--storage value`<br />
对象存储类型 (例如 s3, gcs, oss, cos) (默认: "file")

`--bucket value`<br />
存储数据的桶路径 (默认: `"$HOME/.juicefs/local"` 或 `"/var/jfs"`)

`--access-key value`<br />
对象存储的 Access key (env `ACCESS_KEY`)

`--secret-key value`<br />
对象存储的 Secret key (env `SECRET_KEY`)

`--encrypt-rsa-key value`<br />
RSA 私钥的路径 (PEM)

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数 (默认: 1)

`--force`<br />
强制覆盖当前的格式化配置 (默认: false)

`--no-update`<br />
不要修改已有的格式化配置 (默认: false)

### juicefs mount

#### 描述

挂载一个已经格式化的文件系统。

#### 使用

```
juicefs mount [command options] META-URL MOUNTPOINT
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](how_to_setup_metadata_engine.md)」。
- **MOUNTPOINT**：文件系统挂载点，例如：`/mnt/jfs`

#### 选项

`--metrics value`<br />
监控数据导出地址 (默认: "127.0.0.1:9567")

`--consul value`<br />
consul注册中心地址(默认: "127.0.0.1:8500")

`--no-usage-report`<br />
不发送使用量信息 (默认: false)

`-d, --background`<br />
后台运行 (默认: false)

`--no-syslog`<br />
禁用系统日志 (默认: false)

`--log value`<br />
后台运行时日志文件的位置 (默认: `$HOME/.juicefs/juicefs.log` 或 `/var/log/juicefs.log`)

`-o value`<br />
其他 FUSE 选项 (参见[此文档](../reference/fuse_mount_options.md)来了解更多信息)

`--attr-cache value`<br />
属性缓存过期时间；单位为秒 (默认: 1)

`--entry-cache value`<br />
文件项缓存过期时间；单位为秒 (默认: 1)

`--dir-entry-cache value`<br />
目录项缓存过期时间；单位为秒 (默认: 1)

`--enable-xattr`<br />
启用扩展属性 (xattr) 功能 (默认: false)

`--bucket value`<br />
为当前挂载点指定访问访对象存储的 endpoint

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`<br />
网络异常时的重试次数 (默认: 30)

`--max-uploads value`<br />
上传对象的连接数 (默认: 20)

`--max-deletes value`<br />
删除对象的连接数 (默认: 2)

`--buffer-size value`<br />
读写缓存的总大小；单位为 MiB (默认: 300)

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认: 0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认: 0)

`--prefetch value`<br />
并发预读 N 个块 (默认: 1)

`--writeback`<br />
后台异步上传对象 (默认: false)

`--cache-dir value`<br />
本地缓存目录路径；使用冒号隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认: 102400)

`--free-space-ratio value`<br />
最小剩余空间比例 (默认: 0.1)

`--cache-partial-only`<br />
仅缓存随机小块读 (默认: false)

`--read-only`<br />
只读模式 (默认: false)

`--open-cache value`<br />
打开的文件的缓存过期时间（0 代表关闭这个特性）；单位为秒 (默认: 0)

`--subdir value`<br />
将某个子目录挂载为根 (默认: "")

### juicefs umount

#### 描述

卸载一个文件文件系统。

#### 使用

```
juicefs umount [command options] MOUNTPOINT
```

#### 选项

`-f, --force`<br />
强制卸载一个忙碌的文件系统 (默认: false)

### juicefs gateway

#### 描述

启动一个 S3 兼容的网关。

#### 使用

```
juicefs gateway [command options] META-URL ADDRESS
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](how_to_setup_metadata_engine.md)」。
- **ADDRESS**：S3 网关地址和监听的端口，例如：`localhost:9000`

#### 选项

`--bucket value`<br />
为当前网关指定访问访对象存储的 endpoint

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`<br />
网络异常时的重试次数 (默认: 30)

`--max-uploads value`<br />
上传对象的连接数 (默认: 20)

`--max-deletes value`<br />
删除对象的连接数 (默认: 2)

`--buffer-size value`<br />
读写缓存的总大小；单位为 MiB (默认: 300)

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认: 0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认: 0)

`--prefetch value`<br />
并发预读 N 个块 (默认: 1)

`--writeback`<br />
后台异步上传对象 (默认: false)

`--cache-dir value`<br />
本地缓存目录路径；使用冒号隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `/var/jfsCache`)

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认: 102400)

`--free-space-ratio value`<br />
最小剩余空间比例 (默认: 0.1)

`--cache-partial-only`<br />
仅缓存随机小块读 (默认: false)

`--read-only`<br />
只读模式 (默认: false)

`--open-cache value`<br />
打开的文件的缓存过期时间（0 代表关闭这个特性）；单位为秒 (默认: 0)

`--subdir value`<br />
将某个子目录挂载为根 (默认: "")

`--attr-cache value`<br />
属性缓存过期时间；单位为秒 (默认: 1)

`--entry-cache value`<br />
文件项缓存过期时间；单位为秒 (默认: 0)

`--dir-entry-cache value`<br />
目录项缓存过期时间；单位为秒 (默认: 1)

`--access-log value`<br />
访问日志的路径

`--metrics value`<br />
监控数据导出地址 (默认: "127.0.0.1:9567")

`--no-usage-report`<br />
不发送使用量信息 (默认: false)

`--no-banner`<br />
禁用 MinIO 的启动信息 (默认: false)

`--multi-buckets`<br />
使用第一级目录作为存储桶 (默认: false)

`--keep-etag`<br />
保留对象上传时的 ETag (默认: false)

### juicefs sync

#### 描述

在两个存储系统之间同步数据。

#### 使用

```
juicefs sync [command options] SRC DST
```

- **SRC**：源路径
- **DST**：目标路径

#### 选项

`--start KEY, -s KEY`<br />
同步的第一个对象名

`--end KEY, -e KEY`<br />
同步的最后一个对象名

`--threads value, -p value`<br />
并发线程数 (默认: 10)

`--http-port PORT`<br />
监听的 HTTP 端口 (默认: 6070)

`--update, -u`<br />
当源文件更新时修改已存在的文件 (默认: false)

`--force-update, -f`<br />
强制修改已存在的文件 (默认: false)

`--perms`<br />
保留权限设置 (默认: false)

`--dirs`<br />
同步目录 (默认: false)

`--dry`<br />
不拷贝文件 (默认: false)

`--delete-src, --deleteSrc`<br />
同步后删除源存储的对象 (默认: false)

`--delete-dst, --deleteDst`<br />
删除目标存储下的不相关对象 (默认: false)

`--exclude PATTERN`<br />
跳过包含 PATTERN (POSIX正则表达式) 的对象名

`--include PATTERN`<br />
仅同步包含 PATTERN (POSIX正则表达式) 的对象名

`--manager value`<br />
管理者地址

`--worker value`<br />
工作节点列表 (使用逗号分隔)

`--bwlimit value`<br />
限制最大带宽；单位为 Mbps (0 表示不限制) (默认: 0)

`--no-https`<br />
不要使用 HTTPS (默认: false)

:::note 注意
如果源存储是公共访问权限的桶，请将 `accessKey` 设置为 `anonymous`
:::

### juicefs rmr

#### 描述

递归删除指定目录下的所有文件。

#### 使用

```
juicefs rmr PATH ...
```

### juicefs info

#### 描述

显示指定路径或 inode 的内部信息。

#### 使用

```
juicefs info [command options] PATH or INODE
```

#### 选项

`--inode, -i`<br />
使用 inode 号而不是路径 (当前目录必须在 JuiceFS 挂载点内) (默认: false)

`--recursive, -r`<br />
递归获取所有子目录的概要信息（注意：当指定一个目录结构很复杂的路径时可能会耗时很长） (默认: false)

### juicefs bench

#### 描述

对指定的路径做基准测试，包括对大文件和小文件的读/写/获取属性操作。

#### 使用

```
juicefs bench [command options] PATH
```

#### 选项

`--block-size value`<br />
块大小；单位为 MiB (默认: 1)

`--big-file-size value`<br />
大文件大小；单位为 MiB (默认: 1024)

`--small-file-size value`<br />
小文件大小；单位为 MiB (默认: 0.1)

`--small-file-count value`<br />
小文件数量 (默认: 100)

`--threads value, -p value`<br />
并发线程数 (默认: 1)

### juicefs gc

#### 描述

收集泄漏的对象。

#### 使用

```
juicefs gc [command options] META-URL
```

#### 选项

`--delete`<br />
删除泄漏的对象 (默认: false)

`--compact`<br />
整理所有文件的碎片 (默认: false).

`--threads value`<br />
用于删除泄漏对象的线程数 (默认: 10)

### juicefs fsck

#### 描述

检查文件系统一致性。

#### 使用

```
juicefs fsck [command options] META-URL
```

### juicefs profile

#### 描述

分析[访问日志](../administration/fault_diagnosis_and_analysis.md#访问日志)。

#### 使用

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

#### 选项

`--uid value, -u value`<br />
仅跟踪指定 UIDs (用逗号分隔)

`--gid value, -g value`<br />
仅跟踪指定 GIDs (用逗号分隔)

`--pid value, -p value`<br />
仅跟踪指定 PIDs (用逗号分隔)

`--interval value`<br />
显示间隔；在回放模式中将其设置为 0 可以立即得到整体的统计结果；单位为秒 (默认: 2)

### juicefs stats

#### 描述

展示实时的性能统计信息.

#### 使用

```
juicefs stats [command options] MOUNTPOINT
```

#### 选项

`--schema value`<br />

控制输出内容的标题字符串 (u: usage, f: fuse, m: meta, c: blockcache, o: object, g: go) (默认: "ufmco")

`--interval value`<br />

更新间隔；单位为秒 (默认: 1)

`--verbosity value`<br />

详细级别；通常 0 或 1 已足够 (默认: 0)

`--nocolor`<br />

禁用颜色显示 (默认: false)

### juicefs status

#### 描述

显示 JuiceFS 的状态。

#### 使用

```
juicefs status [command options] META-URL
```

#### 选项

`--session value, -s value`<br />
展示指定会话 (sid) 的具体信息 (默认: 0)

### juicefs warmup

#### 描述

主动为指定目录/文件建立缓存。

#### 使用

```
juicefs warmup [command options] [PATH ...]
```

#### 选项

`--file value, -f value`<br />
指定一个包含一组路径的文件

`--threads value, -p value`<br />
并发的工作线程数 (默认: 50)

`--background, -b`<br />
后台运行 (默认: false)

### juicefs dump

#### 描述

将元数据导出到一个 JSON 文件中。

#### 使用

```
juicefs dump [command options] META-URL [FILE]
```

如果没有指定导出文件路径，会导出到标准输出。

#### 选项

`--subdir value`<br />
只导出一个子目录。

### juicefs load

#### 描述

从之前导出的 JSON 文件中加载元数据。

#### 使用

```
juicefs load [command options] META-URL [FILE]
```

如果没有指定导入文件路径，会从标准输入导入。

### juicefs config

#### 描述

修改指定文件系统的配置项。

#### 使用

```
juicefs config [command options] META-URL
```

#### 选项

`--capacity value`<br />
容量配额；单位为 GiB

`--inodes value`<br />
文件数配额

`--bucket value`<br />
存储数据的桶路径

`--access-key value`<br />
对象存储的 Access key

`--secret-key value`<br />
对象存储的 Secret key

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数

`--force`<br />
跳过合理性检查并强制更新指定配置项 (默认: false)

### juicefs destroy

#### 描述

销毁一个已经存在的文件系统

#### 使用

```
juicefs destroy [command options] META-URL UUID
```

#### 选项

`--force`<br />
跳过合理性检查并强制销毁文件系统 (默认: false)
