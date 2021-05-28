# 在 Hadoop 环境使用 JuiceFS Java SDK

JuiceFS 提供兼容 HDFS 接口的 Java 客户端来支持 Hadoop 生态中的各种应用。

> **注意**：
>
> 由于 JuiceFS 默认使用本地的 user 和 UID 映射。因此，在分布式环境下使用，需要[同步所有需要使用的 user 和 UID](sync_accounts_between_multiple_hosts.md) 到所有的 Hadoop 节点上，以避免权限问题。也可以指定一个全局的用户列表和所属用户组文件，具体请参见[相关配置](#其他配置)。

## Hadoop 兼容性

JuiceFS Hadoop Java SDK 同时兼容 Hadoop 2.x 以及 Hadoop 3.x 环境，以及 Hadoop 生态中的各种主流组件。

为了使各组件能够识别 JuiceFS，通常需要两个步骤：

1. 将 JAR 文件放置到组件的 classpath 内；
2. 将 JuiceFS 相关配置写入配置文件（通常是 `core-site.xml`）。

## 编译

你需要先安装 Go 1.13+、JDK 8+ 以及 Maven 工具，然后运行以下命令：

```shell
$ cd sdk/java
$ make
```

> **提示**：对于中国用户，建议设置更快的 Maven 镜像仓库以加速编译，比如[阿里云 Maven 仓库](https://maven.aliyun.com)。

## 部署 JuiceFS Hadoop Java SDK

当编译完成后，你可以在 `sdk/java/target` 目录下找到编译好的 JAR 文件，例如 `juicefs-hadoop-0.10.0.jar`。注意带有 `original-` 前缀的 JAR 文件是不包含第三方依赖的，推荐使用包含第三方依赖的 JAR 文件。

**注意：编译后的 JAR 文件只能部署在相同的系统环境中，例如在 Linux 中编译则只能用于 Linux 环境。**

然后将对应的 JAR 文件和 `$JAVA_HOME/lib/tools.jar` 放到 Hadoop 生态各组件的 classpath 里。常见路径如下，建议将 JAR 文件放置在一个地方，然后其他地方均通过符号链接的方式放置。

### 发行版

| 名称           | 安装路径                                                                                                                                                                                                                                                                                                                   |
| ----           | ----                                                                                                                                                                                                                                                                                                                       |
| CDH            | `/opt/cloudera/parcels/CDH/lib/hadoop/lib`<br>`/opt/cloudera/parcels/CDH/spark/jars`<br>`/var/lib/impala`                                                                                                                                                                                                                  |
| HDP            | `/usr/hdp/current/hadoop-client/lib`<br>`/usr/hdp/current/hive-client/auxlib`<br>`/usr/hdp/current/spark2-client/jars`                                                                                                                                                                                                     |
| Amazon EMR     | `/usr/lib/hadoop/lib`<br>`/usr/lib/spark/jars`<br>`/usr/lib/hive/auxlib`                                                                                                                                                                                                                                                   |
| 阿里云 EMR     | `/opt/apps/ecm/service/hadoop/*/package/hadoop*/share/hadoop/common/lib`<br>`/opt/apps/ecm/service/spark/*/package/spark*/jars`<br>`/opt/apps/ecm/service/presto/*/package/presto*/plugin/hive-hadoop2`<br>`/opt/apps/ecm/service/hive/*/package/apache-hive*/lib`<br>`/opt/apps/ecm/service/impala/*/package/impala*/lib` |
| 腾讯云 EMR     | `/usr/local/service/hadoop/share/hadoop/common/lib`<br>`/usr/local/service/presto/plugin/hive-hadoop2`<br>`/usr/local/service/spark/jars`<br>`/usr/local/service/hive/auxlib`                                                                                                                                              |
| UCloud UHadoop | `/home/hadoop/share/hadoop/common/lib`<br>`/home/hadoop/hive/auxlib`<br>`/home/hadoop/spark/jars`<br>`/home/hadoop/presto/plugin/hive-hadoop2`                                                                                                                                                                             |
| 百度云 EMR     | `/opt/bmr/hadoop/share/hadoop/common/lib`<br>`/opt/bmr/hive/auxlib`<br>`/opt/bmr/spark2/jars`                                                                                                                                                                                                                              |

### 社区开源组件

| 名称   | 安装路径                             |
| ----   | ----                                 |
| Spark  | `${SPARK_HOME}/jars`                 |
| Presto | `${PRESTO_HOME}/plugin/hive-hadoop2` |
| Flink  | `${FLINK_HOME}/lib`                  |

## 配置参数

### 核心配置

| 配置项                           | 默认值                       | 描述                                                                                                                                                                                                         |
| ------------------------------   | --------------------------   | ------------------------------------------------------------                                                                                                                                                 |
| `fs.jfs.impl`                    | `io.juicefs.JuiceFileSystem` | 指定 `jfs://` 这个存储类型所使用的实现。JuiceFS 支持修改 scheme，例如想要使用 `cfs://` 作为 scheme，则将 `fs.cfs.impl` 的实现修改为此配置即可，当使用 `cfs://` 访问数据的时候，仍然是访问的 JuiceFS 的数据。 |
| `fs.AbstractFileSystem.jfs.impl` | `io.juicefs.JuiceFS`         |                                                                                                                                                                                                              |
| `juicefs.meta`                   |                              | Redis 地址，格式为 `redis://<user>:<password>@<host>:<port>/<db>`。                                                                                                                                          |
| `juicefs.accesskey`              |                              | 对象存储的访问 ID（Access Key ID）。如果计算节点已经有访问对象存储的权限，则无需提供。请查看[这个文档](../en/how_to_setup_object_storage.md)了解如何获取不同对象存储的 access key。                          |
| `juicefs.secretkey`              |                              | 对象存储的私钥 (Secret Access Key)。如果计算节点已经有访问对象存储的权限，则无需提供。请查看[这个文档](../en/how_to_setup_object_storage.md)了解如何获取不同对象存储的 secret key。                          |

### 缓存配置

| 配置项                       | 默认值 | 描述                                                                                                                                                                                                                                                                                      |
| --------------------------   | ------ | ------------------------------------------------------------                                                                                                                                                                                                                              |
| `juicefs.cache-dir`          |        | 本地缓存目录，可以指定多个文件夹，用冒号 `:` 分隔，也可以使用通配符（比如 `*` ）。**通常应用没有权限创建这些目录，需要手动创建并给予 `0777` 权限，便于多个应用共享缓存数据。**                                                                                                            |
| `juicefs.cache-size`         | 0      | 磁盘缓存容量，单位 MiB。如果配置多个目录，这是所有缓存目录的空间总和。                                                                                                                                                                                                                    |
| `juicefs.cache-full-block`   | `true` | 是否缓存所有读取的数据块，`false` 表示只缓存随机读的数据块。                                                                                                                                                                                                                              |
| `juicefs.free-space`         | 0.2    | 本地缓存目录的最小可用空间比例                                                                                                                                                                                                                                                            |
| `juicefs.discover-nodes-url` |        | 指定发现集群节点列表的方式，每 10 分钟刷新一次。<br /><br />YARN：`yarn`<br />Spark Standalone：`http://spark-master:web-ui-port/json/`<br />Spark ThriftServer：`http://thrift-server:4040/api/v1/applications/`<br />Presto：`http://coordinator:discovery-uri-port/v1/service/presto/` |

### I/O 配置

| 配置项                | 默认值 | 描述                                                         |
| ------------------    | ------ | ------------------------------------------------------------ |
| `juicefs.max-uploads` | 50     | 上传数据的最大连接数                                         |
| `juicefs.get-timeout` | 5      | 下载一个对象的超时时间，单位为秒。                           |
| `juicefs.put-timeout` | 60     | 上传一个对象的超时时间，单位为秒。                           |
| `juicefs.memory-size` | 300    | 读写数据的缓冲区最大空间，单位为 MiB。                       |
| `juicefs.prefetch`    | 3      | 预读数据块的最大并发数                                       |

### 其他配置

| 配置项                    | 默认值  | 描述                                                                                                                                          |
| ------------------        | ------  | ------------------------------------------------------------                                                                                  |
| `juicefs.debug`           | `false` | 是否开启 debug 日志                                                                                                                           |
| `juicefs.access-log`      |         | 访问日志的路径。需要所有应用都有写权限，可以配置为 `/tmp/juicefs.access.log`。该文件会自动轮转，保留最近 7 个文件。                           |
| `juicefs.superuser`       | `hdfs`  | 超级用户                                                                                                                                      |
| `juicefs.users`           | `null`  | 用户名以及 UID 列表文件的地址，比如 `jfs://name/etc/users`。文件格式为 `<username>:<UID>`，一行一个用户。                                     |
| `juicefs.groups`          | `null`  | 用户组、GID 以及组成员列表文件的地址，比如 `jfs://name/etc/groups`。文件格式为 `<group-name>:<GID>:<username1>,<username2>`，一行一个用户组。 |
| `juicefs.umask`           | `null`  | 创建文件和目录的 umask 值（如 `0022`），如果没有此配置，默认值是 `fs.permissions.umask-mode`。                                                |
| `juicefs.push-gateway`    |         | [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) 地址，格式为 `<host>:<port>`。                                            |
| `juicefs.push-interval`   | 10      | 推送数据到 Prometheus 的时间间隔，单位为秒。                                                                                                  |
| `juicefs.push-auth`       |         | [Prometheus 基本认证](https://prometheus.io/docs/guides/basic-auth)信息，格式为 `<username>:<password>`。                                     |
| `juicefs.fast-resolve`    | `true`  | 是否开启快速元数据查找（通过 Redis Lua 脚本实现）                                                                                             |
| `juicefs.no-usage-report` | `false` | 是否上报数据，它只上报诸如版本号等使用量数据，不包含任何用户信息。                                                                            |

当使用多个 JuiceFS 文件系统时，上述所有配置项均可对单个文件系统指定，需要将文件系统名字 `{JFS_NAME}` 放在配置项的中间，比如：

```xml
<property>
  <name>juicefs.{JFS_NAME}.meta</name>
  <value>redis://host:port/1</value>
</property>
```

### 常用配置

**注意：替换 `juicefs.meta` 配置中的 `{HOST}`、`{PORT}` 和 `{DB}` 变量为实际的值。**

```xml
<property>
  <name>fs.jfs.impl</name>
  <value>io.juicefs.JuiceFileSystem</value>
</property>
<property>
  <name>fs.AbstractFileSystem.jfs.impl</name>
  <value>io.juicefs.JuiceFS</value>
</property>
<property>
  <name>juicefs.meta</name>
  <value>redis://{HOST}:{PORT}/{DB}</value>
</property>
<property>
  <name>juicefs.cache-dir</name>
  <value>/data*/jfs</value>
</property>
<property>
  <name>juicefs.cache-size</name>
  <value>1024</value>
</property>
<property>
  <name>juicefs.access-log</name>
  <value>/tmp/juicefs.access.log</value>
</property>
```

### Hadoop 环境配置

将配置参数加入到 Hadoop 配置文件 `core-site.xml` 中。

#### CDH 6 环境配置

如果使用的是 CDH 6 版本，除了修改 `core-site` 外，还需要通过 YARN 服务界面修改 `mapreduce.application.classpath`，增加：

```shell
$HADOOP_COMMON_HOME/lib/juicefs-hadoop.jar
```

#### HDP 环境配置

除了修改 `core-site` 外，还需要通过 MapReduce2 服务界面修改配置 `mapreduce.application.classpath`，在末尾增加（变量无需替换）：

```shell
/usr/hdp/${hdp.version}/hadoop/lib/juicefs-hadoop.jar
```

### Flink 配置

将配置参数加入 `conf/flink-conf.yaml`。如果只是在 Flink 中使用 JuiceFS, 可以不在 Hadoop 环境配置 JuiceFS，只需要配置 Flink 客户端即可。

## 重启相关服务

当需要使用以下组件访问 JuiceFS 数据时，需要重启相关服务。

> **注意**：在重启之前需要保证 JuiceFS 配置已经写入配置文件，通常可以查看机器上各组件配置的 `core-site.xml` 里面是否有 JuiceFS 相关配置。

| 组件名 | 服务名                     |
| ------ | -------------------------- |
| Hive   | HiveServer<br />Metastore  |
| Spark  | ThriftServer               |
| Presto | Coordinator<br />Worker    |
| Impala | Catalog Server<br />Daemon |
| HBase  | Master<br />RegionServer   |

HDFS、Hue、ZooKeeper 等服务无需重启。

重启后，访问 JuiceFS 如果出现 `Class io.juicefs.JuiceFileSystem not found` 或者 `No FilesSystem for scheme: jfs`，可以参考 [FAQ](#faq)。

## 验证

### Hadoop

```bash
$ hadoop fs -ls jfs://{JFS_NAME}/
```

> **注**：这里的 `JFS_NAME` 是创建 JuiceFS 文件系统时指定的名称。

### Hive

```sql
CREATE TABLE IF NOT EXISTS person
(
  name STRING,
  age INT
) LOCATION 'jfs://{JFS_NAME}/tmp/person';
```

## 指标收集

JuiceFS Hadoop Java SDK 支持把运行指标以 [Prometheus](https://prometheus.io) 格式上报到 [Pushgateway](https://github.com/prometheus/pushgateway)，然后可以通过 [Grafana](https://grafana.com) 以及我们[预定义的模板](../en/k8s_grafana_template.json)来展示收集的运行指标。

请用如下参数启用指标收集：

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

> **注意**：每一个使用 JuiceFS Hadoop Java SDK 的进程会有唯一的指标，而 Pushgateway 会一直记住所有收集到的指标，导致指标数持续积累占用过多内存，也会使得 Prometheus 抓取指标时变慢，建议定期清理 Pushgateway 上 `job` 为 `juicefs` 的指标。建议每个小时使用下面的命令清理一次，运行中的 Hadoop Java SDK 会在指标清空后继续更新，基本不影响使用。
>
> ```bash
> $ curl -X DELETE http://host:9091/metrics/job/juicefs
> ```

关于所有监控指标的描述，请查看 [JuiceFS 监控指标](p8s_metrics.md)。

## Benchmark

当部署完成后，可以运行 JuiceFS 自带的压测工具进行性能测试。


### 本地测试
#### 元数据

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation create -numberOfFiles 10000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

  此命令会 create 10000 个空文件

- open

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation open -numberOfFiles 10000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

  此命令会 open 10000 个文件，并不读取数据

- rename

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation rename -numberOfFiles 10000 -bytesPerBlock 134217728 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

- delete

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation delete -numberOfFiles 10000 -bytesPerBlock 134217728 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

- 参考值

| 操作   | TPS  | 时延（ms） |
| ------ | ---- | ----       |
| create | 546  | 1.83       |
| open   | 1135 | 0.88       |
| rename | 364  | 2.75       |
| delete | 289  | 3.46       |

#### IO 性能

- 连续写

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestFSIO -write -fileSize 20000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

- 连续读

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestFSIO -read -fileSize 20000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  如果多次运行此命令，可能会出现数据被缓存到了系统缓存而导致读取速度非常快，只需清除 JuiceFS 的本地磁盘缓存即可

- 参考值

| 操作   | 吞吐（MB/s） |
| ------ | ----         |
| write  | 453          |
| read   | 141          |

如果机器的网络带宽比较低，则一般能达到网络带宽瓶颈

### 分布式测试

以下命令会启动 MapReduce 分布式任务程序元数据和 IO 性能

以下测试需要保证集群有足够的资源能够同时启动所需的 map 数量

此测试使用了 3 台 4c32g 内存的计算节点，突发带宽 5Gbit/s，阿里云 Redis 5.0 社区 4G 主从版

#### 元数据

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation create -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会创建 1000 个空文件，总共 100000 个空文件

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation open -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 open 1000 个文件，总共 open 100000 个文件

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation rename -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 rename 1000 个文件，总共 rename 100000 个文件

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation delete -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 delete 1000 个文件，总共 delete 100000 个文件

- 参考值

  - 10 并发

  | 操作   | IOPS | 时延（ms） |
  | ------ | ---- | ----       |
  | create | 2307 | 3.6        |
  | open   | 3215 | 2.3        |
  | rename | 1700 | 5.22       |
  | delete | 1378 | 6.7        |

  - 100 并发

  | 操作   | IOPS  | 时延（ms） |
  | ------ | ----  | ----       |
  | create | 8375  | 11.5       |
  | open   | 12691 | 7.5        |
  | rename | 5343  | 18.4       |
  | delete | 3576  | 27.6       |

#### IO 性能

- 连续写

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestDFSIO -write -nrFiles 10 -fileSize 10000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  此命令会启动 10 个 map task，每个 task 写入 10000MB 的数据

- 连续读

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestDFSIO -read -nrFiles 10 -fileSize 10000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  此命令会启动 10 个 map task，每个 task 读取 10000MB 的数据


- 参考值

| 操作   | 平均吞吐（MB/s） | 总吞吐（MB/s） |
| ------ | ----             | ----           |
| write  | 180              | 1792           |
| read   | 141              | 1409           |


## FAQ

### 出现 `Class io.juicefs.JuiceFileSystem not found` 异常

出现这个异常的原因是 juicefs-hadoop.jar 没有被加载，可以用 `lsof -p {pid} | grep juicefs` 查看 JAR 文件是否被加载。需要检查 JAR 文件是否被正确地放置在各个组件的 classpath 里面，并且保证 JAR 文件有可读权限。

另外在某些发行版 Hadoop 环境，需要修改 `mapred-site.xml` 里面的 `mapreduce.application.classpath` 参数，增加 juicefs-hadoop.jar 的路径。

### 出现 `No FilesSystem for scheme: jfs` 异常

出现这个异常的原因是 `core-site.xml` 里面的 JuiceFS 配置没有被读取到，需要检查组件配置的 `core-site.xml` 里面是否有 JuiceFS 相关配置。
