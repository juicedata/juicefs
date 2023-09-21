---
title: 数据同步
sidebar_position: 7
---

[`juicefs sync`](../reference/command_reference.md#sync) 是强大的数据同步工具，可以在所有支持的存储之间并发同步或迁移数据，包括对象存储、JuiceFS、本地文件系统，你可以在这三者之间以任意方向和搭配进行数据同步。除此之外，还支持同步通过 SSH 访问远程目录、HDFS、WebDAV 等，同时提供增量同步、模式匹配（类似 rsync）、分布式同步等高级功能。

## 基本用法

### 命令格式

```shell
juicefs sync [command options] SRC DST
```

其中：

- `SRC` 代表数据源地址及路径
- `DST` 代表目标地址及路径
- `[command options]` 代表可选的同步选项，详情查看[命令参考](../reference/command_reference.md#sync)。

地址格式均为：

```shell
[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]

# 特别地，MinIO 目前仅支持路径风格
minio://[ACCESS_KEY:SECRET_KEY[:TOKEN]@]ENDPOINT/BUCKET[/PREFIX]
```

其中：

- `NAME` 是存储类型，比如 `s3`、`oss`。详情查看[所有支持的存储服务](../reference/how_to_set_up_object_storage.md#supported-object-storage)
- `ACCESS_KEY` 和 `SECRET_KEY` 是对象存储的 API 访问密钥，如果包含了特殊字符，则需要手动转义并替换，比如 `/` 需要被替换为其转义符 `%2F`
- `TOKEN` 用来访问对象存储的 token，部分对象存储支持使用临时的 token 以获得有限时间的权限
- `BUCKET[.ENDPOINT]` 是对象存储的访问地址
- `PREFIX` 是可选的，限定要同步的目录名前缀。

以下是一个 Amazon S3 对象存储的地址范例：

```
s3://ABCDEFG:HIJKLMN@myjfs.s3.us-west-1.amazonaws.com
```

特别地，`SRC` 和 `DST` 如果以 `/` 结尾将被视为目录，例如：`movies/`。没有以 `/` 结尾则会被视为「前缀」，将按照前缀匹配的规则进行匹配，例如，当前目录下有 `test` 和 `text` 两个目录，使用以下命令可以将它们同步到目标路径 `~/mnt/`：

```shell
juicefs sync ./te ~/mnt/te
```

使用这种方式，`sync` 命令会以 `te` 前缀匹配当前路径下所有包含该前缀的目录或文件，即 `test` 和 `text`。而目标路径 `~/mnt/te` 中的 `te` 也是前缀，它会替换所有同步过来的目录和文件的前缀，在此示例中是将 `te` 替换为 `te`，即保持前缀不变。如果调整目标路径的前缀，例如将目标前缀改为 `ab`：

```shell
juicefs sync ./te ~/mnt/ab
```

目标路径中同步来的 `test` 目录名会变成 `abst`，`text` 会变成 `abxt`。

### 资源清单 {#required-storages}

这里假设有以下存储资源：

1. **对象存储 A**
   - Bucket 名：aaa
   - Endpoint：`https://aaa.s3.us-west-1.amazonaws.com`

2. **对象存储 B**
   - Bucket 名：bbb
   - Endpoint：`https://bbb.oss-cn-hangzhou.aliyuncs.com`

3. **JuiceFS 文件系统**
   - 元数据存储：`redis://10.10.0.8:6379/1`
   - 对象存储：`https://ccc-125000.cos.ap-beijing.myqcloud.com`

所有存储的**访问密钥**均为：

- **ACCESS_KEY**：`ABCDEFG`
- **SECRET_KEY**：`HIJKLMN`

### 对象存储与 JuiceFS 之间同步

将 [对象存储 A](#required-storages) 的 `movies` 目录同步到 [JuiceFS 文件系统](#required-storages)：

```shell
# 挂载 JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

将 [JuiceFS 文件系统](#required-storages) 的 `images` 目录同步到 [对象存储 A](#required-storages)：

```shell
# 挂载 JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
juicefs sync /mnt/jfs/images/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/images/
```

### 对象存储与对象存储之间同步

将 [对象存储 A](#required-storages) 的全部数据同步到 [对象存储 B](#required-storages)：

```shell
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

### 无挂载点同步 <VersionAdd>1.1</VersionAdd>

在两个存储系统之间同步数据，如果其中一方是 JuiceFS，推荐直接使用 `jfs://` 协议头，而不是先挂载 JuiceFS，再访问本地目录。这样便能跳过挂载点，直接读取或写入数据，在大规模场景下，绕过 FUSE 挂载点将能节约资源开销以及提升数据同步性能。

```shell
myfs=redis://10.10.0.8:6379/1 juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ jfs://myfs/movies/
```

## 高级用法

### 观测 {#observation}

简单来说，用 `sync` 命令拷贝大文件时，进度条可能会迟迟不更新，如果担心命令未能正常工作，可以用其他手段对传输情况进行观测。

`sync` 假定了使用场景是拷贝大量文件，因此进度的计算也是针对多个文件设计的：每一个文件完成了拷贝后，进度会更新一次。因此如果面对的都是大文件，单个文件的拷贝速度太慢，进度条就会变化缓慢，或者呈现卡死的状态。如果目的地端协议不支持 multipart upload（比如 `file`、`sftp`、`jfs`、`gluster` 协议头），单个文件会单线程进行复制，无法对大文件进行并发上传。可想而知，大文件 + 不支持 multipart upload，将会更容易出现进度条卡死的情况。

如果你观察到进度不再变化，参考下列手段进行观测和排查：

* 如果数据同步的两方有任何一个是 JuiceFS 宿主机挂载点，可以用 [`juicefs stats`](../administration/fault_diagnosis_and_analysis.md#stats) 快速查看文件系统是否正在写入（或读出）。
* 如果数据同步的目的地是宿主机本地盘，可以直接观察目录下是否存在名称中带 `.tmp.xxx` 后缀的临时文件，`sync` 过程中会将传输结果写入临时文件，待传输完成后进行重命名，才完成最终的写入。观察临时文件大小是否变化，就能确定当前的写入状况。
* 如果传输目的地均为对象存储，可以通过类似 `nethogs` 的命令，查看出入网流量，来判断传输进展。

### 增量同步与全量同步 {#incremental-and-full-synchronization}

`juicefs sync` 默认以增量同步方式工作，对于已存在的文件，仅在文件大小不一样时，才再次同步进行覆盖。在此基础上，还可以指定 [`--update`](../reference/command_reference.md#sync)，在源文件 `mtime` 更新时进行覆盖。如果你的场景对正确性有着极致要求，可以指定 [`--check-new`](../reference/command_reference.md#sync) 或 [`--check-all`](../reference/command_reference.md#sync)，来对两边的文件进行字节流比对，确保数据一致。

如需全量同步，即不论目标路径上是否存在相同的文件都重新同步，可以使用 `--force-update` 或 `-f`。例如，将 [对象存储 A](#required-storages) 的 `movies` 目录全量同步到 [JuiceFS 文件系统](#required-storages)：

```shell
# 挂载 JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行全量同步
juicefs sync --force-update s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

## 模式匹配 {#pattern-matching}

类似 rsync，你可以用 `--exclude`、`--include` 来过滤需要同步的文件，并通过多个规则的组合实现任意集合的同步，规则如下：

- 以 `/` 结尾的模式会仅匹配目录，否则会匹配文件、链接或设备；
- 包含 `*`、`?` 或 `[` 字符时会以通配符模式匹配，否则按照常规字符串匹配；
- `*` 匹配任意非空路径组件，在 `/` 处停止匹配；
- `?` 匹配除 `/` 外的任意字符；
- `[` 匹配一组字符集合，例如 `[a-z]` 或 `[[:alpha:]]`；
- 在通配符模式中，反斜杠可以用来转义通配符，但在没有通配符的情况下，会按字面意思匹配；
- 在使用包含／排除规则时，位置在前的选项优先级更高。`--include` 应该排在前面，如果先设置 `--exclude '*'` 排除了所有文件，那么后面的 `--include 'xxx'` 包含规则就不会生效。

例如，同步所有文件，但排除隐藏文件（以 `.` 为开头命名均被视为隐藏文件）：

```shell
# 排除隐藏文件
juicefs sync --exclude '.*' /tmp/dir/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

重复该选项匹配更多规则，例如，排除所有隐藏文件、`pic/` 目录 和 `4.png`：

```shell
juicefs sync --exclude '.*' --exclude 'pic/' --exclude '4.png' /tmp/dir/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

使用 `--include` 选项设置要包含的目录或文件，例如，只同步 `pic/` 和 `4.png` 两个文件，其他文件都排除：

```shell
juicefs sync --include 'pic/' --include '4.png' --exclude '*' /tmp/dir/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

### 目录结构与文件权限

默认情况下，sync 命令只同步文件对象以及包含文件对象的目录，空目录不会被同步。如需同步空目录，可以使用 `--dirs` 选项。

另外，在 local、SFTP、HDFS 等文件系统之间同步时，如需保持文件权限，可以使用 `--perms` 选项。

### 拷贝符号链接

JuiceFS `sync` 在**本地目录之间**同步时，支持通过设置 `--links` 选项开启遇到符号链时同步其自身而不是其指向的对象的功能。同步后的符号链接指向的路径为源符号链接中存储的原始路径，无论该路径在同步前后是否可达都不会被转换。

另外需要注意的几个细节

1. 符号链接自身的 `mtime` 不会被拷贝；
1. `--check-new` 和 `--perms` 选项的行为在遇到符号链接时会被忽略。

## 并发同步 {#concurrent-sync}

`juicefs sync` 默认启用 10 个线程执行同步任务，可以根据需要设置 `--threads` 选项调大或减少线程数。但也要注意，受限于各种因素，一味增加 `--threads` 未必能持续提升同步速度，你还需要考虑：

* `SRC` 和 `DST` 的存储系统是否已经达到了带宽上限，如果其中一个对象存储已经到达带宽限制，同步的瓶颈就在这里，继续增加并发度也不会继续提升同步速度；
* 单机资源是否吃紧，比如 CPU、网卡拥堵。如果同步受限于单机资源，考虑使用[分布式同步](#distributed-sync)，在下方小节继续介绍；
* 如果同步的数据以小文件为主，并且 `SRC` 的存储系统的 `list` API 性能极佳，那么 `juicefs sync` 默认的单线程 `list` 可能会成为瓶颈。此时考虑启用[并发 `list`](#concurrent-list) 操作，在下方小节继续介绍。

### 并发 `list` {#concurrent-list}

在 `juicefs sync` 命令的输出中，关注 `Pending objects` 的数量，如果该值持续为 0，说明消费速度大于生产，可以增大 `--list-threads` 来启用并发 `list`，以及用 `--list-depth` 来控制并发 `list` 的目录深度。

比方说，如果你面对的是 JuiceFS 所使用的对象存储服务，那么目录结构为 `/<vol-name>/chunks/xxx/xxx/...`，对于这样的目录结构，使用 `--list-depth=2` 来实现对于 `/<vol-name>/chunks` 的并发列表操作，是比较合适的选择。

### 分布式同步 {#distributed-sync}

在两个对象存储之间同步数据，就是从一端拉取数据再推送到另一端，同步的效率取决于客户端与云之间的带宽：

![JuiceFS-sync-single](../images/juicefs-sync-single.png)

在同步大量数据时，单机带宽往往会被占满出现瓶颈，针对这种情况，JuiceFS Sync 提供多机并发同步支持，如下图。

![JuiceFS-sync-worker](../images/juicefs-sync-worker.png)

Manager 作为主控执行 `sync` 命令，通过 `--worker` 参数定义多个 Worker 主机，JuiceFS 会根据 Worker 的总数量，动态拆分同步的工作量并分发给各个主机同时执行。即把原本在一台主机上处理的同步任务量拆分成多份，分发到多台主机上同时处理，单位时间内能处理的数据量更大，总带宽也成倍增加。

在配置多机并发同步任务时，需要提前配置好 Manager 节点到 Worker 节点的 SSH 免密登录，如果 Worker 节点的 SSH 端口不是默认的 22，请在 Manager 节点的 `~/.ssh/config` 设置其端口号。Manager 会将 JuiceFS 客户端程序分发到 Worker 节点，为避免兼容性问题，Manager 和 Worker 应使用相同类型和架构的操作系统。

例如，将 [对象存储 A](#required-storages) 同步到 [对象存储 B](#required-storages)，采用多主机并行同步：

```shell
juicefs sync --worker bob@192.168.1.20,tom@192.168.8.10 s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

当前主机与两个 Worker 主机 `bob@192.168.1.20` 和 `tom@192.168.8.10` 将共同分担两个对象存储之间的数据同步任务。

## 场景应用

### 数据异地容灾备份

异地容灾备份针对的是文件本身，因此应将 JuiceFS 中存储的文件同步到其他的对象存储，例如，将 [JuiceFS 文件系统](#required-storages) 中的文件同步到 [对象存储 A](#required-storages)：

```shell
# 挂载 JuiceFS
juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
juicefs sync /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

同步以后，在 [对象存储 A](#required-storages) 中可以直接看到所有的文件。

### 建立 JuiceFS 数据副本

与面向文件本身的容灾备份不同，建立 JuiceFS 数据副本的目的是为 JuiceFS 的数据存储建立一个内容和结构完全相同的镜像，当使用中的对象存储发生了故障，可以通过修改配置切换到数据副本继续工作。需要注意这里仅复制了 JuiceFS 文件系统的数据，并没有复制元数据，元数据引擎的数据备份依然需要。

这需要直接操作 JuiceFS 底层的对象存储，将它与目标对象存储之间进行同步。例如，要把 [对象存储 B](#required-storages) 作为 [JuiceFS 文件系统](#required-storages) 的数据副本：

```shell
juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

同步以后，在 [对象存储 B](#required-storages) 中看到的与 [JuiceFS 使用的对象存储](#required-storages) 中的内容和结构完全一样。
