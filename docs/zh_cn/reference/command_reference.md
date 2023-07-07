---
title: 命令参考
sidebar_position: 1
slug: /command_reference
description: 本文提供 JuiceFS 包含的所有命令及选项的说明、用法和示例。
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## 概览

在终端输入 `juicefs` 并执行，你就会看到所有可用的命令。另外，你可以在每个命令后面添加 `-h/--help` 标记获得该命令的详细帮助信息。

```shell
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.1.0-beta1+2023-06-08.5ef17ba0

COMMANDS:
   ADMIN:
     format   Format a volume
     config   Change configuration of a volume
     quota    Manage directory quotas
     destroy  Destroy an existing volume
     gc       Garbage collector of objects in data storage
     fsck     Check consistency of a volume
     restore  restore files from trash
     dump     Dump metadata into a JSON file
     load     Load metadata from a previously dumped JSON file
     version  Show version
   INSPECTOR:
     status   Show status of a volume
     stats    Show real time performance statistics of JuiceFS
     profile  Show profiling of operations completed in JuiceFS
     info     Show internal information of a path or inode
     debug    Collect and display system static and runtime information
     summary  Show tree summary of a directory
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
     clone     clone a file or directory without copying the underlying data

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             show warning and errors only (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              disable pprof (:6060) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::note 注意
如果命令选项是布尔（boolean）类型，例如 `--debug` ，无需设置任何值，只要在命令中添加 `--debug` 即代表启用该功能，反之则代表不启用。
:::

## 自动补全

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

```shell
sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
source /etc/bash_completion.d/juicefs
```

## 命令列表

### 管理

#### `juicefs format` {#format}

创建文件系统，如果 `META-URL` 中已经存在一个文件系统，则不会再次进行格式化。如果文件系统创建后需要调整配置，请使用 [`juicefs config`](#config)。

##### 使用

```
juicefs format [command options] META-URL NAME
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)」。
- **NAME**：文件系统名称

##### 选项

###### 常规

`--force`<br />
强制覆盖当前的格式化配置 (默认：false)

`--no-update`<br />
不要修改已有的格式化配置 (默认：false)

###### 数据存储

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

`--bucket value`<br />
存储数据的桶路径 (默认：`"$HOME/.juicefs/local"` 或 `"/var/jfs"`)

`--access-key value`<br />
对象存储的 Access Key (也可通过环境变量 `ACCESS_KEY` 设置)

`--secret-key value`<br />
对象存储的 Secret Key (也可通过环境变量 `SECRET_KEY` 设置)

`--session-token value`<br />
对象存储的 session token

`--storage-class value`<br />
默认存储类型

###### 数据格式

`--block-size value`<br />
块大小；单位为 KiB (默认：4096)。4M 是一个较好的默认值，不少对象存储（比如 S3）都将 4M 设为内部的块大小，因此将 JuiceFS block size 设为相同大小，往往也能获得更好的性能

`--compress value`<br />
压缩算法 (`lz4`, `zstd`, `none`) (默认："none")，开启压缩将不可避免地对性能产生一定影响，请权衡。

`--encrypt-rsa-key value`<br />
RSA 私钥的路径 (PEM)

`--encrypt-algo value`<br />
加密算法 (aes256gcm-rsa, chacha20-rsa) (默认："aes256gcm-rsa")

`--hash-prefix`<br />
给每个对象添加 hash 前缀 (默认：false)

`--shards value`<br />
将数据块根据名字哈希存入 N 个桶中 (默认：0)，当 N 大于 0 时，`bucket` 需要写成 `%d` 的形式，例如 `--bucket "juicefs-%d"`

###### 管理

`--capacity value`<br />
容量配额；单位为 GiB (默认：不限制)。如果启用了回收站，那么配额大小也将包含回收站文件

`--inodes value`<br />
文件数配额 (默认：不限制)

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数 (默认：1)

##### 示例

```shell
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

#### `juicefs config` {#config}

修改指定文件系统的配置项。注意更新某些设置以后，客户端未必能立刻生效，需要等待一定时间，具体的等待时间可以通过 [`--heartbeat`](#mount) 选项控制。

##### 使用

```
juicefs config [command options] META-URL
```

##### 选项

###### 常规

`--yes, -y`<br />
对所有提示自动回答 "yes" 并以非交互方式运行 (默认值：false)

`--force`<br />
跳过合理性检查并强制更新指定配置项 (默认：false)

###### 数据存储

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

`--bucket value`<br />
存储数据的桶路径 (默认：`"$HOME/.juicefs/local"` 或 `"/var/jfs"`)

`--access-key value`<br />
对象存储的 Access Key (也可通过环境变量 `ACCESS_KEY` 设置)

`--secret-key value`<br />
对象存储的 Secret Key (也可通过环境变量 `SECRET_KEY` 设置)

`--session-token value`<br />
对象存储的 session token

`--storage-class value`<br />
默认存储类型

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认：0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认：0)

###### 管理

`--capacity value`<br />
容量配额；单位为 GiB (默认：不限制)。如果启用了回收站，那么配额大小也将包含回收站文件

`--inodes value`<br />
文件数配额 (默认：不限制)

`--trash-days value`<br />
文件被自动清理前在回收站内保留的天数 (默认：1)

`--encrypt-secret`<br />
如果密钥之前以原格式存储，则加密密钥 (默认值：false)

`--min-client-version value`<br />
允许连接的最小客户端版本

`--max-client-version value`<br />
允许连接的最大客户端版本

`--dir-stats`<br />
开启目录统计，这是快速汇总和目录配额所必需的 (默认值：false)

##### 示例

```shell
# 显示当前配置
$ juicefs config redis://localhost

