---
sidebar_label: 数据同步
position: 4
---

# 使用 JuiceFS Sync 跨云迁移和同步数据

JuiceFS 的 `sync` 子命令是功能完整的数据同步实用工具，可以在所有 [JuiceFS 支持的对象存储](../guide/how_to_set_up_object_storage.md)之间多线程并发同步或迁移数据，既支持在「对象存储」与「JuiceFS」之间迁移数据，也支持在「对象存储」与「对象存储」之间跨云跨区迁移数据。与 rsync 类似，除了对象存储也支持同步本地目录、通过 SSH 访问远程目录、HDFS、WebDAV 等，同时提供全量同步、增量同步、条件模式匹配等高级功能。

## 基本用法

### 命令格式

```shell
juicefs sync [command options] SRC DST
```

即把 `SRC` 同步到 `DST`，既可以同步目录，也可以同步文件。

其中：

- `SRC` 代表数据源地址及路径
- `DST` 代表目标地址及路径
- `[command options]` 代表可选的同步选项，详情查看[命令参考](../reference/command_reference.md#juicefs-sync)。

地址格式均为 `[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]`

:::tip 提示
minio 目前仅支持路径风格，地址格式为 `minio://[ACCESS_KEY:SECRET_KEY@]ENDPOINT/BUCKET[/PREFIX]`
:::

其中：

- `NAME` 是存储类型，比如 `s3`、`oss`。详情查看[所有支持的存储服务](../guide/how_to_set_up_object_storage.md#支持的存储服务)
- `ACCESS_KEY` 和 `SECRET_KEY` 是对象存储的 API 访问密钥，如果包含了特殊字符，则需要手动转义并替换，比如 `/` 需要被替换为其转义符 `%2F`
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

### 资源清单

这里假设有以下存储资源：

1. **对象存储 A** <span id="bucketA" />
   - Bucket 名：aaa
   - Endpoint：`https://aaa.s3.us-west-1.amazonaws.com`

2. **对象存储 B** <span id="bucketB" />
   - Bucket 名：bbb
   - Endpoint：`https://bbb.oss-cn-hangzhou.aliyuncs.com`

3. **JuiceFS 文件系统** <span id="bucketC" />
   - 元数据存储：`redis://10.10.0.8:6379/1`
   - 对象存储：`https://ccc-125000.cos.ap-beijing.myqcloud.com`

所有存储的**访问密钥**均为：

- **ACCESS_KEY**：`ABCDEFG`
- **SECRET_KEY**：`HIJKLMN`

### 对象存储与 JuiceFS 之间同步

将 [对象存储 A](#bucketA) 的 `movies` 目录同步到 [JuiceFS 文件系统](#bucketC)：

```shell
# 挂载 JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

将 [JuiceFS 文件系统](#bucketC) 的 `images` 目录同步到 [对象存储 A](#bucketA)：

```shell
# 挂载 JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
juicefs sync /mnt/jfs/images/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/images/
```

### 对象存储与对象存储之间同步

将 [对象存储 A](#bucketA) 的全部数据同步到 [对象存储 B](#bucketB)：

```shell
juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

## 高级用法

### 增量同步与全量同步

sync 命令默认以增量同步方式工作，即先对比源路径与目标路径之间的差异，然后仅同步有差异的部分。可以使用 `--update` 或 `-u` 选项更新文件的 `mtime`。

如需全量同步，即不论目标路径上是否存在相同的文件都重新同步，可以使用 `--force-update` 或 `-f`。例如，将 [对象存储 A](#bucketA) 的 `movies` 目录全量同步到 [JuiceFS 文件系统](#bucketC)：

```shell
# 挂载 JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行全量同步
juicefs sync --force-update s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies/ /mnt/jfs/movies/
```

### 模式匹配

`sync` 命令的模式匹配功能跟 rsync 类似，可以通过规则排除或包含某类文件，并通过多个规则的组合实现任意集合的同步，规则如下：

- 以 `/` 结尾的模式会仅匹配目录，否则会匹配文件、链接或设备；
- 包含 `*`、`?` 或 `[` 字符时会以通配符模式匹配，否则按照常规字符串匹配；
- `*` 匹配任意非空路径组件，在 `/` 处停止匹配；
- `?` 匹配除 `/` 外的任意字符；
- `[` 匹配一组字符集合，例如 `[a-z]` 或 `[[:alpha:]]`；
- 在通配符模式中，反斜杠可以用来转义通配符，但在没有通配符的情况下，会按字面意思匹配；
- 始终以模式作为前缀递归匹配。

#### 排除文件／目录

使用 `--exclude` 选项设置要排除的目录或文件。例如，将 [JuiceFS 文件系统](#bucketC) 完整同步到[对象存储 A](#bucketA)，但不同步隐藏的文件和文件夹：

:::note 备注
在 Linux 系统中所有以 `.` 开始的名称均被视为隐藏文件
:::

```shell
# 挂载 JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 完整同步，排除隐藏文件和目录
juicefs sync --exclude '.*' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

可以重复该选项匹配更多规则，例如，排除所有隐藏文件、`pic/` 目录 和 `4.png` 文件：

```shell
juicefs sync --exclude '.*' --exclude 'pic/' --exclude '4.png' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

#### 包含文件／目录

使用 `--include` 选项设置要包含（不被排除）的目录或文件，例如，只同步 `pic/` 和 `4.png` 两个文件，其他文件都排除：

```shell
juicefs sync --include 'pic/' --include '4.png' --exclude '*' /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

:::info 注意
在使用包含／排除规则时，位置在前的选项优先级更高。`--include` 应该排在前面，如果先设置 `--exclude '*'` 排除了所有文件，那么后面的 `--include 'pic/' --include '4.png'` 包含规则就不会生效。
:::

### 多线程和带宽限制

JuiceFS `sync` 默认启用 10 个线程执行同步任务，可以根据需要设置 `--threads` 选项调大或减少线程数。

另外，如果需要限制同步任务占用的带宽，可以设置 `--bwlimit` 选项，单位 `Mbps`，默认值为 `0` 即不限制。

### 目录结构与文件权限

默认情况下，sync 命令只同步文件对象以及包含文件对象的目录，空目录不会被同步。如需同步空目录，可以使用 `--dirs` 选项。

另外，在 local、sftp、hdfs 等文件系统之间同步时，如需保持文件权限，可以使用 `--perms` 选项。

### 拷贝符号链接

JuiceFS `sync` 在**本地目录之间**同步时，支持通过设置 `--links` 选项开启遇到符号链时同步其自身而不是其指向的对象的功能。同步后的符号链接指向的路径为源符号链接中存储的原始路径，无论该路径在同步前后是否可达都不会被转换。

另外需要注意的几个细节

1. 符号链接自身的 `mtime` 不会被拷贝；
2. `--check-new` 和 `--perms` 选项的行为在遇到符号链接时会被忽略。

### 多机并发同步

本质上在两个对象存储之间同步数据就是从一端拉取数据再推送到另一端，如下图所示，同步的效率取决于客户端与云之间的带宽。

![](../images/juicefs-sync-single.png)

在同步大量数据时，单机带宽往往会被占满出现瓶颈，针对这种情况，JuiceFS Sync 提供多机并发同步支持，如下图。

![](../images/juicefs-sync-worker.png)

Manager 作为主控执行 `sync` 命令，通过 `--worker` 参数定义多个 Worker 主机，JuiceFS 会根据 Worker 的总数量，动态拆分同步的工作量并分发给各个主机同时执行。即把原本在一台主机上处理的同步任务量拆分成多份，分发到多台主机上同时处理，单位时间内能处理的数据量更大，总带宽也成倍增加。

在配置多机并发同步任务时，需要提前配置好 Manager 主机到 Worker 主机的 SSH 免密登陆，确保客户端和任务能够成功分发到 Worker。

:::note 注意
Manager 会将 JuiceFS 客户端程序分发到 Worker 主机，为了避免客户端的兼容性问题，请确保 Manager 和 Worker 使用相同类型和架构的操作系统。
:::

例如，将 [对象存储 A](#bucketA) 同步到 [对象存储 B](#bucketB)，采用多主机并行同步：

```shell
juicefs sync --worker bob@192.168.1.20,tom@192.168.8.10 s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

当前主机与两个 Worker 主机 `bob@192.168.1.20` 和 `tom@192.168.8.10` 将共同分担两个对象存储之间的数据同步任务。

:::tip 提示
如果 Worker 主机的 SSH 服务不是默认的 22 号端口，请在 Manager 主机通过 `.ssh/config` 配置文件设置 Worker 主机的 SSH 服务端口号。
:::

## 场景应用

### 数据异地容灾备份

异地容灾备份针对的是文件本身，因此应将 JuiceFS 中存储的文件同步到其他的对象存储，例如，将 [JuiceFS 文件系统](#bucketC) 中的文件同步到 [对象存储 A](#bucketA)：

```shell
# 挂载 JuiceFS
sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
sudo juicefs sync /mnt/jfs/ s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/
```

同步以后，在 [对象存储 A](#bucketA) 中可以直接看到所有的文件。

### 建立 JuiceFS 数据副本

与面向文件本身的容灾备份不同，建立 JuiceFS 数据副本的目的是为 JuiceFS 的数据存储建立一个内容和结构完全相同的镜像，当使用中的对象存储发生了故障，可以通过修改配置切换到数据副本继续工作。需要注意这里仅复制了 JuiceFS 文件系统的数据，并没有复制元数据，元数据引擎的数据备份依然需要。

这需要直接操作 JucieFS 底层的对象存储，将它与目标对象存储之间进行同步。例如，要把 [对象存储 B](#bucketB) 作为 [JuiceFS 文件系统](#bucketC) 的数据副本：

```shell
juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

同步以后，在 [对象存储 B](#bucketB) 中看到的与 [JuiceFS 使用的对象存储](#bucketC) 中的内容和结构完全一样。

:::tip 提示
请阅读《[技术架构](../introduction/architecture.md)》了解 JuiceFS 如何存储文件。
:::
