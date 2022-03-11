---
sidebar_label: 数据同步和迁移
---

# 将数据迁移到 JuiceFS

开始使用 JuiceFS 以后，可能需要将存储在其他对象存储的文件迁移到 JuiceFS，也可能希望定期把文件同步到其他对象存储实现异地容灾，抑或是为 JuiceFS 建立一个数据副本在必要时进行故障转移。

针对数据同步和迁移这类常见需求，JuiceFS 提供了sync 子命令，支持在`对象存储与 JuiceFS 之间`、`对象存储与对象存储之间`多线程并发同步和增量同步数据，所有[ JuiceFS 支持的对象存储](../reference/how_to_setup_object_storage.md)都可以使用该功能。

## 基本用法

### 资源清单

这里假设有以下存储资源：

1. **对象存储 A** <span id="bucketA" />
   
   - 地址：`https://aaa.s3.us-west-1.amazonaws.com`

2. **对象存储 B** <span id="bucketB" />
   
   - 地址：`https://bbb.oss-cn-hangzhou.aliyuncs.com`

3. **JuiceFS 文件系统** <span id="bucketC" />
   
   - 元数据存储：`redis://10.10.0.8:6379/1`
   
   - 对象存储：`https://ccc-125000.cos.ap-beijing.myqcloud.com`

所有存储的**访问密钥**均为：

- **ACCESS_KEY**：`ABCDEFG`

- **SECRET_KEY**：`HIJKLMN`

### 命令格式

```shell
juicefs sync SRC DST
```

其中：

- `SRC` 代表数据源位置

- `DST` 代表目标位置

地址格式均为 `[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]`

其中：

- `NAME` 是存储类型，比如 `s3`、`oss`。

- `ACCESS_KEY` 和 `SECRET_KEY` 是对象存储的 API 访问密钥

- `BUCKET[.ENDPOINT]` 是对象存储的访问地址

- `PREFIX` 是可选的，可以用来限定仅同步特定文件夹的数据

### 对象存储与 JuiceFS 之间同步

将 [对象存储 A](#bucketA) 的 `movies` 目录同步到 [JuiceFS 文件系统](#bucketC)：

```shell
# 挂载 JuiceFS
$ sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
$ juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies /mnt/jfs
```

将 [JuiceFS 文件系统](#bucketC) 的 `images` 目录同步到 [对象存储 A](#bucketA)：

```shell
# 挂载 JuiceFS
$ sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
$ juicefs sync /mnt/jfs/images s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

### 对象存储与对象存储之间同步

将 [对象存储 A](#bucketA) 的全部数据同步到 [对象存储 B](#bucketB)：

```shell
$ juicefs sync s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

### 增量同步

当需要定期在两个存储了大量数据的对象存储之间同步，且数据源变化不大，比如会只新增或减少一些文件。如果每次都执行完整同步不但浪费时间，而且也会浪费网络资源。这种情况下可以使用 `juicefs sync` 的增量同步功能。

借用前面的例子，通过在命令中指定 `--update` 或 `-u` 选项，仅把 [对象存储 A](#bucketA) 的 `movies` 目录中发生了变化的部分同步到 [JuiceFS 文件系统](#bucketC)：

```shell
# 挂载 JuiceFS
$ sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行同步
$ juicefs sync --update s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com/movies /mnt/jfs
```

## 场景应用

### 数据异地容灾备份

异地容灾备份针对的是文件本身，因此应将 JuiceFS 中存储的文件同步到其他的对象存储，例如，将 [JuiceFS 文件系统](#bucketC) 中的文件同步到 [对象存储 A](#bucketA)：

```shell
# 挂载 JuiceFS
$ sudo juicefs mount -d redis://10.10.0.8:6379/1 /mnt/jfs
# 执行增量同步
$ juicefs sync --update /mnt/jfs s3://ABCDEFG:HIJKLMN@aaa.s3.us-west-1.amazonaws.com
```

同步以后，在 [对象存储 A](#bucketA) 中可以直接看到所有的文件。

### 建立 JuiceFS 数据副本

与面向文件本身的容灾备份不同，建立 JuiceFS 数据副本的目的是为 JuiceFS 的数据存储建立一个内容和结构完全相同的镜像，当使用中的对象存储发生了故障，可以通过修改配置切换到数据副本继续工作。

这需要直接操作 JucieFS 底层的对象存储，将它与目标对象存储之间进行同步。例如，要把 [对象存储 B](#bucketB) 作为 [JuiceFS 文件系统](#bucketC) 的数据副本：

```shell
$ juicefs sync cos://ABCDEFG:HIJKLMN@ccc-125000.cos.ap-beijing.myqcloud.com oss://ABCDEFG:HIJKLMN@bbb.oss-cn-hangzhou.aliyuncs.com
```

同步以后，在 [对象存储 B](#bucketB) 中看到的与 [JuiceFS 使用的对象存储](#bucketC) 中的内容和结构完全一样。

:::tip 提示
请阅读《[技术架构](../introduction/architecture.md)》了解 JuiceFS 如何存储文件。
:::