# 改变目录的配额
$ juicefs config redis://localhost --inode 10000000 --capacity 1048576

# 更改回收站中文件可被保留的最长天数
$ juicefs config redis://localhost --trash-days 7

# 限制允许连接的客户端版本
$ juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0
```

#### `juicefs quota` {#quota}

管理目录配额

##### 使用

```shell
   juicefs quota command [command options] META-URL
```

##### 子命令

`set`<br />
为目录设置配额

`get`<br />
获取目录配额信息

`delete, del`<br />
删除目录配额

`list, ls`<br />
列出所有目录配额

`check`<br />
检查目录配额的一致性

##### 选项

`--path value`<br />
卷中目录的全路径

`--capacity value`<br />
目录空间硬限制，单位 GiB (默认：0)

`--inodes value`<br />
用于硬限制目录 inode 数 (默认：0)

`--repair`<br />
修复不一致配额 (默认：false)

`--strict`<br />
在严格模式下计算目录的总使用量 (注意：对于大目录可能很慢) (默认：false)

##### 示例

```shell
juicefs quota set redis://localhost --path /dir1 --capacity 1 --inodes 100
juicefs quota get redis://localhost --path /dir1
juicefs quota list redis://localhost
juicefs quota delete redis://localhost --path /dir1
```

#### `juicefs destroy` {#destroy}

销毁一个已经存在的文件系统，将会清空元数据引擎与对象存储中的相关数据。详见[「如何销毁文件系统」](../administration/destroy.md)。

##### 使用

```
juicefs destroy [command options] META-URL UUID
```

##### 选项

`--yes, -y`<br />
对所有提示自动回答 "yes" 并以非交互方式运行 (默认值：false)

`--force`<br />
跳过合理性检查并强制销毁文件系统 (默认：false)

##### 示例

```shell
juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892
```

#### `juicefs gc` {#gc}

用来处理「对象泄漏」，以及因为覆盖写而产生的碎片数据的命令。详见[「状态检查 & 维护」](../administration/status_check_and_maintenance.md#gc)。

##### 使用

```
juicefs gc [command options] META-URL
```

##### 选项

`--delete`<br />
删除泄漏的对象 (默认：false)

`--compact`<br />
整理所有文件的碎片 (默认：false).

`--threads value`<br />
用于删除泄漏对象的线程数 (默认：10)

##### 示例

```shell
# 只检查，没有更改的能力
$ juicefs gc redis://localhost

# 触发所有 slices 的压缩
$ juicefs gc redis://localhost --compact

# 删除泄露的对象
$ juicefs gc redis://localhost --delete
```

#### `juicefs fsck` {#fsck}

检查文件系统一致性。

##### 使用

```
juicefs fsck [command options] META-URL
```

##### 选项

`--path value`<br />
待检查的 JuiceFS 中的绝对路径

`--repair`<br />
发现损坏后尽可能修复 (默认：false)

`--recursive, -r`<br />
递归检查或修复 (默认值：false)

`--sync-dir-stat`<br />
同步所有目录的状态，即使他们没有损坏 (注意：巨大的文件树可能会花费很长时间) (默认：false)

##### 示例

```shell
juicefs fsck redis://localhost
```

#### `juicefs restore` {#restore}

重新构建回收站文件的树结构，并将它们放回原始目录。

##### 使用

```shell
juicefs restore [command options] META HOUR ...
```

##### 选项

`--put-back value`<br />
将恢复的文件移动到原始目录 (默认值：false)

`--threads value`<br />
线程数 (默认：10)

##### 示例

```shell
juicefs restore redis://localhost/1 2023-05-10-01
```

#### `juicefs dump` {#dump}

导出元数据。阅读[「元数据备份」](../administration/metadata_dump_load.md#backup)以了解更多。

##### 使用

```
juicefs dump [command options] META-URL [FILE]
```

- META-URL: 用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../guide/how_to_set_up_metadata_engine.md)
- FILE: 导出文件路径，如果不指定，则会导出到标准输出。如果文件名以 `.gz` 结尾，将会自动压缩

```shell
# 导出元数据至 meta-dump.json
juicefs dump redis://localhost meta-dump.json

