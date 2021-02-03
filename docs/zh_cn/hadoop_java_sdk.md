# 在 Hadoop 环境使用 JuiceFS Java SDK

JuiceFS 提供兼容 HDFS 接口的 Java 客户端来支持 Hadoop 生态中的各种应用。

## Hadoop 兼容性

JuiceFS Java SDK 同时兼容 Hadoop 2.x 以及 Hadoop 3.x 环境，以及 Hadoop 生态中的各种主流组件。

## 编译

你需要先安装 Go 1.13+、JDK 8+ 以及 Maven 工具，然后运行以下命令：

```shell
$ cd sdk/java
$ make
```

对于中国用户，建议设置更快的 Maven 镜像仓库以加速编译，比如[阿里云 Maven 仓库](https://maven.aliyun.com)。

## 部署 JuiceFS Java SDK

当编译完成后，你可以在 `sdk/java/target` 目录下找到编译好的 JAR 文件，例如 `juicefs-hadoop-0.10.0.jar`。注意带有 `original-` 前缀的 JAR 文件是不包含第三方依赖的，推荐使用包含第三方依赖的 JAR 文件。

**注意：编译后的 JAR 文件只能部署在相同的系统环境中，例如在 Linux 中编译则只能用于 Linux 环境。**

然后将对应的 JAR 文件和 $JAVA_HOME/lib/tools.jar 放到 Hadoop 生态各组件的 classpath 里。常见路径如下，建议将 JAR 文件放置在一个地方，然后其他地方均通过符号链接的方式放置。

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
| Flink | `${FLINK_HOME}/lib` |

## 配置参数

### 核心配置

| 配置项                           | 默认值                       | 描述                                                                                                                                                                                                         |
| ------------------------------   | --------------------------   | ------------------------------------------------------------                                                                                                                                                 |
| `fs.jfs.impl`                    | `io.juicefs.JuiceFileSystem` | 指定 `jfs://` 这个存储类型所使用的实现。JuiceFS 支持修改 scheme，例如想要使用 `cfs://` 作为 scheme，则将 `fs.cfs.impl` 的实现修改为此配置即可，当使用 `cfs://` 访问数据的时候，仍然是访问的 JuiceFS 的数据。 |
| `fs.AbstractFileSystem.jfs.impl` | `io.juicefs.JuiceFS`         |                                                                                                                                                                                                              |
| `juicefs.meta`                   |                              | Redis 地址，格式为 `redis://<user>:<password>@<host>:6379/<db>`。                                                                                                                                            |
| `juicefs.accesskey`              |                              | 对象存储的访问 ID（Access Key ID）。如果计算节点已经有访问对象存储的权限，则无需提供。                                                                                                                       |
| `juicefs.secretkey`              |                              | 对象存储的私钥 (Secret Access Key)。如果计算节点已经有访问对象存储的权限，则无需提供。                                                                                                                       |

### 缓存配置

| 配置项                       | 默认值 | 描述                                                                                                                                                                                                                                                                                |
| --------------------------   | ------ | ------------------------------------------------------------                                                                                                                                                                                                                        |
| `juicefs.cache-dir`          |        | 本地缓存目录，可以指定多个文件夹，用冒号 `:` 分隔，也可以使用通配符（比如 `*` ）。**通常应用没有权限创建这些目录，需要手动创建并给予 `0777` 权限，便于多个应用共享缓存数据。**                                                                                                      |
| `juicefs.cache-size`         | 0      | 磁盘缓存容量，单位 MiB。如果配置多个目录，这是所有缓存目录的空间总和。                                                                                                                                                                                                              |
| `juicefs.discover-nodes-url` |        | 指定发现集群节点列表的方式，每 10 分钟刷新一次。<br />YARN：`yarn`<br />Spark Standalone：`http://spark-master:web-ui-port/json/`<br />Spark ThriftServer：`http://thrift-server:4040/api/v1/applications/`<br />Presto：`http://coordinator:discovery-uri-port/v1/service/presto/` |

### 其他配置

| 配置项                    | 默认值  | 描述                                                                                                                |
| ------------------        | ------  | ------------------------------------------------------------                                                        |
| `juicefs.access-log`      |         | 访问日志的路径。需要所有应用都有写权限，可以配置为 `/tmp/juicefs.access.log`。该文件会自动轮转，保留最近 7 个文件。 |
| `juicefs.superuser`       | `hdfs`  | 超级用户                                                                                                            |
| `juicefs.no-usage-report` | `false` | 是否上报数据，它只上报诸如版本号等使用量数据，不包含任何用户信息。                                                  |

当使用多个 JuiceFS 文件系统时，上述所有配置项均可对单个文件系统指定，需要将文件系统名字 `JFS_NAME` 放在配置项的中间，比如：

```xml
<property>
  <name>juicefs.{JFS_NAME}.meta</name>
  <value>redis://host:port/1</value>
</property>
```

### 常用配置

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
  <value>redis://host:6379/1</value>
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

#### Hadoop 环境配置

将配置参数加入到 Hadoop 配置文件 `core-site.xml` 中：

#### Flink 配置

将配置参数加入 `conf/flink-conf.yaml`。如果只是在 Flink 中使用 JuiceFS, 可以不在 Hadoop 环境配置 JuiceFS，只需要配置 Flink 客户端即可。

### 验证

#### Hadoop

```bash
$ hadoop fs -ls jfs://{JFS_NAME}/
```

#### Hive

```sql
CREATE TABLE IF NOT EXISTS person
(
  name STRING,
  age INT
) LOCATION 'jfs://{JFS_NAME}/tmp/person';
```
