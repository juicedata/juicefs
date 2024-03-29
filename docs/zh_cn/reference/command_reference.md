---
title: 命令参考
sidebar_position: 1
slug: /command_reference
description: JuiceFS 客户端的所有命令及选项的说明、用法和示例。
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

在终端输入 `juicefs` 并执行，就能看到所有可用的命令。在每个子命令后面添加 `-h/--help` 并运行，就能获得该命令的详细帮助信息，例如 `juicefs format -h`。

```
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.1.0

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
   --log-id value          append the given log id in log, use "random" to use random uuid
   --no-agent              disable pprof (:6060) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

## 自动补全 {#auto-completion}

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
cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
source /etc/bash_completion.d/juicefs
```

## 管理 {#admin}

### `juicefs format` {#format}

创建并格式化文件系统，如果 `META-URL` 中已经存在一个文件系统，不会再次进行格式化。如果文件系统创建后需要调整配置，请使用 [`juicefs config`](#config)。

#### 概览

```shell
juicefs format [command options] META-URL NAME

# 创建一个简单的测试卷（数据将存储在本地目录中）
juicefs format sqlite3://myjfs.db myjfs

# 使用 Redis 和 S3 创建卷
juicefs format redis://localhost myjfs --storage=s3 --bucket=https://mybucket.s3.us-east-2.amazonaws.com

# 使用带有密码的 MySQL 创建卷
juicefs format mysql://jfs:mypassword@(127.0.0.1:3306)/juicefs myjfs
# 更安全的方法
META_PASSWORD=mypassword juicefs format mysql://jfs:@(127.0.0.1:3306)/juicefs myjfs

# 创建一个开启配额设置的卷
juicefs format sqlite3://myjfs.db myjfs --inodes=1000000 --capacity=102400

# 创建一个关闭了回收站的卷
juicefs format sqlite3://myjfs.db myjfs --trash-days=0
```

#### 参数

|项 | 说明|
|-|-|
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
|`NAME`|文件系统名称。|
|`--force`|强制覆盖当前的格式化配置，默认为 false。|
|`--no-update`|不要修改已有的格式化配置，默认为 false。|

#### 数据存储参数 {#format-data-storage-options}

|项 | 说明|
|-|-|
|`--storage=file`|对象存储类型，例如 `s3`、`gs`、`oss`、`cos`。默认为 `file`，参考[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型。|
|`--bucket=path`|存储数据的桶路径（默认：`$HOME/.juicefs/local` 或 `/var/jfs`）。|
|`--access-key=value`|对象存储的 Access Key，也可通过环境变量 `ACCESS_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--secret-key=value`|对象存储的 Secret Key，也可通过环境变量 `SECRET_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--session-token=value`|对象存储的临时访问凭证（Session Token），查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#session-token)以了解更多。|
|`--storage-class=value` <VersionAdd>1.1</VersionAdd>|默认存储类型。|

#### 数据格式参数 {#format-data-format-options}

|项 | 说明|
|-|-|
|`--block-size=4096`|块大小，单位为 KiB，默认 4096。4M 是一个较好的默认值，不少对象存储（比如 S3）都将 4M 设为内部的块大小，因此将 JuiceFS block size 设为相同大小，往往也能获得更好的性能。|
|`--compress=none`|压缩算法，支持 `lz4`、`zstd`、`none`（默认），启用压缩将不可避免地对性能产生一定影响。这两种压缩算法中，`lz4` 提供更好的性能，但压缩比要逊于 `zstd`，他们的具体性能差别具体需要读者自行搜索了解。|
|`--encrypt-rsa-key=value`|RSA 私钥的路径，查看[数据加密](../security/encryption.md)以了解更多。|
|`--encrypt-algo=aes256gcm-rsa`|加密算法 (aes256gcm-rsa, chacha20-rsa) (默认："aes256gcm-rsa")|
|`--hash-prefix`|对于部分对象存储服务，如果对象存储命名路径的键值（key）是连续的，那么坐落在对象存储上的物理数据也将是连续的。在大规模顺序读场景下，这样会带来数据访问热点，让对象存储服务的部分区域访问压力过大。<br/><br/>启用 `--hash-prefix` 将会给每个对象路径命名添加 hash 前缀（用 slice ID 对 256 取模，详见[内部实现](../development/internals.md#object-storage-naming-format)），相当于“打散”对象存储键值，避免在对象存储服务层面创造请求热点。显而易见，由于影响着对象存储块的命名规则，该选项**必须在创建文件系统之初就指定好、不能动态修改。**<br/><br/>目前而言，[AWS S3](https://aws.amazon.com/about-aws/whats-new/2018/07/amazon-s3-announces-increased-request-rate-performance) 已经做了优化，不再需要应用侧的随机对象前缀。而对于其他对象对象存储服务（比如 [COS 就在文档里推荐随机化前缀](https://cloud.tencent.com/document/product/436/13653#.E6.B7.BB.E5.8A.A0.E5.8D.81.E5.85.AD.E8.BF.9B.E5.88.B6.E5.93.88.E5.B8.8C.E5.89.8D.E7.BC.80)），因此，对于这些对象存储，如果文件系统规模庞大，建议启用该选项以提升性能。|
|`--shards=0`|如果对象存储服务在桶级别设置了限速（或者你使用自建的对象存储服务，单个桶的性能有限），可以将数据块根据名字哈希分散存入 N 个桶中。该值默认为 0，也就是所有数据存入单个桶。当 N 大于 0 时，`bucket` 需要包含 `%d` 占位符，例如 `--bucket=juicefs-%d`。`--shards` 设置无法动态修改，需要提前规划好用量。|

#### 管理参数 {#format-management-options}

|项 | 说明|
|-|-|
|`--capacity=0`|容量配额，单位为 GiB，默认为 0 代表不限制。如果启用了[回收站](../security/trash.md)，那么配额大小也将包含回收站文件。|
|`--inodes=0`|文件数配额，默认为 0 代表不限制。|
|`--trash-days=1`|文件被删除后，默认会进入[回收站](../security/trash.md)，该选项控制已删除文件在回收站内保留的天数，默认为 1，设为 0 以禁用回收站。|
|`--enable-acl=true` <VersionAdd>1.2</VersionAdd>|启用[POSIX ACL](../security/posix_acl.md)，该选项启用后暂不支持关闭。|

### `juicefs config` {#config}

修改指定文件系统的配置项。注意更新某些设置以后，客户端未必能立刻生效，需要等待一定时间，具体的等待时间可以通过 [`--heartbeat`](#mount) 选项控制。

#### 概览

```shell
juicefs config [command options] META-URL

# 显示当前配置
juicefs config redis://localhost

# 改变目录的配额
juicefs config redis://localhost --inodes 10000000 --capacity 1048576

# 更改回收站中文件可被保留的最长天数
juicefs config redis://localhost --trash-days 7

# 限制允许连接的客户端版本
juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0
```

#### 参数

|项 | 说明|
|-|-|
|`--yes, -y`|对所有提示自动回答 "yes" 并以非交互方式运行 (默认值：false)|
|`--force`|跳过合理性检查并强制更新指定配置项 (默认：false)|

#### 数据存储参数 {#config-data-storage-options}

|项 | 说明|
|-|-|
|`--storage=file` <VersionAdd>1.1</VersionAdd>|对象存储类型，例如 `s3`、`gs`、`oss`、`cos`。默认为 `file`，参考[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型。|
|`--bucket=path`|存储数据的桶路径（默认：`$HOME/.juicefs/local` 或 `/var/jfs`）。|
|`--access-key=value`|对象存储的 Access Key，也可通过环境变量 `ACCESS_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--secret-key=value`|对象存储的 Secret Key，也可通过环境变量 `SECRET_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--session-token=value`|对象存储的临时访问凭证（Session Token），查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#session-token)以了解更多。|
|`--storage-class=value` <VersionAdd>1.1</VersionAdd>|默认存储类型。|
|`--upload-limit=0`|上传带宽限制，单位为 Mbps (默认：0)|
|`--download-limit=0`|下载带宽限制，单位为 Mbps (默认：0)|

#### 管理参数 {#config-management-options}

|项 | 说明|
|-|-|
|`--capacity value`|容量配额，单位为 GiB|
|`--inodes value`|文件数配额|
|`--trash-days value`|文件被自动清理前在回收站内保留的天数|
|`--encrypt-secret`|如果密钥之前以原格式存储，则加密密钥 (默认值：false)|
|`--min-client-version value` <VersionAdd>1.1</VersionAdd>|允许连接的最小客户端版本|
|`--max-client-version value` <VersionAdd>1.1</VersionAdd>|允许连接的最大客户端版本|
|`--dir-stats` <VersionAdd>1.1</VersionAdd>|开启目录统计，这是快速汇总和目录配额所必需的 (默认值：false)|
|`--enable-acl` <VersionAdd>1.2</VersionAdd>|开启 POSIX ACL(不支持关闭), 同时允许连接的最小客户端版本会提升到 v1.2|

### `juicefs quota` <VersionAdd>1.1</VersionAdd> {#quota}

管理目录配额

#### 概览

```shell
juicefs quota command [command options] META-URL

# 为目录设置配额
juicefs quota set redis://localhost --path /dir1 --capacity 1 --inodes 100

# 获取目录配额信息
juicefs quota get redis://localhost --path /dir1

# 列出所有目录配额
juicefs quota list redis://localhost

# 删除目录配额
juicefs quota delete redis://localhost --path /dir1

# 检查目录配额的一致性
juicefs quota check redis://localhost
```

#### 参数

|项 | 说明|
|-|-|
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
|`--path value`|卷中目录的全路径|
|`--capacity value`|目录空间硬限制，单位 GiB (默认：0)|
|`--inodes value`|用于硬限制目录 inode 数 (默认：0)|
|`--repair`|修复不一致配额 (默认：false)|
|`--strict`|在严格模式下计算目录的总使用量 (注意：对于大目录可能很慢) (默认：false)|

### `juicefs destroy` {#destroy}

销毁一个已经存在的文件系统，将会清空元数据引擎与对象存储中的相关数据。详见[「如何销毁文件系统」](../administration/destroy.md)。

#### 概览

```shell
juicefs destroy [command options] META-URL UUID

juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892
```

#### 参数

| 项                                         | 说明|
|-------------------------------------------|-|
| `--yes, -y` <VersionAdd>1.1</VersionAdd> |对所有提示自动回答 "yes" 并以非交互方式运行 (默认值：false)|
| `--force`                                 |跳过合理性检查并强制销毁文件系统 (默认：false)|

### `juicefs gc` {#gc}

如果对象存储块因为某种原因，完全脱离了 JuiceFS 的管理，也就是对象存储上依然还存在，但在 JuiceFS 元数据已经不复存在，无法被回收释放，这种现象称作「对象泄漏」。如果你并没有进行任何特殊操作，那么对象泄露通常昭示着 bug，建议提交 [GitHub Issue](https://github.com/juicedata/juicefs/issues/new/choose)。

与此同时，你可以用该命令清理泄漏对象。顺带一提，该命令还能够清理失效的文件碎片。详见[「状态检查 & 维护」](../administration/status_check_and_maintenance.md#gc)。

#### 概览

```shell
juicefs gc [command options] META-URL

# 只检查，没有更改的能力
juicefs gc redis://localhost

# 触发所有 slices 的压缩
juicefs gc redis://localhost --compact

# 删除泄露的对象
juicefs gc redis://localhost --delete
```

#### 参数

|项 | 说明|
|-|-|
|`--delete`|删除泄漏的对象，以及因不完整的 `clone` 命令而产生泄漏的元数据。|
|`--compact`|对所有文件执行碎片合并。|
|`--threads=10`|并发线程数，默认为 10。|

### `juicefs fsck` {#fsck}

检查文件系统一致性。

#### 概览

```shell
juicefs fsck [command options] META-URL

juicefs fsck redis://localhost
```

#### 参数

|项 | 说明|
|-|-|
|`--path value` <VersionAdd>1.1</VersionAdd>|待检查的 JuiceFS 中的绝对路径|
|`--repair` <VersionAdd>1.1</VersionAdd>|发现损坏后尽可能修复 (默认：false)|
|`--recursive, -r` <VersionAdd>1.1</VersionAdd>|递归检查或修复 (默认值：false)|
|`--sync-dir-stat` <VersionAdd>1.1</VersionAdd>|同步所有目录的状态，即使他们没有损坏 (注意：巨大的文件树可能会花费很长时间) (默认：false)|

### `juicefs restore` <VersionAdd>1.1</VersionAdd> {#restore}

重新构建回收站文件的树结构，并将它们放回原始目录。如果需要恢复文件存在命名冲突，程序会直接跳过，不会覆盖新创建的文件（注意日志中会有提示）。

#### 概览

```shell
juicefs restore [command options] META HOUR ...

juicefs restore redis://localhost/1 2023-05-10-01
```

#### 参数

|项 | 说明|
|-|-|
|`--put-back`|将恢复的文件移动到原始目录，面对命名冲突时会直接跳过，不会覆盖已有文件。|
|`--threads=10`|线程数，默认 10，如果恢复速度慢，增加并发以提速。|

### `juicefs dump` {#dump}

导出元数据。阅读[「元数据备份」](../administration/metadata_dump_load.md#backup)以了解更多。

#### 概览

```shell
juicefs dump [command options] META-URL [FILE]

# 导出元数据至 meta-dump.json
juicefs dump redis://localhost meta-dump.json

# 只导出文件系统的一个子目录的元数据
juicefs dump redis://localhost sub-meta-dump.json --subdir /dir/in/jfs
```

#### 参数

|项 | 说明|
|-|-|
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
|`FILE`|导出文件路径，如果不指定，则会导出到标准输出。如果文件名以 `.gz` 结尾，将会自动压缩。|
|`--subdir=path`|只导出指定子目录的元数据。|
|`--keep-secret-key` <VersionAdd>1.1</VersionAdd>|导出对象存储认证信息，默认为 `false`。由于是明文导出，使用时注意数据安全。如果导出文件不包含对象存储认证信息，后续的导入完成后，需要用 [`juicefs config`](#config) 重新配置对象存储认证信息。|
|`--fast` <VersionAdd>1.2</VersionAdd>|使用更多内存来加速导出。|
|`--skip-trash` <VersionAdd>1.2</VersionAdd>|跳过回收站中的文件和目录。|

### `juicefs load` {#load}

将元数据导入一个空的文件系统。阅读[「元数据恢复与迁移」](../administration/metadata_dump_load.md#recovery-and-migration)以了解更多。

#### 概览

```shell
juicefs load [command options] META-URL [FILE]

# 将元数据备份文件 meta-dump.json 导入数据库
juicefs load redis://127.0.0.1:6379/1 meta-dump.json
```

#### 参数

| 项                                                          | 说明|
|------------------------------------------------------------|-|
| `META-URL`                                                 |用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
| `FILE`                                                     |导入文件路径，如果不指定，则会从标准输入导入。如果文件名以 `.gz` 结尾，将会自动解压。|
| `--encrypt-rsa-key=path` <VersionAdd>1.0.4</VersionAdd>    |加密所使用的 RSA 私钥文件路径。|
| `--encrypt-algo=aes256gcm-rsa` <VersionAdd>1.0.4</VersionAdd> |加密算法，默认为 `aes256gcm-rsa`。|

## 检视 {#inspector}

### `juicefs status` {#status}

显示 JuiceFS 的状态。

#### 概览

```shell
juicefs status [command options] META-URL

juicefs status redis://localhost
```

#### 参数

|项 | 说明|
|-|-|
|`--session=0, -s 0`|展示指定会话 (SID) 的具体信息 (默认：0)|
|`--more, -m` <VersionAdd>1.1</VersionAdd>|显示更多的统计信息，可能需要很长时间 (默认值：false)|

### `juicefs stats` {#stats}

展示实时的性能统计信息，阅读[「实时性能监控」](../administration/fault_diagnosis_and_analysis.md#performance-monitor)以了解更多。

#### 概览

```shell
juicefs stats [command options] MOUNTPOINT

juicefs stats /mnt/jfs

# 更多的指标
juicefs stats /mnt/jfs -l 1
```

#### 参数

|项 | 说明|
|-|-|
|`--schema=ufmco`|控制输出内容的标题字符串，默认为 `ufmco`，含义如下：<br/>`u`：usage<br/>`f`：FUSE<br/>`m`：metadata<br/>`c`：block cache<br/>`o`：object storage<br/>`g`：Go|
|`--interval=1`|更新间隔；单位为秒 (默认：1)|
|`--verbosity=0`|详细级别；通常 0 或 1 已足够 (默认：0)|

### `juicefs profile` {#profile}

展示基于[文件系统访问日志](../administration/fault_diagnosis_and_analysis.md#access-log)的实时监控数据，阅读[「实时性能监控」](../administration/fault_diagnosis_and_analysis.md#performance-monitor)以了解更多。

#### 概览

```shell
juicefs profile [command options] MOUNTPOINT/LOGFILE

# 监控实时操作
juicefs profile /mnt/jfs

# 重放访问日志
cat /mnt/jfs/.accesslog > /tmp/jfs.alog
# 一段时间后按 Ctrl-C 停止 “cat” 命令
juicefs profile /tmp/jfs.alog

# 分析访问日志并立即打印总统计数据
juicefs profile /tmp/jfs.alog --interval 0
```

#### 参数

|项 | 说明|
|-|-|
|`--uid=value, -u value`|仅跟踪指定 UIDs (用逗号分隔)|
|`--gid=value, -g value`|仅跟踪指定 GIDs (用逗号分隔)|
|`--pid=value, -p value`|仅跟踪指定 PIDs (用逗号分隔)|
|`--interval=2`|显示间隔；在回放模式中将其设置为 0 可以立即得到整体的统计结果；单位为秒 (默认：2)|

### `juicefs info` {#info}

显示指定路径或 inode 的内部信息。

#### 概览

```shell
juicefs info [command options] PATH or INODE

# 检查路径
juicefs info /mnt/jfs/foo

# 检查 inode
cd /mnt/jfs
juicefs info -i 100
```

#### 参数

|项 | 说明|
|-|-|
|`--inode, -i`|使用 inode 号而不是路径 (当前目录必须在 JuiceFS 挂载点内) (默认：false)|
|`--recursive, -r`|递归获取所有子目录的概要信息，当指定一个目录结构很复杂的路径时可能会耗时很长） (默认：false)|
|`--strict` <VersionAdd>1.1</VersionAdd>|获取准确的目录概要 (注意：巨大的文件树可能会花费很长的时间) (默认：false)|
|`--raw`|显示内部原始信息 (默认：false)|

### `juicefs debug` <VersionAdd>1.1</VersionAdd> {#debug}

从运行环境、系统日志等多个维度收集和展示信息，帮助更好地定位错误

#### 概览

```shell
juicefs debug [command options] MOUNTPOINT

# 收集并展示挂载点 /mnt/jfs 的各类信息
juicefs debug /mnt/jfs

# 指定输出目录为 /var/log
juicefs debug --out-dir=/var/log /mnt/jfs

# 收集最后 1000 条日志条目
juicefs debug --out-dir=/var/log --limit=1000 /mnt/jfs
```

#### 参数

|项 | 说明|
|-|-|
|`--out-dir=./debug/`|结果输出目录，若目录不存在则自动创建，默认为 `./debug/`。|
|`--limit=value`|收集的日志条目数，从新到旧，若不指定则收集全部条目|
|`--stats-sec=5`|.stats 文件采样秒数 (默认：5)|
|`--trace-sec=5`|trace 指标采样秒数 (默认：5)|
|`--profile-sec=30`|profile 指标采样秒数 (默认：30)|

### `juicefs summary` <VersionAdd>1.1</VersionAdd> {#summary}

显示目标目录树摘要。

#### 概览

```shell
juicefs summary [command options] PATH

juicefs summary /mnt/jfs/foo

# 显示最大深度为 5
juicefs summary --depth 5 /mnt/jfs/foo

# 显示前 20 个 entry
juicefs summary --entries 20 /mnt/jfs/foo

# 显示准确的结果
juicefs summary --strict /mnt/jfs/foo
```

#### 参数

|项 | 说明|
|-|-|
|`--depth value, -d value`|显示树的深度 (0 表示只显示根) (默认：2)|
|`--entries value, -e value`|显示前 N 个 entry (按大小排序)(默认：10)|
|`--strict`|显示准确的摘要，包括目录和文件 (可能很慢) (默认值：false)|
|`--csv`|以 CSV 格式打印摘要 (默认：false)|

## 服务 {#service}

### `juicefs mount` {#mount}

挂载一个已经创建的文件系统。

JuiceFS 支持用 root 以及普通用户挂载，但由于权限不同，挂载时所使用的的缓存目录和日志文件等路径会有所区别，详见下方参数说明。

#### 概要

```shell
juicefs mount [command options] META-URL MOUNTPOINT

# 前台挂载
juicefs mount redis://localhost /mnt/jfs

# 使用带密码的 redis 后台挂载
juicefs mount redis://:mypassword@localhost /mnt/jfs -d
# 更安全的方式
META_PASSWORD=mypassword juicefs mount redis://localhost /mnt/jfs -d

# 将一个子目录挂载为根目录
juicefs mount redis://localhost /mnt/jfs --subdir /dir/in/jfs

# 开启写缓存（writeback）模式，可以提升写入性能但同时有数据丢失风险
juicefs mount redis://localhost /mnt/jfs -d --writeback

# 开启只读模式
juicefs mount redis://localhost /mnt/jfs -d --read-only

# 关闭元数据自动备份
juicefs mount redis://localhost /mnt/jfs --backup-meta 0
```

#### 参数

|项 | 说明|
|-|-|
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
|`MOUNTPOINT`|文件系统挂载点，例如：`/mnt/jfs`、`Z:`。|
|`-d, --background`|后台运行，默认为 false。|
|`--no-syslog`|禁用系统日志，默认为 false。|
|`--log=path`|后台运行时日志文件的位置 (默认：`$HOME/.juicefs/juicefs.log` 或 `/var/log/juicefs.log`)|
|`--force`|强制挂载即使挂载点已经被相同的文件系统挂载 (默认值:false)|
|`--update-fstab` <VersionAdd>1.1</VersionAdd>|新增／更新 `/etc/fstab` 中的条目，如果不存在将会创建一个从 `/sbin/mount.juicefs` 到 JuiceFS 可执行文件的软链接，默认为 false。|

#### FUSE 相关参数 {#mount-fuse-options}

|项 | 说明|
|-|-|
|`--enable-xattr`|启用扩展属性 (xattr) 功能，默认为 false。|
|`--enable-ioctl` <VersionAdd>1.1</VersionAdd>|启用 ioctl (仅支持 GETFLAGS/SETFLAGS) (默认：false)|
|`--root-squash value` <VersionAdd>1.1</VersionAdd>|将本地 root 用户 (UID=0) 映射到一个指定用户，如 UID:GID|
|`--prefix-internal` <VersionAdd>1.1</VersionAdd>|挂载 JuiceFS 后，挂载点下默认创建 `.stats`, `.accesslog` 等虚拟文件。如果这些内部文件和你的应用发生冲突，可以启用该选项，添加 `.jfs` 前缀到所有内部文件。|
|`-o value`|其他 FUSE 选项，详见 [FUSE 挂载选项](../reference/fuse_mount_options.md)|

#### 元数据相关参数 {#mount-metadata-options}

|项 | 说明|
|-|-|
|`--subdir=value`|挂载指定的子目录，默认挂载整个文件系统。|
|`--backup-meta=3600`|自动备份元数据到对象存储的间隔时间；单位秒，默认 3600，设为 0 表示不备份。|
|`--backup-skip-trash` <VersionAdd>1.2</VersionAdd>|备份元数据时跳过回收站中的文件和目录。|
|`--heartbeat=12`|发送心跳的间隔（单位秒），建议所有客户端使用相同的心跳值 (默认：12)|
|`--read-only`|启用只读模式挂载。|
|`--no-bgjob`|禁用后台任务，默认为 false，也就是说客户端会默认运行后台任务。后台任务包含：<br/><ul><li>清理回收站中过期的文件（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `cleanupDeletedFiles` 和 `cleanupTrash`）</li><li>清理引用计数为 0 的 Slice（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `cleanupSlices`）</li><li>清理过期的客户端会话（在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go) 中搜索 `CleanStaleSessions`）</li></ul>特别地，与[企业版](https://juicefs.com/docs/zh/cloud/guide/background-job)不同，社区版碎片合并（Compaction）不受该选项的影响，而是随着文件读写操作，自动判断是否需要合并，然后异步执行（以 Redis 为例，在 [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/redis.go) 中搜索 `compactChunk`）|
|`--atime-mode=noatime` <VersionAdd>1.1</VersionAdd>|控制如何更新 atime（文件最后被访问的时间）。支持以下模式：<br/><ul><li>`noatime`（默认）：仅在文件创建和主动调用 `SetAttr` 时设置，平时访问与修改文件不影响 atime 值。考虑到更新 atime 需要运行额外的事务，对性能有影响，因此默认关闭。</li><li>`relatime`：仅在 mtime（文件内容修改时间）或 ctime（文件元数据修改时间）比 atime 新，或者 atime 超过 24 小时没有更新时进行更新。</li><li>`strictatime`：持续更新 atime</li></ul>|
|`--skip-dir-nlink value` <VersionAdd>1.1</VersionAdd>|跳过更新目录 nlink 前的重试次数 (仅用于 TKV, 0 代表永不跳过) (默认：20)|

#### 元数据缓存参数 {#mount-metadata-cache-options}

元数据缓存的介绍和使用，详见[「内核元数据缓存」](../guide/cache.md#kernel-metadata-cache)及[「客户端内存元数据缓存」](../guide/cache.md#client-memory-metadata-cache)。

|项 | 说明|
|-|-|
|`--attr-cache=1`|属性缓存过期时间；单位为秒，默认为 1。|
|`--entry-cache=1`|文件项缓存过期时间；单位为秒，默认为 1。|
|`--dir-entry-cache=1`|目录项缓存过期时间；单位为秒，默认为 1。|
|`--open-cache=0`|打开的文件的缓存过期时间，单位为秒，默认为 0，代表关闭该特性。|
|`--open-cache-limit=value` <VersionAdd>1.1</VersionAdd>|允许缓存的最大文件个数 (软限制，0 代表不限制) (默认：10000)|

#### 数据存储参数 {#mount-data-storage-options}

|项 | 说明|
|-|-|
|`--storage=file`|对象存储类型 (例如 `s3`、`gs`、`oss`、`cos`) (默认：`"file"`，参考[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)|
|`--bucket=value`|为当前挂载点指定访问对象存储的 Endpoint。|
|`--storage-class value` <VersionAdd>1.1</VersionAdd>|当前客户端写入数据的存储类型|
|`--get-timeout=60`|下载一个对象的超时时间；单位为秒 (默认：60)|
|`--put-timeout=60`|上传一个对象的超时时间；单位为秒 (默认：60)|
|`--io-retries=10`|网络异常时的重试次数 (默认：10)|
|`--max-uploads=20`|上传并发度，默认为 20。对于粒度为 4M 的写入模式，20 并发已经是很高的默认值，在这样的写入模式下，提高写并发往往需要伴随增大 `--buffer-size`, 详见「[读写缓冲区](../guide/cache.md#buffer-size)」。但面对百 K 级别的小随机写，并发量大的时候很容易产生阻塞等待，造成写入速度恶化。如果无法改善应用写模式，对其进行合并，那么需要考虑采用更高的写并发，避免排队等待。|
|`--max-deletes=10`|删除对象的连接数 (默认：10)|
|`--upload-limit=0`|上传带宽限制，单位为 Mbps (默认：0)|
|`--download-limit=0`|下载带宽限制，单位为 Mbps (默认：0)|

#### 数据缓存相关参数 {#mount-data-cache-options}

|项 | 说明|
|-|-|
|`--buffer-size=300`|读写缓冲区的总大小；单位为 MiB (默认：300)。阅读[「读写缓冲区」](../guide/cache.md#buffer-size)了解更多。|
|`--prefetch=1`|并发预读 N 个块 (默认：1)。阅读[「客户端读缓存」](../guide/cache.md#client-read-cache)了解更多。|
|`--writeback`|后台异步上传对象，默认为 false。阅读[「客户端写缓存」](../guide/cache.md#client-write-cache)了解更多。|
|`--upload-delay=0`|启用 `--writeback` 后，可以使用该选项控制数据延迟上传到对象存储，默认为 0 秒，相当于写入后立刻上传。该选项也支持 `s`（秒）、`m`（分）、`h`（时）这些单位。如果在等待的时间内数据被应用删除，则无需再上传到对象存储。如果数据只是临时落盘，可以考虑用该选项节约资源。阅读[「客户端写缓存」](../guide/cache.md#client-write-cache)了解更多。|
|`--cache-dir=value`|本地缓存目录路径；使用 `:`（Linux、macOS）或 `;`（Windows）隔离多个路径 (默认：`$HOME/.juicefs/cache` 或 `/var/jfsCache`)。阅读[「客户端读缓存」](../guide/cache.md#client-read-cache)了解更多。|
|`--cache-mode value` <VersionAdd>1.1</VersionAdd>|缓存块的文件权限 (默认："0600")|
|`--cache-size=102400`|缓存对象的总大小；单位为 MiB (默认：102400)。阅读[「客户端读缓存」](../guide/cache.md#client-read-cache)了解更多。|
|`--free-space-ratio=0.1`|最小剩余空间比例，默认为 0.1。如果启用了[「客户端写缓存」](../guide/cache.md#client-write-cache)，则该参数还控制着写缓存占用空间。阅读[「客户端读缓存」](../guide/cache.md#client-read-cache)了解更多。|
|`--cache-partial-only`|仅缓存随机小块读，默认为 false。阅读[「客户端读缓存」](../guide/cache.md#client-read-cache)了解更多。|
|`--verify-cache-checksum=full` <VersionAdd>1.1</VersionAdd>|缓存数据一致性检查级别，启用 Checksum 校验后，生成缓存文件时会对数据切分做 Checksum 并记录于文件末尾，供读缓存时进行校验。支持以下级别：<br/><ul><li>`none`：禁用一致性检查，如果本地数据被篡改，将会读到错误数据；</li><li>`full`（默认）：读完整数据块时才校验，适合顺序读场景；</li><li>`shrink`：对读范围内的切片数据进行校验，校验范围不包含读边界所在的切片（可以理解为开区间），适合随机读场景；</li><li>`extend`：对读范围内的切片数据进行校验，校验范围同时包含读边界所在的切片（可以理解为闭区间），因此将带来一定程度的读放大，适合对正确性有极致要求的随机读场景。</li></ul>|
|`--cache-eviction value` <VersionAdd>1.1</VersionAdd>|缓存逐出策略 (none 或 2-random) (默认值："2-random")|
|`--cache-scan-interval value` <VersionAdd>1.1</VersionAdd>|扫描缓存目录重建内存索引的间隔 (以秒为单位) (默认："3600")|

#### 监控相关参数 {#mount-metrics-options}

|项 | 说明|
|-|-|
|`--metrics=127.0.0.1:9567`|监控数据导出地址，默认为 `127.0.0.1:9567`。|
|`--custom-labels`|监控指标自定义标签，格式为 `key1:value1;key2:value2` (默认："")|
|`--consul=127.0.0.1:8500`|Consul 注册中心地址，默认为 `127.0.0.1:8500`。|
|`--no-usage-report`|不发送使用量信息 (默认：false)|

### `juicefs umount` {#umount}

卸载 JuiceFS 文件系统。

#### 概要

```shell
juicefs umount [command options] MOUNTPOINT

juicefs umount /mnt/jfs
```

#### 参数

|项 | 说明|
|-|-|
|`-f, --force`|强制卸载一个忙碌的文件系统 (默认：false)|
|`--flush` <VersionAdd>1.1</VersionAdd>|等待所有暂存块被刷新 (默认值：false)|

### `juicefs gateway` {#gateway}

启动一个 S3 兼容的网关，详见[「配置 JuiceFS S3 网关」](../deployment/s3_gateway.md)。

#### 概览

```shell
juicefs gateway [command options] META-URL ADDRESS

export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
juicefs gateway redis://localhost localhost:9000
```

#### 参数

除下方列出的参数，该命令还与 `juicefs mount` 共享参数，因此需要结合 [`mount`](#mount) 一起参考。

|项 | 说明|
|-|-|
| `--log value`<VersionAdd>1.2</VersionAdd>      | 网关日志路径                                                                                   |
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
| `--background, -d`<VersionAdd>1.2</VersionAdd> | 后台运行 (默认：false)                                                                    |
|`ADDRESS`|S3 网关地址和监听的端口，例如：`localhost:9000`|
|`--access-log=path`|访问日志的路径|
|`--no-banner`|禁用 MinIO 的启动信息 (默认：false)|
|`--multi-buckets`|使用第一级目录作为存储桶 (默认：false)|
|`--keep-etag`|保留对象上传时的 ETag (默认：false)|
|`--umask=022`|新文件和新目录的 umask 的八进制格式 (默认值：022)|
| `--domain value`<VersionAdd>1.2</VersionAdd>   |虚拟主机样式请求的域|

### `juicefs webdav` {#webdav}

启动一个 WebDAV 服务，阅读[「配置 WebDAV 服务」](../deployment/webdav.md)以了解更多。

#### 概览

```shell
juicefs webdav [command options] META-URL ADDRESS

juicefs webdav redis://localhost localhost:9007
```

#### 参数

除下方列出的参数，该命令还与 `juicefs mount` 共享参数，因此需要结合 [`mount`](#mount) 参数一起参考。

|项 | 说明|
|-|-|
|`META-URL`|用于元数据存储的数据库 URL，详情查看[「JuiceFS 支持的元数据引擎」](../reference/how_to_set_up_metadata_engine.md)。|
|`ADDRESS`|WebDAV 服务监听的地址与端口，例如：`localhost:9007`|
|`--cert-file` <VersionAdd>1.1</VersionAdd>|HTTPS 证书文件|
|`--key-file` <VersionAdd>1.1</VersionAdd>|HTTPS 密钥文件|
|`--gzip`|通过 gzip 压缩提供的文件（默认值：false）|
|`--disallowList`|禁止列出目录（默认值：false）|
| `--log value`<VersionAdd>1.2</VersionAdd>      | WebDAV 日志路径                                                                              |
|`--access-log=path`|访问日志的路径|
| `--background, -d`<VersionAdd>1.2</VersionAdd> | 后台运行 (默认：false)                                                                          |

## 工具 {#tool}

### `juicefs bench` {#bench}

对指定的路径做基准测试，包括对大文件和小文件的读/写/获取属性操作。详细介绍参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-bench)。

#### 概览

```shell
juicefs bench [command options] PATH

# 使用4个线程运行基准测试
juicefs bench /mnt/jfs -p 4

# 只运行小文件的基准测试
juicefs bench /mnt/jfs --big-file-size 0
```

#### 参数

|项 | 说明|
|-|-|
|`--block-size=1`|块大小；单位为 MiB (默认：1)|
|`--big-file-size=1024`|大文件大小；单位为 MiB (默认：1024)|
|`--small-file-size=0.1`|小文件大小；单位为 MiB (默认：0.1)|
|`--small-file-count=100`|小文件数量 (默认：100)|
|`--threads=1, -p 1`|并发线程数 (默认：1)|

### `juicefs objbench` {#objbench}

测试对象存储接口的正确性与基本性能，详细介绍参考[文档](../benchmark/performance_evaluation_guide.md#juicefs-objbench)。

#### 概览

```shell
juicefs objbench [command options] BUCKET

# 测试 S3 对象存储的基准性能
ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage=s3 https://mybucket.s3.us-east-2.amazonaws.com -p 6
```

#### 参数

|项 | 说明|
|-|-|
|`--storage=file`|对象存储类型 (例如 `s3`、`gs`、`oss`、`cos`) (默认：`file`，参考[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)查看所有支持的对象存储类型)|
|`--access-key=value`|对象存储的 Access Key，也可通过环境变量 `ACCESS_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--secret-key=value`|对象存储的 Secret Key，也可通过环境变量 `SECRET_KEY` 设置。查看[如何设置对象存储](../reference/how_to_set_up_object_storage.md#aksk)以了解更多。|
|`--block-size=4096`|每个 IO 块的大小（以 KiB 为单位）（默认值：4096）|
|`--big-object-size=1024`|大文件的大小（以 MiB 为单位）（默认值：1024）|
|`--small-object-size=128`|每个小文件的大小（以 KiB 为单位）（默认值：128）|
|`--small-objects=100`|小文件的数量（默认值：100）|
|`--skip-functional-tests`|跳过功能测试（默认值：false）|
|`--threads=4, -p 4`|上传下载等操作的并发数（默认值：4）|

### `juicefs warmup` {#warmup}

将文件提前下载到缓存，提升后续本地访问的速度。可以指定某个挂载点路径，递归对这个路径下的所有文件进行缓存预热；也可以通过 `--file` 选项指定文本文件，在文本文件中指定需要预热的文件名。

如果需要预热的文件分布在许多不同的目录，推荐将这些文件名保存到文本文件中并用 `--file` 参数传给预热命令，这样做能利用 `warmup` 的并发功能，速度会显著优于多次调用 `juicefs warmup`，在每次调用里传入单个文件。

#### 概览

```shell
juicefs warmup [command options] [PATH ...]

# 预热目录中的所有文件
juicefs warmup /mnt/jfs/datadir

# 只预热指定文件
echo '/jfs/f1
/jfs/f2
/jfs/f3' > /tmp/filelist.txt
juicefs warmup -f /tmp/filelist.txt
```

#### 参数

|项 | 说明|
|-|-|
|`--file=value, -f value`|指定一个包含一组路径的文件（每一行为一个文件路径）。|
|`--threads=50, -p 50`|并发的工作线程数，默认 50。如果带宽不足导致下载失败，需要减少并发度，控制下载速度。|
|`--background, -b`|后台运行（默认：false）|

### `juicefs rmr` {#rmr}

快速删除目录里的所有文件和子目录，效果等同于 `rm -rf`，但该命令直接操纵元数据，不经过内核，所以速度更快。

如果文件系统启用了回收站功能，被删除的文件会进入回收站。详见[「回收站」](../security/trash.md)。

#### 概览

```shell
juicefs rmr PATH ...

juicefs rmr /mnt/jfs/foo
```

### `juicefs sync` {#sync}

在两个存储之间同步数据，阅读[「数据同步」](../guide/sync.md)以了解更多。

#### 概览

```shell
juicefs sync [command options] SRC DST

# 从 OSS 同步到 S3
juicefs sync oss://mybucket.oss-cn-shanghai.aliyuncs.com s3://mybucket.s3.us-east-2.amazonaws.com

# 从 S3 直接同步到 JuiceFS
juicefs sync s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# 源端: a1/b1,a2/b2,aaa/b1   目标端: empty   同步结果: aaa/b1
juicefs sync --exclude='a?/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# 源端: a1/b1,a2/b2,aaa/b1   目标端: empty   同步结果: a1/b1,aaa/b1
juicefs sync --include='a1/b1' --exclude='a[1-9]/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# 源端: a1/b1,a2/b2,aaa/b1,b1,b2  目标端: empty   同步结果: b2
juicefs sync --include='a1/b1' --exclude='a*' --include='b2' --exclude='b?' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/
```

源路径（`SRC`）和目标路径（`DST`）的格式均为：

```
[NAME://][ACCESS_KEY:SECRET_KEY[:SESSIONTOKEN]@]BUCKET[.ENDPOINT][/PREFIX]
```

其中：

- `NAME`：JuiceFS 支持的数据存储类型，比如 `s3`、`oss`，完整列表见[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)。
- `ACCESS_KEY` 和 `SECRET_KEY`：访问数据存储所需的密钥信息，参考[文档](../reference/how_to_set_up_object_storage.md#aksk)。
- `TOKEN` 用来访问对象存储的 token，部分对象存储支持使用临时的 token 以获得有限时间的权限
- `BUCKET[.ENDPOINT]`：数据存储服务的访问地址，不同存储类型格式可能不同，详见[文档](../reference/how_to_set_up_object_storage.md#supported-object-storage)。
- `[/PREFIX]`：可选，源路径和目标路径的前缀，可用于限定只同步某些路径中的数据。

#### 选择条件相关参数 {#sync-selection-related-options}

|项 | 说明|
|-|-|
|`--start=KEY, -s KEY, --end=KEY, -e KEY`|提供 KEY 范围，来指定对象存储的 List 范围。|
|`--exclude=PATTERN`|排除匹配 `PATTERN` 的 Key。|
|`--include=PATTERN`|不排除匹配 `PATTERN` 的 Key，需要与 `--exclude` 选项配合使用。|
|`--limit=-1`|限制将要处理的对象的数量，默认为 -1 表示不限制|
|`--update, -u`|当源文件更新时（`mtime` 更新），覆盖已存在的文件，默认为 false。|
|`--force-update, -f`|强制覆盖已存在的文件，默认为 false。|
|`--existing, --ignore-non-existing` <VersionAdd>1.1</VersionAdd>|不创建任何新文件，默认为 false。|
|`--ignore-existing` <VersionAdd>1.1</VersionAdd>|不更新任何已经存在的文件，默认为 false。|

#### 行为相关参数 {#sync-action-related-options}

|项 | 说明|
|-|-|
|`--dirs`|同步目录（包括空目录）。|
|`--perms`|保留权限设置，默认为 false。|
|`--links, -l`|将符号链接复制为符号链接，默认为 false，此时会查找并同步符号链接所指向的文件。|
|`--delete-src, --deleteSrc`|如果目标存储已经存在，删除源存储的对象。与 rsync 不同，为保数据安全，首次执行时不会删除源存储文件，只有拷贝成功后再次运行时，扫描确认目标存储已经存在相关文件，才会删除源存储文件。|
|`--delete-dst, --deleteDst`|删除目标存储下的不相关对象。|
|`--check-all`|校验源路径和目标路径中所有文件的数据完整性，默认为 false。校验方式是基于字节流对比，因此也将带来相应的开销。|
|`--check-new`|校验新拷贝文件的数据完整性，默认为 false。校验方式是基于字节流对比，因此也将带来相应的开销。|
|`--dry`|仅打印执行计划，不实际拷贝文件。|

#### 对象存储相关参数 {#sync-storage-related-options}

|项 | 说明|
|-|-|
|`--threads=10, -p 10`|并发线程数，默认为 10。|
|`--list-threads=1` <VersionAdd>1.1</VersionAdd>|并发 `list` 线程数，默认为 1。阅读[并发 `list`](../guide/sync.md#concurrent-list)以了解如何使用。|
|`--list-depth=1` <VersionAdd>1.1</VersionAdd>|并发 `list` 目录深度，默认为 1。阅读[并发 `list`](../guide/sync.md#concurrent-list)以了解如何使用。|
|`--no-https`|不要使用 HTTPS，默认为 false。|
|`--storage-class value` <VersionAdd>1.1</VersionAdd>|目标端的新建文件的存储类型。|
|`--bwlimit=0`|限制最大带宽，单位 Mbps，默认为 0 表示不限制。|

#### 分布式相关参数 {#sync-cluster-related-options}

|项 | 说明 |
|-|-|
|`--manager-addr=ADDR`| 分布式同步模式中，Manager 节点的监听地址，格式：`<IP>:[port]`，如果不写端口，则监听随机端口。如果没有该参数，则监听本机随机的 IPv4 地址与随机端口。|
|`--worker=ADDR,ADDR`| 分布式同步模式中，工作节点列表，使用逗号分隔。|

### `juicefs clone` <VersionAdd>1.1</VersionAdd> {#clone}

快速在同一挂载点下克隆目录或者文件，只拷贝元数据但不拷贝数据块，因此拷贝速度非常快。更多介绍详见[「克隆文件或目录」](../guide/clone.md)。

#### 概览

```shell
juicefs clone [command options] SRC DST

# 克隆文件
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# 克隆目录
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# 克隆时保留文件的 UID、GID 和 mode
juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2
```

#### 参数

|项 | 说明|
|-|-|
|`--preserve, -p`|克隆时默认使用当前用户的 UID 和 GID，而 mode 则使用当前用户的 umask 重新计算获得。如果启用该选项，则保留文件的 UID、GID 和 mode。|