# 只导出文件系统的一个子目录的元数据
juicefs dump redis://localhost sub-meta-dump.json --subdir /dir/in/jfs
```

##### 选项

`--subdir value`<br />
只导出指定子目录的元数据。

`--keep-secret-key`<br />
导出对象存储认证信息，默认为 `false`。由于是明文导出，使用时注意数据安全。如果导出文件不包含对象存储认证信息，后续的导入完成后，需要用 [`juicefs config`](#config) 重新配置对象存储认证信息。

#### `juicefs load` {#load}

将元数据导入一个空的文件系统。阅读[「元数据恢复与迁移」](../administration/metadata_dump_load.md#recovery-and-migration)以了解更多。

##### 使用

```
juicefs load [command options] META-URL [FILE]
```

- META-URL: 用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../guide/how_to_set_up_metadata_engine.md)。
- FILE: 导入文件路径，如果不指定，则会从标准输入导入。如果文件名以 `.gz` 结尾，将会自动解压。

```shell
# 将元数据备份文件 meta-dump.json 导入数据库
juicefs load redis://127.0.0.1:6379/1 meta-dump.json
```

##### 选项

`--encrypt-rsa-key value`<br />
加密所使用的 RSA 私钥文件路径。

`--encrypt-algo value`<br />
加密算法，默认为 `aes256gcm-rsa`。

### 检视

#### `juicefs status`{#status}

显示 JuiceFS 的状态。

##### 使用

```
juicefs status [command options] META-URL
```

##### 选项

`--session value, -s value`<br />
展示指定会话 (SID) 的具体信息 (默认：0)

`--more, -m`<br />
显示更多的统计信息，可能需要很长时间 (默认值：false)

##### 示例

```shell
juicefs status redis://localhost
```

#### `juicefs stats` {#stats}

展示实时的性能统计信息。

##### 使用

```
juicefs stats [command options] MOUNTPOINT
```

##### 选项

`--schema value`<br />
控制输出内容的标题字符串 (u: `usage`, f: `fuse`, m: `meta`, c: `blockcache`, o: `object`, g: `go`) (默认："ufmco")

`--interval value`<br />
更新间隔；单位为秒 (默认：1)

`--verbosity value`<br />
详细级别；通常 0 或 1 已足够 (默认：0)

##### 示例

```shell
$ juicefs stats /mnt/jfs

# 更多的指标
$ juicefs stats /mnt/jfs -l 1
```

#### `juicefs profile` {#profile}

分析[访问日志](../administration/fault_diagnosis_and_analysis.md#access-log)。

##### 使用

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

##### 选项

`--uid value, -u value`<br />
仅跟踪指定 UIDs (用逗号分隔)

`--gid value, -g value`<br />
仅跟踪指定 GIDs (用逗号分隔)

`--pid value, -p value`<br />
仅跟踪指定 PIDs (用逗号分隔)

`--interval value`<br />
显示间隔；在回放模式中将其设置为 0 可以立即得到整体的统计结果；单位为秒 (默认：2)

##### 示例

```shell
# 监控实时操作
$ juicefs profile /mnt/jfs

# 重放访问日志
$ cat /mnt/jfs/.accesslog > /tmp/jfs.alog
# 一段时间后按 Ctrl-C 停止 “cat” 命令
$ juicefs profile /tmp/jfs.alog

# 分析访问日志并立即打印总统计数据
$ juicefs profile /tmp/jfs.alog --interval 0
```

#### `juicefs info` {#info}

显示指定路径或 inode 的内部信息。

##### 使用

```
juicefs info [command options] PATH or INODE
```

##### 选项

`--inode, -i`<br />
使用 inode 号而不是路径 (当前目录必须在 JuiceFS 挂载点内) (默认：false)

`--recursive, -r`<br />
递归获取所有子目录的概要信息（注意：当指定一个目录结构很复杂的路径时可能会耗时很长） (默认：false)

`--strict`<br />
获取准确的目录概要 (注意：巨大的文件树可能会花费很长的时间) (默认：false)

`--raw`<br />
显示内部原始信息 (默认：false)

##### 示例

```shell
# 检查路径
$ juicefs info /mnt/jfs/foo

# 检查 inode
$ cd /mnt/jfs
$ juicefs info -i 100
```

#### `juicefs debug` {#debug}

从运行环境、系统日志等多个维度收集和展示信息，帮助更好地定位错误

##### 使用

```
juicefs debug [command options] MOUNTPOINT
```

##### 选项

`--out-dir value`<br />
结果输出目录，若目录不存在则自动创建 (默认：./debug/)

`--limit value`<br />
收集的日志条目数，从新到旧，若不指定则收集全部条目

`--stats-sec value`<br />
.stats 文件采样秒数 (默认：5)

`--trace-sec value`<br />
trace 指标采样秒数 (默认：5)

`--profile-sec value`<br />
profile 指标采样秒数 (默认：30)

##### 示例

```shell
# 收集并展示挂载点 /mnt/jfs 的各类信息
$ juicefs debug /mnt/jfs

# 指定输出目录为 /var/log
$ juicefs debug --out-dir=/var/log /mnt/jfs

# 收集最后 1000 条日志条目
$ juicefs debug --out-dir=/var/log --limit=1000 /mnt/jfs
```

#### `juicefs summary` {#summary}

用于显示目标目录的树摘要

##### 使用

```shell
juicefs summary [command options] PATH
```

##### 选项

`--depth value, -d value`<br />
显示树的深度 (0 表示只显示根) (默认：2)

`--entries value, -e value`<br />
显示前 N 个 entry (按大小排序)(默认：10)

`--strict`<br />
显示准确的摘要，包括目录和文件 (可能很慢) (默认值：false)

`--csv`<br />
以 CSV 格式打印摘要 (默认：false)

##### 示例

```shell
$ juicefs summary /mnt/jfs/foo

