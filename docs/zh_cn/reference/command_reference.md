---
sidebar_label: 命令参考
sidebar_position: 1
slug: /command_reference
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# JuiceFS 命令参考

有许多命令可帮助您管理文件系统，该页面提供了有关这些命令的详细参考。

## 概览

在终端输入 `juicefs` 并执行，你就会看到所有可用的命令。另外，你可以在每个命令后面添加 `-h/--help` 标记获得该命令的详细帮助信息。

```bash
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.0.0+2022-08-01.0e7afe2d

COMMANDS:
   ADMIN:
     format   Format a volume
     config   Change configuration of a volume
     destroy  Destroy an existing volume
     gc       Garbage collector of objects in data storage
     fsck     Check consistency of a volume
     dump     Dump metadata into a JSON file
     load     Load metadata from a previously dumped JSON file
     version  Show version
   INSPECTOR:
     status   Show status of a volume
     stats    Show real time performance statistics of JuiceFS
     profile  Show profiling of operations completed in JuiceFS
     info     Show internal information of a path or inode
   SERVICE:
     mount    Mount a volume
     umount   Unmount a volume
     gateway  Start an S3-compatible gateway
     webdav   Start a WebDAV server
   TOOL:
     bench     Run benchmarks on a path
     objbench  Run benchmarks on an object storage
     warmup    Build cache for target directories/files
     rmr       Remove directories recursively
     sync      Sync between two storages

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             show warning and errors only (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              disable pprof (:6060) and gops (:6070) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::note 注意
如果 `juicefs` 不在 `$PATH` 中，你需要指定程序所在的路径才能执行。例如，`juicefs` 如果在当前目录中，则可以使用 `./juicefs`。为了方便使用，建议将 `juicefs` 添加到  `$PATH` 中。可以参考[「安装」](../getting-started/installation.md)了解安装相关内容。
:::

:::note 注意
如果命令选项是布尔（boolean）类型，例如 `--debug` ，无需设置任何值，只要在命令中添加 `--debug` 即代表启用该功能，反之则代表不启用。
:::

## 自动补全

:::note 注意
此特性需要使用 0.15.2 及以上版本的 JuiceFS。它基于 `github.com/urfave/cli/v2` 实现，更多信息请参见[这里](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling)。
:::

通过加载 [`hack/autocomplete`](https://github.com/juicedata/juicefs/tree/main/hack/autocomplete) 目录下的对应脚本可以启用命令的自动补全，例如：

<Tabs groupId="juicefs-cli-autocomplete">
  <TabItem value="bash" label="Bash">

```shell
source hack/autocomplete/bash_autocomplete
```

  </TabItem>
  <TabItem value="zsh" label="Zsh">

```shell
source hack/autocomplete/zsh_autocomplete
```

  </TabItem>
</Tabs>

请注意自动补全功能仅对当前会话有效。如果你希望对所有新会话都启用此功能，请将 `source` 命令添加到 `.bashrc` 或 `.zshrc` 中：

<Tabs groupId="juicefs-cli-autocomplete">
  <TabItem value="bash" label="Bash">

```shell
echo "source path/to/bash_autocomplete" >> ~/.bashrc
```

  </TabItem>
  <TabItem value="zsh" label="Zsh">

```shell
echo "source path/to/zsh_autocomplete" >> ~/.zshrc
```

  </TabItem>
</Tabs>

另外，如果你是在 Linux 系统上使用 bash，也可以直接将脚本拷贝到 `/etc/bash_completion.d` 目录并将其重命名为 `juicefs`：

<Tabs>
  <TabItem value="bash" label="Bash">

```shell
sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
```

```shell
source /etc/bash_completion.d/juicefs
```

  </TabItem>
</Tabs>

## 命令列表

### juicefs format

#### 描述

格式化文件系统；这是使用新文件系统的第一步。

#### 使用

```
juicefs format [command options] META-URL NAME
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)」。
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
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认: `"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)查看所有支持的对象存储类型)

`--bucket value`<br />
存储数据的桶路径 (默认: `"$HOME/.juicefs/local"` 或 `"/var/jfs"`)

`--access-key value`<br />
对象存储的 Access Key (也可通过环境变量 `ACCESS_KEY` 设置)

`--secret-key value`<br />
对象存储的 Secret Key (也可通过环境变量 `SECRET_KEY` 设置)

`--session-token value`<br />
对象存储的 session token

`--encrypt-rsa-key value`<br />
RSA 私钥的路径 (PEM)

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数 (默认: 1)

`--force`<br />
强制覆盖当前的格式化配置 (默认: false)

`--no-update`<br />
不要修改已有的格式化配置 (默认: false)

#### 示例

```bash
# 创建一个简单的测试卷（数据将存储在本地目录中）
$ juicefs format sqlite3://myjfs.db myjfs

