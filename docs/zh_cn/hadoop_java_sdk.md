# Hadoop 生态使用 JuiceFS 存储

## 目录

- [环境要求](#%E7%8E%AF%E5%A2%83%E8%A6%81%E6%B1%82)
  * [1. Hadoop 及相关组件](#1-hadoop-%E5%8F%8A%E7%9B%B8%E5%85%B3%E7%BB%84%E4%BB%B6)
  * [2. 用户权限](#2-%E7%94%A8%E6%88%B7%E6%9D%83%E9%99%90)
  * [3. 文件系统](#3-%E6%96%87%E4%BB%B6%E7%B3%BB%E7%BB%9F)
  * [4. 内存资源](#4-%E5%86%85%E5%AD%98%E8%B5%84%E6%BA%90)
- [客户端编译](#%E5%AE%A2%E6%88%B7%E7%AB%AF%E7%BC%96%E8%AF%91)
  * [Linux 和 macOS](#linux-%E5%92%8C-macos)
  * [Windows](#windows)
- [部署客户端](#%E9%83%A8%E7%BD%B2%E5%AE%A2%E6%88%B7%E7%AB%AF)
  * [大数据平台](#%E5%A4%A7%E6%95%B0%E6%8D%AE%E5%B9%B3%E5%8F%B0)
  * [社区开源组件](#%E7%A4%BE%E5%8C%BA%E5%BC%80%E6%BA%90%E7%BB%84%E4%BB%B6)
  * [客户端配置参数](#%E5%AE%A2%E6%88%B7%E7%AB%AF%E9%85%8D%E7%BD%AE%E5%8F%82%E6%95%B0)
    + [核心配置](#%E6%A0%B8%E5%BF%83%E9%85%8D%E7%BD%AE)
    + [缓存配置](#%E7%BC%93%E5%AD%98%E9%85%8D%E7%BD%AE)
    + [I/O 配置](#io-%E9%85%8D%E7%BD%AE)
    + [其他配置](#%E5%85%B6%E4%BB%96%E9%85%8D%E7%BD%AE)
    + [多文件系统配置](#%E5%A4%9A%E6%96%87%E4%BB%B6%E7%B3%BB%E7%BB%9F%E9%85%8D%E7%BD%AE)
    + [配置示例](#%E9%85%8D%E7%BD%AE%E7%A4%BA%E4%BE%8B)
- [Hadoop 环境配置](#hadoop-%E7%8E%AF%E5%A2%83%E9%85%8D%E7%BD%AE)
  * [CDH6](#cdh6)
  * [HDP](#hdp)
  * [Flink](#flink)
  * [重启服务](#%E9%87%8D%E5%90%AF%E6%9C%8D%E5%8A%A1)
- [环境验证](#%E7%8E%AF%E5%A2%83%E9%AA%8C%E8%AF%81)
  * [Hadoop](#hadoop)
  * [Hive](#hive)
- [监控指标收集](#%E7%9B%91%E6%8E%A7%E6%8C%87%E6%A0%87%E6%94%B6%E9%9B%86)
- [基准测试](#%E5%9F%BA%E5%87%86%E6%B5%8B%E8%AF%95)
  * [1. 本地测试](#1-%E6%9C%AC%E5%9C%B0%E6%B5%8B%E8%AF%95)
    + [元数据性能](#%E5%85%83%E6%95%B0%E6%8D%AE%E6%80%A7%E8%83%BD)
    + [I/O 性能](#io-%E6%80%A7%E8%83%BD)
  * [2. 分布式测试](#2-%E5%88%86%E5%B8%83%E5%BC%8F%E6%B5%8B%E8%AF%95)
    + [元数据性能](#%E5%85%83%E6%95%B0%E6%8D%AE%E6%80%A7%E8%83%BD-1)
    + [I/O 性能](#io-%E6%80%A7%E8%83%BD-1)
- [FAQ](#faq)

----

JuiceFS 提供与 HDFS 接口[高度兼容](https://hadoop.apache.org/docs/current/hadoop-project-dist/hadoop-common/filesystem/introduction.html)的 Java 客户端，Hadoop 生态中的各种应用都可以在不改变代码的情况下，平滑地使用 JuiceFS 存储数据。

## 环境要求

### 1. Hadoop 及相关组件

JuiceFS Hadoop Java SDK 同时兼容 Hadoop 2.x、Hadoop 3.x，以及 Hadoop 生态中的各种主流组件。

### 2. 用户权限

JuiceFS 默认使用本地的 `用户` 和 `UID` 映射，在分布式环境下使用时，为了避免权限问题，请参考[文档](sync_accounts_between_multiple_hosts.md)将需要使用的 `用户` 和 `UID` 同步到所有 Hadoop 节点。也可以通过定义一个全局的用户和用户组文件给集群共享读取，[查看详情](#其他配置)。

### 3. 文件系统

通过 JuiceFS Java 客户端为 Hadoop 生态提供存储，需要提前创建 JuiceFS 文件系统。部署 Java 客户端时，在配置文件中指定已创建文件系统的元数据引擎地址。

创建文件系统可以参考 [JuiceFS 快速上手指南](quick_start_guide.md)。

> **注意**：如果要在分布式环境中使用 JuiceFS，创建文件系统时，请合理规划要使用的对象存储和数据库，确保它们可以被每个集群节点正常访问。

### 4. 内存资源

JuiceFS Hadoop Java SDK 最多需要额外使用 4 * [`juicefs.memory-size`](#io-配置) 的 off-heap 内存用来加速读写性能，默认情况下，最多需要额外 1.2GB 内存（取决于写入负载）。

## 客户端编译

编译依赖以下工具：

- [Go](https://golang.org/) 1.15+（中国用户建议使用 [Goproxy China 镜像加速](https://github.com/goproxy/goproxy.cn)）
- JDK 8+
- [Maven](https://maven.apache.org/) 3.3+（中国用户建议使用[阿里云镜像加速](https://maven.aliyun.com)）
- git
- make
- GCC 5.4+

> **注意**：如果使用 Ceph 的 RADOS 作为 JuiceFS 的存储引擎，需要安装 librados-dev 并且在编译 `libjfs.so` 时加上 `-tag ceph`。

### Linux 和 macOS

克隆仓库：

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

进入目录，执行编译：

```shell
$ cd juicefs/sdk/java
$ make
```

编译完成后，可以在  `sdk/java/target`  目录中找到编译好的 `JAR` 文件，包括两个版本：

- 包含第三方依赖的包：`juicefs-hadoop-X.Y.Z.jar`
- 不包含第三方依赖的包：`original-juicefs-hadoop-X.Y.Z.jar`

建议使用包含第三方依赖的版本。

### Windows

用于 Windows 环境的客户端需要在 Linux 或 macOS 系统上通过交叉编译的方式获得，编译依赖 [mingw-w64](https://www.mingw-w64.org/)，需要提前安装。

与编译面向 Linux 和 macOS 客户端的步骤相同，比如在 Ubuntu 系统上，先安装 `mingw-w64` 包，解决依赖问题：

```shell
$ sudo apt install mingw-w64
```

克隆并进入 JuiceFS 源代码目录，执行以下代码进行编译：

```shell
$ cd juicefs/sdk/java
$ make win
```

> **注意**：不论为哪个系统环境编译客户端，编译后的 JAR 文件都为相同的名称，且只能部署在匹配的系统环境中，例如在 Linux 中编译则只能用于 Linux 环境。另外，由于编译的包依赖 glibc，建议尽量使用低版本的系统进行编译，这样可以获得更好的兼容性。

## 部署客户端

让 Hadoop 生态各组件能够正确识别 JuiceFS，需要进行以下配置：

1. 将编译好的 JAR 文件和 `$JAVA_HOME/lib/tools.jar` 放置到组件的 `classpath` 内，常见大数据平台和组件的安装路径见下表。
2. 将 JuiceFS 相关配置写入配置文件（通常是 `core-site.xml`），详见[客户端配置参数](#客户端配置参数)。

建议将 JAR 文件放置在一个统一的位置，其他位置通过符号链接进行调用。

### 大数据平台

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

### 客户端配置参数

请参考以下表格设置 JuiceFS 文件系统相关参数，并写入配置文件，一般是 `core-site.xml`。

#### 核心配置

| 配置项                           | 默认值                       | 描述                                                         |
| -------------------------------- | ---------------------------- | ------------------------------------------------------------ |
| `fs.jfs.impl`                    | `io.juicefs.JuiceFileSystem` | 指定要使用的存储实现，默认使用 `jfs://` 。如想要使用 `cfs://` 作为 scheme，则修改为 `fs.cfs.impl` 即可。在使用 `cfs://` 时，仍是访问 JuiceFS 中的数据。 |
| `fs.AbstractFileSystem.jfs.impl` | `io.juicefs.JuiceFS`         |                                                              |
| `juicefs.meta`                   |                              | 指定预先创建好的 JuiceFS 文件系统的元数据引擎地址。可以通过 `juicefs.{vol_name}.meta` 格式为客户端同时配置多个文件系统。 |

#### 缓存配置

| 配置项                       | 默认值 | 描述                                                         |
| ---------------------------- | ------ | ------------------------------------------------------------ |
| `juicefs.cache-dir`          |        | 设置本地缓存目录，可以指定多个文件夹，用冒号 `:` 分隔，也可以使用通配符（比如 `*` ）。**请预先创建好这些目录，并给予 `0777` 权限，便于多个应用共享缓存数据。** |
| `juicefs.cache-size`         | 0      | 设置本地缓存目录的容量，单位 MiB，默认为0，不开启缓存。如果配置了多个缓存目录，该值代表所有缓存目录容量的总和。 |
| `juicefs.cache-full-block`   | `true` | 是否缓存所有读取的数据块，`false` 表示只缓存随机读的数据块。 |
| `juicefs.free-space`         | 0.1    | 本地缓存目录的最小可用空间比例，默认保留 10% 剩余空间。      |
| `juicefs.attr-cache`         | 0      | 目录和文件属性缓存的过期时间（单位：秒）                   |
| `juicefs.entry-cache`        | 0      | 文件项缓存的过期时间（单位：秒）                          |
| `juicefs.dir-entry-cache`    | 0      | 目录项缓存的过期时间（单位：秒）                          |
| `juicefs.discover-nodes-url` |        | 指定发现集群节点列表的方式，每 10 分钟刷新一次。<br /><br />YARN：`yarn`<br />Spark Standalone：`http://spark-master:web-ui-port/json/`<br />Spark ThriftServer：`http://thrift-server:4040/api/v1/applications/`<br />Presto：`http://coordinator:discovery-uri-port/v1/service/presto/` |

#### I/O 配置

| 配置项                   | 默认值 | 描述                                    |
| ------------------------ | ------ | --------------------------------------- |
| `juicefs.max-uploads`    | 20     | 上传数据的最大连接数                    |
| `juicefs.get-timeout`    | 5      | 下载一个对象的超时时间，单位为秒。      |
| `juicefs.put-timeout`    | 60     | 上传一个对象的超时时间，单位为秒。      |
| `juicefs.memory-size`    | 300    | 读写数据的缓冲区最大空间，单位为 MiB。  |
| `juicefs.prefetch`       | 1      | 预读数据块的线程数                      |
| `juicefs.upload-limit`   | 0      | 上传带宽限制，单位为 Mbps，默认不限制。 |
| `juicefs.download-limit` | 0      | 下载带宽限制，单位为 Mbps，默认不限制。 |

  #### 其他配置

| 配置项                    | 默认值  | 描述                                                         |
| ------------------------- | ------- | ------------------------------------------------------------ |
| `juicefs.debug`           | `false` | 是否开启 debug 日志                                          |
| `juicefs.access-log`      |         | 访问日志的路径。需要所有应用都有写权限，可以配置为 `/tmp/juicefs.access.log`。该文件会自动轮转，保留最近 7 个文件。 |
| `juicefs.superuser`       | `hdfs`  | 超级用户                                                     |
| `juicefs.users`           | `null`  | 用户名以及 UID 列表文件的地址，比如 `jfs://name/etc/users`。文件格式为 `<username>:<UID>`，一行一个用户。 |
| `juicefs.groups`          | `null`  | 用户组、GID 以及组成员列表文件的地址，比如 `jfs://name/etc/groups`。文件格式为 `<group-name>:<GID>:<username1>,<username2>`，一行一个用户组。 |
| `juicefs.umask`           | `null`  | 创建文件和目录的 umask 值（如 `0022`），如果没有此配置，默认值是 `fs.permissions.umask-mode`。 |
| `juicefs.push-gateway`    |         | [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) 地址，格式为 `<host>:<port>`。 |
| `juicefs.push-interval`   | 10      | 推送数据到 Prometheus 的时间间隔，单位为秒。                 |
| `juicefs.push-auth`       |         | [Prometheus 基本认证](https://prometheus.io/docs/guides/basic-auth)信息，格式为 `<username>:<password>`。 |
| `juicefs.fast-resolve`    | `true`  | 是否开启快速元数据查找（通过 Redis Lua 脚本实现）            |
| `juicefs.no-usage-report` | `false` | 是否上报数据。仅上版本号等使用量数据，不包含任何用户信息。   |

#### 多文件系统配置

当需要同时使用多个 JuiceFS 文件系统时，上述所有配置项均可对特定文件系统进行指定，只需要将文件系统名字放在配置项的中间，比如下面示例中的 `jfs1` 和 `jfs2`：

```xml
<property>
  <name>juicefs.jfs1.meta</name>
  <value>redis://jfs1.host:port/1</value>
</property>
<property>
  <name>juicefs.jfs2.meta</name>
  <value>redis://jfs2.host:port/1</value>
</property>
```

#### 配置示例

以下是一个常用的配置示例，请替换 `juicefs.meta` 配置中的 `{HOST}`、`{PORT}` 和 `{DB}` 变量为实际的值。

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

## Hadoop 环境配置

请参照前述各项配置表，将配置参数加入到 Hadoop 配置文件 `core-site.xml` 中。

### CDH6

如果使用的是 CDH 6 版本，除了修改 `core-site` 外，还需要通过 YARN 服务界面修改 `mapreduce.application.classpath`，增加：

```shell
$HADOOP_COMMON_HOME/lib/juicefs-hadoop.jar
```

### HDP

除了修改 `core-site` 外，还需要通过 MapReduce2 服务界面修改配置 `mapreduce.application.classpath`，在末尾增加（变量无需替换）：

```shell
/usr/hdp/${hdp.version}/hadoop/lib/juicefs-hadoop.jar
```

### Flink

将配置参数加入 `conf/flink-conf.yaml`。如果只是在 Flink 中使用 JuiceFS, 可以不在 Hadoop 环境配置 JuiceFS，只需要配置 Flink 客户端即可。

### 重启服务

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

若访问 JuiceFS 出现 `Class io.juicefs.JuiceFileSystem not found` 或 `No FilesSystem for scheme: jfs` 错误，请参考 [FAQ](#faq)。

## 环境验证

JuiceFS Java 客户端部署完成以后，可以采用以下方式验证部署是否成功。

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

## 监控指标收集

JuiceFS Hadoop Java SDK 支持把运行指标以 [Prometheus](https://prometheus.io) 格式上报到 [Pushgateway](https://github.com/prometheus/pushgateway)，然后可以通过 [Grafana](https://grafana.com) 以及我们[预定义的模板](../en/grafana_template.json)来展示收集的运行指标。

请用如下参数启用指标收集：

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

**注意**：每一个使用 JuiceFS Hadoop Java SDK 的进程会有唯一的指标，而 Pushgateway 会一直记住所有收集到的指标，导致指标数持续积累占用过多内存，也会使得 Prometheus 抓取指标时变慢，建议定期清理 Pushgateway 上 `job` 为 `juicefs` 的指标。

每个小时使用下面的命令清理一次，运行中的 Hadoop Java SDK 会在指标清空后继续更新，基本不影响使用。

> ```bash
>$ curl -X DELETE http://host:9091/metrics/job/juicefs
> ```

关于所有监控指标的描述，请查看 [JuiceFS 监控指标](p8s_metrics.md)。

## 基准测试

以下提供了一系列方法，使用 JuiceFS 客户端内置的压测工具，对已经成功部署了客户端环境进行性能测试。


### 1. 本地测试
#### 元数据性能

- **create**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench create -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

  此命令会 create 10000 个空文件

- **open**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench open -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

  此命令会 open 10000 个文件，并不读取数据

- **rename**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench rename -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

- **delete**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench delete -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

- **参考值**

| 操作   | TPS  | 时延（ms） |
| ------ | ---- | ----       |
| create | 644  | 1.55       |
| open   | 3467 | 0.29       |
| rename | 483  | 2.07       |
| delete | 506  | 1.97       |

#### I/O 性能

- **顺序写**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -write -size 20000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO -local
  ```

- **顺序读**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -read -size 20000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO -local
  ```

  如果多次运行此命令，可能会出现数据被缓存到了系统缓存而导致读取速度非常快，只需清除 JuiceFS 的本地磁盘缓存即可

- **参考值**

| 操作   | 吞吐（MB/s） |
| ------ | ----         |
| write  | 647          |
| read   | 111          |

如果机器的网络带宽比较低，则一般能达到网络带宽瓶颈

### 2. 分布式测试

以下命令会启动 MapReduce 分布式任务程序对元数据和 IO 性能进行测试，测试时需要保证集群有足够的资源能够同时启动所需的 map 任务。

本项测试使用的计算资源：

- **服务器**：3 台 4 核 32 GB 内存的云服务器，突发带宽 5Gbit/s。
- **数据库**：阿里云 Redis 5.0 社区 4G 主从版

#### 元数据性能

- **create**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench create -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会创建 1000 个空文件，总共 100000 个空文件

- **open**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench open -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 open 1000 个文件，总共 open 100000 个文件

- **rename**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench rename -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 rename 1000 个文件，总共 rename 100000 个文件

- **delete**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench delete -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  此命令会启动 10 个 map task，每个 task 有 10 个线程，每个线程会 delete 1000 个文件，总共 delete 100000 个文件

- **参考值**

  - 10 并发

  | 操作   | IOPS | 时延（ms） |
  | ------ | ---- | ----       |
  | create | 4178 | 2.2        |
  | open   | 9407 | 0.8        |
  | rename | 3197 | 2.9       |
  | delete | 3060 | 3.0        |

  - 100 并发

  | 操作   | IOPS  | 时延（ms） |
  | ------ | ----  | ----       |
  | create | 11773  | 7.9       |
  | open   | 34083 | 2.4        |
  | rename | 8995  | 10.8       |
  | delete | 7191  | 13.6       |

#### I/O 性能

- **连续写**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -write -maps 10 -size 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO
  ```

  此命令会启动 10 个 map task，每个 task 写入 10000MB 的数据

- **连续读**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -read -maps 10 -size 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO
  ```

  此命令会启动 10 个 map task，每个 task 读取 10000MB 的数据


- **参考值**

| 操作   | 平均吞吐（MB/s） | 总吞吐（MB/s） |
| ------ | ----             | ----           |
| write  | 198              | 1835           |
| read   | 124              | 1234           |


## FAQ

### 1. 出现 `Class io.juicefs.JuiceFileSystem not found` 异常

出现这个异常的原因是 juicefs-hadoop.jar 没有被加载，可以用 `lsof -p {pid} | grep juicefs` 查看 JAR 文件是否被加载。需要检查 JAR 文件是否被正确地放置在各个组件的 classpath 里面，并且保证 JAR 文件有可读权限。

另外，在某些发行版 Hadoop 环境中，需要修改 `mapred-site.xml` 中的 `mapreduce.application.classpath` 参数，添加 juicefs-hadoop.jar 的路径。

### 2. 出现 `No FilesSystem for scheme: jfs` 异常

出现这个异常的原因是 `core-site.xml` 配置文件中的 JuiceFS 配置没有被读取到，需要检查组件配置的 `core-site.xml` 中是否有 JuiceFS 相关配置。