# 显示最大深度为 5
$ juicefs summary --depth 5 /mnt/jfs/foo

# 显示前 20 个 entry
$ juicefs summary --entries 20 /mnt/jfs/foo

# 显示准确的结果
$ juicefs summary --strict /mnt/jfs/foo
```

### 服务

#### `juicefs mount` {#mount}

挂载一个已经创建的文件系统。

你可以用任意用户执行挂载命令，不过请确保该用户对缓存目录（`--cache-dir`）有写权限，请阅读[「缓存位置」](../guide/cache_management.md#cache-dir)文档了解更多信息。

##### 使用

```
juicefs mount [command options] META-URL MOUNTPOINT
```

- `META-URL`：用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../guide/how_to_set_up_metadata_engine.md)。
- `MOUNTPOINT`：文件系统挂载点，例如：`/mnt/jfs`、`Z:`。

##### 选项

###### 常规

`-d, --background`<br />
后台运行 (默认：false)

`--no-syslog`<br />
禁用系统日志 (默认：false)

`--log value`<br />
后台运行时日志文件的位置 (默认：`$HOME/.juicefs/juicefs.log` 或 `/var/log/juicefs.log`)

`--force`<br />
强制挂载即使挂载点已经被相同的文件系统挂载 (默认值:false)

`--update-fstab`<br />
新增／更新 `/etc/fstab` 中的条目，如果不存在将会创建一个从 `/sbin/mount.juicefs` 到 JuiceFS 可执行文件的软链接 (默认：false)

###### FUSE

`--enable-xattr`<br />
启用扩展属性 (xattr) 功能 (默认：false)

`--enable-ioctl`<br />
启用 ioctl (仅支持 GETFLAGS/SETFLAGS) (默认：false)

`--root-squash value`<br />
将本地 root 用户 (UID=0) 映射到一个指定用户，如 UID:GID

`--prefix-internal`<br />
添加 `.jfs` 前缀到所有内部文件 (默认：false)

`-o value`<br />
其他 FUSE 选项，详见 [FUSE 挂载选项](../reference/fuse_mount_options.md)

###### 元数据

`--subdir value`<br />
将某个子目录挂载为根 (默认："")

`--backup-meta value`<br />
自动备份元数据到对象存储的间隔时间；单位秒 (0 表示不备份) (默认：3600)

`--heartbeat value`<br />
发送心跳的间隔（单位秒），建议所有客户端使用相同的心跳值 (默认：12)

`--read-only`<br />
只读模式 (默认：false)

`--no-bgjob`<br />
禁用后台任务，默认为 false，也就是说客户端会默认运行后台任务。后台任务包含：

* 清理回收站中过期的文件（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `cleanupDeletedFiles` 和 `cleanupTrash`）
* 清理引用计数为 0 的 Slice（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `cleanupSlices`）
* 清理过期的客户端会话（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `CleanStaleSessions`）

特别地，碎片合并（Compaction）不受该选项的影响，而是随着文件读写操作，自动判断是否需要合并，然后异步执行（以 Redis 为例，在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/redis.go) 中搜索 `compactChunk`）

`--atime-mode value`<br />
控制如何更新 atime（文件最后被访问的时间）。支持以下模式：

* `noatime`（默认），仅在文件创建和主动调用 `SetAttr` 时设置，平时访问与修改文件不影响 atime 值。考虑到更新 atime 需要运行额外的事务，对性能有影响，因此默认关闭
* `relatime`，仅在 mtime（文件内容修改时间）或 ctime（文件元数据修改时间）比 atime 新，或者 atime 超过 24 小时没有更新时进行更新
* `strictatime`，持续更新 atime

`--skip-dir-nlink value`<br />
跳过更新目录 nlink 前的重试次数 (仅用于 TKV, 0 代表不重试) (默认：20)

###### 元数据缓存

`--attr-cache value`<br />
属性缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
文件项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
目录项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--open-cache value`<br />
打开的文件的缓存过期时间（0 代表关闭这个特性）；单位为秒 (默认：0)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--open-cache-limit value`<br />
允许缓存的最大文件个数 (软限制，0 代表不限制) (默认：10000)

###### 数据存储

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

`--bucket value`<br />
为当前挂载点指定访问访对象存储的 endpoint

`--storage-class value`<br />
当前客户端写入数据的存储类型

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认：60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认：60)

`--io-retries value`<br />
网络异常时的重试次数 (默认：10)

`--max-uploads value`<br />
上传对象的连接数 (默认：20)

`--max-deletes value`<br />
删除对象的连接数 (默认：10)

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认：0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认：0)

###### 数据缓存

`--buffer-size value`<br />
读写缓存的总大小；单位为 MiB (默认：300)

`--prefetch value`<br />
并发预读 N 个块 (默认：1)

`--writeback`<br />
后台异步上传对象 (默认：false)。阅读[「客户端写缓存」](../guide/cache_management.md#writeback)了解更多

`--upload-delay value`<br />
数据上传到对象存储的延迟时间，支持秒分时精度，对应格式分别为 ("s", "m", "h")，默认为 0 秒。如果在等待的时间内数据被应用删除，则无需再上传到对象存储，既提升了性能也节省了成本，如果数据只是临时落盘，之后会迅速删除，考虑用该选项进行优化。

`--cache-dir value`<br />
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认：`"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-mode value`<br />
缓存块的文件权限 (默认："0600")

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认：102400)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--free-space-ratio value`<br />
最小剩余空间比例 (默认：0.1)。如果启用了[「客户端写缓存」](../guide/cache_management.md#writeback)，则该参数还控制着写缓存占用空间。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-partial-only`<br />
仅缓存随机小块读 (默认：false)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--verify-cache-checksum value`<br />
缓存数据一致性检查级别，启用 Checksum 校验后，生成缓存文件时会对数据切分做 Checksum 并记录于文件末尾，供读缓存时进行校验。支持以下级别：<br/><ul><li>`none`：禁用一致性检查，如果本地数据被篡改，将会读到错误数据；</li><li>`full`（默认）：读完整数据块时才校验，适合顺序读场景；</li><li>`shrink`：对读范围内的切片数据进行校验，校验范围不包含读边界所在的切片（可以理解为开区间），适合随机读场景；</li><li>`extend`：对读范围内的切片数据进行校验，校验范围同时包含读边界所在的切片（可以理解为闭区间），因此将带来一定程度的读放大，适合对正确性有极致要求的随机读场景。</li></ul>

`--cache-eviction value`<br />
缓存逐出策略 (none 或 2-random) (默认值："2-random")

`--cache-scan-interval value`<br />
扫描缓存目录重建内存索引的间隔 (以秒为单位) (默认："3600")

###### 指标

`--metrics value`<br />
监控数据导出地址 (默认："127.0.0.1:9567")

`--consul value`<br />
Consul 注册中心地址 (默认："127.0.0.1:8500")

`--no-usage-report`<br />
不发送使用量信息 (默认：false)

##### 示例

```shell
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