# 使用 Redis 和 S3 创建卷
$ juicefs format redis://localhost myjfs --storage s3 --bucket https://mybucket.s3.us-east-2.amazonaws.com

# 使用带有密码的 MySQL 创建卷
$ juicefs format mysql://jfs:mypassword@(127.0.0.1:3306)/juicefs myjfs
# 更安全的方法
$ META_PASSWORD=mypassword juicefs format mysql://jfs:@(127.0.0.1:3306)/juicefs myjfs

# 创建一个开启配额设置的卷
$ juicefs format sqlite3://myjfs.db myjfs --inode 1000000 --capacity 102400

# 创建一个关闭了回收站的卷
$ juicefs format sqlite3://myjfs.db myjfs --trash-days 0
```

### juicefs mount

#### 描述

挂载一个已经格式化的文件系统。

#### 使用

```
juicefs mount [command options] META-URL MOUNTPOINT
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)」。
- **MOUNTPOINT**：文件系统挂载点，例如：`/mnt/jfs`、`Z:`。

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
网络异常时的重试次数 (默认: 10)

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
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)

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

`--backup-meta value`<br />
自动备份元数据到对象存储的间隔时间；单位秒 (0表示不备份) (默认: 3600)

`--heartbeat value`<br />
发送心跳的间隔 (秒);建议所有客户端使用相同的心跳值 (默认: 12)。

`--upload-delay value`<br />
数据上传到对象存储的延迟时间,支持秒分时精度，对应格式分别为("s", "m", "h")，默认为 0 秒

`--no-bgjob`<br />
禁用后台作业（清理、备份等）（默认：false）

#### 示例

```bash
# 前台挂载
$ juicefs mount redis://localhost /mnt/jfs

# 使用带密码的 redis 后台挂载
$ juicefs mount redis://:mypassword@localhost /mnt/jfs -d
# 更安全的方式
$ META_PASSWORD=mypassword juicefs mount redis://localhost /mnt/jfs -d

# 将一个子目录挂载为根目录
$ juicefs mount redis://localhost /mnt/jfs --subdir /dir/in/jfs

# 启用 “writeback” 模式，这可以提高性能，但有丢失对象的风险
$ juicefs mount redis://localhost /mnt/jfs -d --writeback

# 开启只读模式
$ juicefs mount redis://localhost /mnt/jfs -d --read-only

# 关闭元数据自动备份
$ juicefs mount redis://localhost /mnt/jfs --backup-meta 0
```

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

#### 示例

```bash
$ juicefs umount /mnt/jfs
```

### juicefs gateway

#### 描述

启动一个 S3 兼容的网关。

#### 使用

```
juicefs gateway [command options] META-URL ADDRESS
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../guide/how_to_set_up_metadata_engine.md)。
- **ADDRESS**：S3 网关地址和监听的端口，例如：`localhost:9000`

#### 选项

`--bucket value`<br />
为当前网关指定访问访对象存储的 endpoint

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`<br />
网络异常时的重试次数 (默认: 10)

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
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `/var/jfsCache`)

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

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认: `"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)查看所有支持的对象存储类型)

`--upload-delay value`<br />
数据上传到对象存储的延迟时间,支持秒分时精度，对应格式分别为("s", "m", "h")，默认为 0 秒

`--backup-meta value`<br />
自动备份元数据到对象存储的间隔时间；单位秒 (0表示不备份) (默认: 3600)

`--heartbeat value`<br />
发送心跳的间隔 (秒);建议所有客户端使用相同的心跳值 (默认: 12)。

`--no-bgjob`<br />
禁用后台作业（清理、备份等）（默认值：false）

`--umask value`
新文件的 umask 的八进制格式 (默认值:“022”)

`--consul value`<br />
consul注册中心地址(默认: "127.0.0.1:8500")

#### 示例

```bash
$ export MINIO_ROOT_USER=admin
$ export MINIO_ROOT_PASSWORD=12345678
$ juicefs gateway redis://localhost localhost:9000
```

### juicefs webdav

#### 描述

启动一个 WebDAV 服务。

#### 使用

```
juicefs webdav [command options] META-URL ADDRESS
```
- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)」。
- **ADDRESS**：webdav 服务监听的地址与端口，例如：`localhost:9007`

#### 选项

`--bucket value`<br />
为当前网关指定访问访对象存储的 endpoint

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认: 60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认: 60)

`--io-retries value`<br />
网络异常时的重试次数 (默认: 10)

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

`--upload-delay`<br />
数据上传到对象存储的延迟时间,支持秒分时精度，对应格式分别为("s", "m", "h")，默认为 0 秒

`--cache-dir value`<br />
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认: `"$HOME/.juicefs/cache"` 或 `/var/jfsCache`)

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认: 102400)

`--free-space-ratio value`<br />
最小剩余空间比例 (默认: 0.1)

`--cache-partial-only`<br />
仅缓存随机小块读 (默认: false)

`--read-only`<br />
只读模式 (默认: false)

`--backup-meta value`<br />
在对象存储中自动备份元数据的时间间隔（0 表示禁用备份）（默认值：1h0m0s）

`--no-bgjob`<br />
禁用后台作业（清理、备份等）（默认值：false）

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

`--gzip`<br />
通过 gzip 压缩提供的文件（默认值：false）

`--disallowList`<br />
禁止列出目录（默认值：false）

`--access-log value`<br />
访问日志的路径

`--metrics value`<br />
监控数据导出地址 (默认: "127.0.0.1:9567")

`--consul value`<br />
consul注册中心地址(默认: "127.0.0.1:8500")

`--no-usage-report`<br />
不发送使用量信息 (默认: false)

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认: `"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)查看所有支持的对象存储类型)