#### `juicefs umount`{#umount}

卸载一个文件文件系统。

##### 使用

```
juicefs umount [command options] MOUNTPOINT
```

##### 选项

`-f, --force`<br />
强制卸载一个忙碌的文件系统 (默认：false)

`--flush`<br />
等待所有暂存块被刷新 (默认值：false)

##### 示例

```shell
juicefs umount /mnt/jfs
```

#### `juicefs gateway`{#gateway}

启动一个 S3 兼容的网关。

##### 使用

```
juicefs gateway [command options] META-URL ADDRESS
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../guide/how_to_set_up_metadata_engine.md)。
- **ADDRESS**：S3 网关地址和监听的端口，例如：`localhost:9000`

##### 选项

###### 常规

`--access-log value`<br />
访问日志的路径

`--no-banner`<br />
禁用 MinIO 的启动信息 (默认：false)

`--multi-buckets`<br />
使用第一级目录作为存储桶 (默认：false)

`--keep-etag`<br />
保留对象上传时的 ETag (默认：false)

`--umask value`
新文件和新目录的 umask 的八进制格式 (默认值:“022”)

###### 元数据

`--subdir value`<br />
将某个子目录挂载为根 (默认："")

`--backup-meta value`<br />
自动备份元数据到对象存储的间隔时间；单位秒 (0 表示不备份) (默认：3600)

`--heartbeat value`<br />
发送心跳的间隔 (秒);建议所有客户端使用相同的心跳值 (默认：12)

`--read-only`<br />
只读模式 (默认：false)

`--no-bgjob`<br />
禁用后台作业（清理、备份等）（默认值：false）

`--atime-mode value`<br />
控制如何更新 atime（文件最后被访问的时间）。支持以下模式：

* `noatime`（默认），仅在文件创建和主动调用 `SetAttr` 时设置，平时访问与修改文件不影响 atime 值。考虑到更新 atime 需要运行额外的事务，对性能有影响，因此默认关闭
* `relatime`，仅在 mtime（文件内容修改时间）或 ctime（文件元数据修改时间）比 atime 新，或者 atime 超过 24 小时没有更新时进行更新
* `strictatime`，持续更新 atime

`--skip-dir-nlink value`<br />
跳过更新目录 nlink 前的重试次数 (仅用于 TKV, 0 代表不重试) (默认：20)

###### 元数据缓存

`--attr-cache value`<br />
属性缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
文件项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
目录项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--open-cache value`<br />
打开的文件的缓存过期时间（0 代表关闭这个特性）；单位为秒 (默认：0)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--open-cache-limit value`<br />
允许缓存的最大文件个数 (软限制，0 代表不限制) (默认：10000)

###### 数据存储

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

`--bucket value`<br />
为当前挂载点指定访问访对象存储的 endpoint

`--storage-class value`<br />
当前客户端写入数据的存储类型

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认：60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认：60)

`--io-retries value`<br />
网络异常时的重试次数 (默认：10)

`--max-uploads value`<br />
上传对象的连接数 (默认：20)

`--max-deletes value`<br />
删除对象的连接数 (默认：10)

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认：0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认：0)

###### 数据缓存

`--buffer-size value`<br />
读写缓存的总大小；单位为 MiB (默认：300)

`--prefetch value`<br />
并发预读 N 个块 (默认：1)

`--writeback`<br />
后台异步上传对象 (默认：false)。阅读[「客户端写缓存」](../guide/cache_management.md#writeback)了解更多

`--upload-delay value`<br />
数据上传到对象存储的延迟时间，支持秒分时精度，对应格式分别为 ("s", "m", "h")，默认为 0 秒。如果在等待的时间内数据被应用删除，则无需再上传到对象存储，既提升了性能也节省了成本，如果数据只是临时落盘，之后会迅速删除，考虑用该选项进行优化。

`--cache-dir value`<br />
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认：`"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-mode value`<br />
缓存块的文件权限 (默认："0600")

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认：102400)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--free-space-ratio value`<br />
最小剩余空间比例 (默认：0.1)。如果启用了[「客户端写缓存」](../guide/cache_management.md#writeback)，则该参数还控制着写缓存占用空间。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-partial-only`<br />
仅缓存随机小块读 (默认：false)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--verify-cache-checksum value`<br />
缓存数据一致性检查级别，启用 Checksum 校验后，生成缓存文件时会对数据切分做 Checksum 并记录于文件末尾，供读缓存时进行校验。支持以下级别：<br/><ul><li>`none`：禁用一致性检查，如果本地数据被篡改，将会读到错误数据；</li><li>`full`（默认）：读完整数据块时才校验，适合顺序读场景；</li><li>`shrink`：对读范围内的切片数据进行校验，校验范围不包含读边界所在的切片（可以理解为开区间），适合随机读场景；</li><li>`extend`：对读范围内的切片数据进行校验，校验范围同时包含读边界所在的切片（可以理解为闭区间），因此将带来一定程度的读放大，适合对正确性有极致要求的随机读场景。</li></ul>

`--cache-eviction value`<br />
缓存逐出策略 (none 或 2-random) (默认值："2-random")

`--cache-scan-interval value`<br />
扫描缓存目录重建内存索引的间隔 (以秒为单位) (默认："3600")

###### 指标

`--metrics value`<br />
监控数据导出地址 (默认："127.0.0.1:9567")

`--consul value`<br />
Consul 注册中心地址 (默认："127.0.0.1:8500")

`--no-usage-report`<br />
不发送使用量信息 (默认：false)

##### 示例

```shell
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
juicefs gateway redis://localhost localhost:9000
```

#### `juicefs webdav` {#webdav}

启动一个 WebDAV 服务。

##### 使用

```
juicefs webdav [command options] META-URL ADDRESS
```

- **META-URL**：用于元数据存储的数据库 URL，详情查看「[JuiceFS 支持的元数据引擎](../guide/how_to_set_up_metadata_engine.md)」。
- **ADDRESS**：WebDAV 服务监听的地址与端口，例如：`localhost:9007`

##### 选项

###### 常规

`--cert-file value`<br />
HTTPS 证书文件

`--key-file value`<br />
HTTPS 密钥文件

`--gzip`<br />
通过 gzip 压缩提供的文件（默认值：false）

`--disallowList`<br />
禁止列出目录（默认值：false）

`--access-log value`<br />
访问日志的路径

###### 元数据

`--subdir value`<br />
将某个子目录挂载为根 (默认："")

`--backup-meta value`<br />
自动备份元数据到对象存储的间隔时间；单位秒 (0 表示不备份) (默认：3600)

`--heartbeat value`<br />
发送心跳的间隔 (秒);建议所有客户端使用相同的心跳值 (默认：12)

`--read-only`<br />
只读模式 (默认：false)

`--no-bgjob`<br />
禁用后台作业（清理、备份等）（默认值：false）

`--atime-mode value`<br />
控制如何更新 atime（文件最后被访问的时间）。支持以下模式：

* `noatime`（默认），仅在文件创建和主动调用 `SetAttr` 时设置，平时访问与修改文件不影响 atime 值。考虑到更新 atime 需要运行额外的事务，对性能有影响，因此默认关闭
* `relatime`，仅在 mtime（文件内容修改时间）或 ctime（文件元数据修改时间）比 atime 新，或者 atime 超过 24 小时没有更新时进行更新
* `strictatime`，持续更新 atime

`--skip-dir-nlink value`<br />
跳过更新目录 nlink 前的重试次数 (仅用于 TKV, 0 代表不重试) (默认：20)

###### 元数据缓存

`--attr-cache value`<br />
属性缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
文件项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
目录项缓存过期时间；单位为秒 (默认：1)。详见[「内核元数据缓存」](../guide/cache_management.md#kernel-metadata-cache)

`--open-cache value`<br />
打开的文件的缓存过期时间（0 代表关闭这个特性）；单位为秒 (默认：0)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--open-cache-limit value`<br />
允许缓存的最大文件个数 (软限制，0 代表不限制) (默认：10000)

###### 数据存储

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

`--bucket value`<br />
为当前挂载点指定访问访对象存储的 endpoint

`--storage-class value`<br />
当前客户端写入数据的存储类型

`--get-timeout value`<br />
下载一个对象的超时时间；单位为秒 (默认：60)

`--put-timeout value`<br />
上传一个对象的超时时间；单位为秒 (默认：60)

`--io-retries value`<br />
网络异常时的重试次数 (默认：10)

`--max-uploads value`<br />
上传对象的连接数 (默认：20)

`--max-deletes value`<br />
删除对象的连接数 (默认：10)

`--upload-limit value`<br />
上传带宽限制，单位为 Mbps (默认：0)

`--download-limit value`<br />
下载带宽限制，单位为 Mbps (默认：0)

###### 数据缓存

`--buffer-size value`<br />
读写缓存的总大小；单位为 MiB (默认：300)

`--prefetch value`<br />
并发预读 N 个块 (默认：1)

`--writeback`<br />
后台异步上传对象 (默认：false)。阅读[「客户端写缓存」](../guide/cache_management.md#writeback)了解更多

`--upload-delay value`<br />
数据上传到对象存储的延迟时间，支持秒分时精度，对应格式分别为 ("s", "m", "h")，默认为 0 秒。如果在等待的时间内数据被应用删除，则无需再上传到对象存储，既提升了性能也节省了成本，如果数据只是临时落盘，之后会迅速删除，考虑用该选项进行优化。

`--cache-dir value`<br />
本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认：`"$HOME/.juicefs/cache"` 或 `"/var/jfsCache"`)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-mode value`<br />
缓存块的文件权限 (默认："0600")

`--cache-size value`<br />
缓存对象的总大小；单位为 MiB (默认：102400)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--free-space-ratio value`<br />
最小剩余空间比例 (默认：0.1)。如果启用了[「客户端写缓存」](../guide/cache_management.md#writeback)，则该参数还控制着写缓存占用空间。阅读[「缓存」](../guide/cache_management.md)了解更多

`--cache-partial-only`<br />
仅缓存随机小块读 (默认：false)。阅读[「缓存」](../guide/cache_management.md)了解更多

`--verify-cache-checksum value`<br />
缓存数据一致性检查级别，启用 Checksum 校验后，生成缓存文件时会对数据切分做 Checksum 并记录于文件末尾，供读缓存时进行校验。支持以下级别：<br/><ul><li>`none`：禁用一致性检查，如果本地数据被篡改，将会读到错误数据；</li><li>`full`（默认）：读完整数据块时才校验，适合顺序读场景；</li><li>`shrink`：对读范围内的切片数据进行校验，校验范围不包含读边界所在的切片（可以理解为开区间），适合随机读场景；</li><li>`extend`：对读范围内的切片数据进行校验，校验范围同时包含读边界所在的切片（可以理解为闭区间），因此将带来一定程度的读放大，适合对正确性有极致要求的随机读场景。</li></ul>

`--cache-eviction value`<br />
缓存逐出策略 (none 或 2-random) (默认值："2-random")

`--cache-scan-interval value`<br />
扫描缓存目录重建内存索引的间隔 (以秒为单位) (默认：3600)

###### 指标

`--metrics value`<br />
监控数据导出地址 (默认："127.0.0.1:9567")

`--consul value`<br />
Consul 注册中心地址 (默认："127.0.0.1:8500")

`--no-usage-report`<br />
不发送使用量信息 (默认：false)

##### 示例

```shell
juicefs webdav redis://localhost localhost:9007
```

### 工具

#### `juicefs bench` {#bench}

对指定的路径做基准测试，包括对大文件和小文件的读/写/获取属性操作。

##### 使用

```
juicefs bench [command options] PATH
```

有关 `bench` 子命令的详细介绍，请参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-bench)。