`--heartbeat value`<br />
发送心跳的间隔 (秒);建议所有客户端使用相同的心跳值 (默认: 12)。

#### 示例

```bash
$ juicefs webdav redis://localhost localhost:9007
```

### juicefs sync

#### 描述

在两个存储系统之间同步数据。

#### 使用

```
juicefs sync [command options] SRC DST
```

- **SRC**：源路径
- **DST**：目标路径

源路径和目标路径的格式均为 `[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]`，其中：

- `NAME`：JuiceFS 支持的数据存储类型（如 `s3`、`oss`），请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)。
- `ACCESS_KEY` 和 `SECRET_KEY`：访问数据存储所需的密钥信息，请参考[文档](../guide/how_to_set_up_object_storage.md#access-key-和-secret-key)。
- `BUCKET[.ENDPOINT]`：数据存储服务的访问地址，不同存储类型格式可能不同，具体请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)。
- `[/PREFIX]`：可选，源路径和目标路径的前缀，可用于限定只同步某些路径中的数据。

有关 `sync` 子命令的详细介绍，请参考[文档](../guide/sync.md)。

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
排除匹配 PATTERN 的 Key

`--include PATTERN`<br />
不排除匹配 PATTERN 的 Key，需要与 `--exclude` 选项配合使用。

`--links, -l`<br />
将符号链接复制为符号链接 (默认: false)

` --limit value`<br />
限制将要处理的对象的数量 (默认: -1)

`--manager value`<br />
管理者地址

`--worker value`<br />
工作节点列表 (使用逗号分隔)

`--bwlimit value`<br />
限制最大带宽；单位为 Mbps (0 表示不限制) (默认: 0)

`--no-https`<br />
不要使用 HTTPS (默认: false)

`--check-all`<br />
验证源路径和目标路径中所有文件的数据完整性 (默认: false)

`--check-new`<br />
验证新拷贝文件的数据完整性 (默认: false)

#### 示例
```bash
# 从 OSS 同步到 S3
$ juicefs sync oss://mybucket.oss-cn-shanghai.aliyuncs.com s3://mybucket.s3.us-east-2.amazonaws.com

# 从 S3 同步到 JuiceFS
$ juicefs mount -d redis://localhost /mnt/jfs
$ juicefs sync s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# 源端: a1/b1,a2/b2,aaa/b1   目标端: empty   同步结果: aaa/b1
$ juicefs sync --exclude='a?/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# 源端: a1/b1,a2/b2,aaa/b1   目标端: empty   同步结果: a1/b1,aaa/b1
$ juicefs sync --include='a1/b1' --exclude='a[1-9]/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# 源端: a1/b1,a2/b2,aaa/b1,b1,b2  目标端: empty   同步结果: a1/b1,b2
$ juicefs sync --include='a1/b1' --exclude='a*' --include='b2' --exclude='b?' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/
```

### juicefs rmr

#### 描述

递归删除指定目录下的所有文件。

#### 使用

```
juicefs rmr PATH ...
```

#### 示例

```bash
$ juicefs rmr /mnt/jfs/foo
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

`--raw`<br />
显示内部原始信息 (默认: false)

#### 示例

```bash
$ 检查路径
$ juicefs info /mnt/jfs/foo