##### 选项

`--block-size value`<br />
块大小；单位为 MiB (默认：1)

`--big-file-size value`<br />
大文件大小；单位为 MiB (默认：1024)

`--small-file-size value`<br />
小文件大小；单位为 MiB (默认：0.1)

`--small-file-count value`<br />
小文件数量 (默认：100)

`--threads value, -p value`<br />
并发线程数 (默认：1)

##### 示例

```shell
# 使用4个线程运行基准测试
$ juicefs bench /mnt/jfs -p 4

# 只运行小文件的基准测试
$ juicefs bench /mnt/jfs --big-file-size 0
```

#### `juicefs objbench` {#objbench}

测试对象存储接口的正确性与基本性能

##### 使用

```shell
juicefs objbench [command options] BUCKET
```

有关 `objbench` 子命令的详细介绍，请参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-objbench)。

##### 选项

`--storage value`<br />
对象存储类型 (例如 `s3`、`gcs`、`oss`、`cos`) (默认：`"file"`，请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)

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
小文件的数量（默认值：100）

`--skip-functional-tests`<br />
跳过功能测试（默认值：false）

`--threads value, -p value`<br />
上传下载等操作的并发数（默认值：4）

##### 示例

```shell
# 测试 S3 对象存储的基准性能
$ ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage s3  https://mybucket.s3.us-east-2.amazonaws.com -p 6
```

#### `juicefs warmup` {#warmup}

将文件提前下载到缓存，提升后续本地访问的速度。可以指定某个挂载点路径，递归对这个路径下的所有文件进行缓存预热；也可以通过 `--file` 选项指定文本文件，在文本文件中指定需要预热的文件名。

如果需要预热的文件分布在许多不同的目录，推荐将这些文件名保存到文本文件中并用 `--file` 参数传给预热命令，这样做能利用 `warmup` 的并发功能，速度会显著优于多次调用 `juicefs warmup`，在每次调用里传入单个文件。

##### 使用

```
juicefs warmup [command options] [PATH ...]
```

##### 选项

`--file value, -f value`<br />
指定一个包含一组路径的文件（每一行为一个文件路径）

`--threads value, -p value`<br />
并发的工作线程数，默认 50。如果带宽不足导致下载失败，需要减少并发度，控制下载速度

`--background, -b`<br />
后台运行（默认：false）

##### 示例

```shell
# 预热目录中的所有文件
$ juicefs warmup /mnt/jfs/datadir

# 只预热目录中 3 个文件
$ cat /tmp/filelist
/mnt/jfs/datadir/f1
/mnt/jfs/datadir/f2
/mnt/jfs/datadir/f3
$ juicefs warmup -f /tmp/filelist
```

#### `juicefs rmr`{#rmr}

快速删除目录里的所有文件和子目录，效果等同于 `rm -rf`，但该命令直接操纵元数据，不经过 POSIX，所以速度更快。

如果文件系统启用了回收站功能，被删除的文件会进入回收站。详见[「回收站」](../security/trash.md)。

##### 使用

```
juicefs rmr PATH ...
```

##### 示例

```shell
juicefs rmr /mnt/jfs/foo
```

#### `juicefs sync`{#sync}

在两个存储系统之间同步数据。

##### 使用

```
juicefs sync [command options] SRC DST
```

- **SRC**：源路径
- **DST**：目标路径

源路径和目标路径的格式均为 `[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]`，其中：

- `NAME`：JuiceFS 支持的数据存储类型（如 `s3`、`oss`），请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)。
- `ACCESS_KEY` 和 `SECRET_KEY`：访问数据存储所需的密钥信息，请参考[文档](../guide/how_to_set_up_object_storage.md#access-key-和-secret-key)。
- `TOKEN` 用来访问对象存储的 token，部分对象存储支持使用临时的 token 以获得有限时间的权限
- `BUCKET[.ENDPOINT]`：数据存储服务的访问地址，不同存储类型格式可能不同，具体请参考[文档](../guide/how_to_set_up_object_storage.md#supported-object-storage)。
- `[/PREFIX]`：可选，源路径和目标路径的前缀，可用于限定只同步某些路径中的数据。

有关 `sync` 子命令的详细介绍，请参考[文档](../guide/sync.md)。

##### 选项

###### 选择条件

`--start KEY, -s KEY`<br />
同步的第一个对象名

`--end KEY, -e KEY`<br />
同步的最后一个对象名

`--exclude PATTERN`<br />
排除匹配 PATTERN 的 Key

`--include PATTERN`<br />
不排除匹配 PATTERN 的 Key，需要与 `--exclude` 选项配合使用。

`--limit value`<br />
限制将要处理的对象的数量 (默认：-1)

`--update, -u`<br />
当源文件更新时修改已存在的文件 (默认：false)

`--force-update, -f`<br />
强制修改已存在的文件 (默认：false)

`--existing, --ignore-non-existing`<br />
跳过在目标上创建新文件 (默认值：false)

`--ignore-existing`<br />
跳过更新目标上已经存在的文件 (默认值：false)

###### 行为

`--dirs`<br />
同步目录 (默认：false)

`--perms`<br />
保留权限设置 (默认：false)

`--links, -l`<br />
将符号链接复制为符号链接 (默认：false)

`--delete-src, --deleteSrc`<br />
同步后删除源存储的对象 (默认：false)

`--delete-dst, --deleteDst`<br />
删除目标存储下的不相关对象 (默认：false)

`--check-all`<br />
验证源路径和目标路径中所有文件的数据完整性 (默认：false)

`--check-new`<br />
验证新拷贝文件的数据完整性 (默认：false)

`--dry`<br />
不拷贝文件 (默认：false)

###### 对象存储

`--threads value, -p value`<br />
并发线程数 (默认：10)

`--list-threads value`<br />
列出对象的线程数 (默认：1)

`--list-depth value`<br />
顶级目录的前 N 层用于并行 list (默认：1)

`--no-https`<br />
不要使用 HTTPS (默认：false)

`--storage-class value`<br />
目标端的新建文件的存储类型

`--bwlimit value`<br />
限制最大带宽；单位为 Mbps (0 表示不限制) (默认：0)

###### 集群模式

`--manager value`<br />
管理者地址

`--worker value`<br />
工作节点列表 (使用逗号分隔)

##### 示例

```shell
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

#### `juicefs clone`{#clone}

该命令可以克隆文件或目录但不复制底层数据，类似于 cp 命令，但是非常快。

##### 使用

```shell
juicefs clone [command options] SRC DST
```

##### 选项

`--preserve, -p`<br />
保留文件的 UID、GID 和 mode (默认值：false)

##### 示例

```shell
# 克隆文件
$ juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# 克隆目录
$ juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# 克隆时保留文件的 uid、gid 和 mode
$ juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2
```