# 检查 inode
$ cd /mnt/jfs
$ juicefs info -i 100
```

### juicefs bench

#### 描述

对指定的路径做基准测试，包括对大文件和小文件的读/写/获取属性操作。

#### 使用

```
juicefs bench [command options] PATH
```

有关 `bench` 子命令的详细介绍，请参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-bench)。

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

#### 示例

```bash
# 使用4个线程运行基准测试
$ juicefs bench /mnt/jfs -p 4

# 只运行小文件的基准测试
$ juicefs bench /mnt/jfs --big-file-size 0
```

### juicefs objbench

#### 描述

测试对象存储接口的正确性与基本性能

#### 使用

```shell
juicefs objbench [command options] BUCKET
```

有关 `objbench` 子命令的详细介绍，请参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-objbench)。

#### 选项

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认: `"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#支持的存储服务)查看所有支持的对象存储类型)

`--access-key value`<br />
对象存储的 Access Key (也可通过环境变量 `ACCESS_KEY` 设置)

`--secret-key value`<br />
对象存储的 Secret Key (也可通过环境变量 `SECRET_KEY` 设置)

`--block-size value`<br />
每个 IO 块的大小（以 KiB 为单位）（默认值：4096）

`--big-object-size value`<br />
大文件的大小（以 MiB 为单位）（默认值：1024）

`--small-object-size value`<br />
每个小文件的大小（以 KiB 为单位）（默认值：128）

`--small-objects value`<br />
小文件的数量（以 KiB 为单位）（默认值：100）

`--skip-functional-tests`<br />
跳过功能测试（默认值：false）

`--threads value, -p value`<br />
上传下载等操作的并发数（默认值：4）

#### 示例:

```bash
# 测试 S3 对象存储的基准性能
$ ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage s3  https://mybucket.s3.us-east-2.amazonaws.com -p 6
```

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

#### 示例

```bash
# 只检查，没有更改的能力
$ juicefs gc redis://localhost

# 触发所有 slices 的压缩
$ juicefs gc redis://localhost --compact

# 删除泄露的对象
$ juicefs gc redis://localhost --delete
```

### juicefs fsck

#### 描述

检查文件系统一致性。

#### 使用

```
juicefs fsck [command options] META-URL
```

#### 示例

```bash
$ juicefs fsck redis://localhost
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

#### 示例

```bash
# 监控实时操作
$ juicefs profile /mnt/jfs

# 重放访问日志
$ cat /mnt/jfs/.accesslog > /tmp/jfs.alog
# 一段时间后按 Ctrl-C 停止 “cat” 命令
$ juicefs profile /tmp/jfs.alog

# 分析访问日志并立即打印总统计数据
$ juicefs profile /tmp/jfs.alog --interval 0
```

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

#### 示例

```bash
$ juicefs stats /mnt/jfs

# 更多的指标
$ juicefs stats /mnt/jfs -l 1
```

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

#### 示例

```bash
$ juicefs status redis://localhost
```

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

#### 示例

```bash
# 预热目录中的所有文件
$ juicefs warmup /mnt/jfs/datadir

# 只预热目录中 3 个文件
$ cat /tmp/filelist
/mnt/jfs/datadir/f1
/mnt/jfs/datadir/f2
/mnt/jfs/datadir/f3
$ juicefs warmup -f /tmp/filelist
```

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

#### 示例

```bash
$ juicefs dump redis://localhost meta-dump

# 只导出卷的一个子树
$ juicefs dump redis://localhost sub-meta-dump --subdir /dir/in/jfs
```

### juicefs load

#### 描述

从之前导出的 JSON 文件中加载元数据。

#### 使用

```
juicefs load [command options] META-URL [FILE]
```

如果没有指定导入文件路径，会从标准输入导入。

#### 示例

```bash
$ juicefs load redis://localhost/1 meta-dump
```

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

`--session-token value`<br />
对象存储的 session token

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数

`--force`<br />
跳过合理性检查并强制更新指定配置项 (默认: false)

`--encrypt-secret`<br />
如果密钥之前以原格式存储，则加密密钥 (默认值: false)

`--min-client-version value`<br />
允许连接的最小客户端版本

`--max-client-version value`<br />
允许连接的最大客户端版本

#### 示例

```bash
# 显示当前配置
$ juicefs config redis://localhost

# 改变目录的配额
$ juicefs config redis://localhost --inode 10000000 --capacity 1048576

# 更改回收站中文件可被保留的最长天数
$ juicefs config redis://localhost --trash-days 7

# 限制允许连接的客户端版本
$ juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0
```

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

#### 示例

```bash
$ juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892
```
