# 在 Hadoop 环境使用 JuiceFS Java SDK

JuiceFS 提供兼容 HDFS 的 Java 客户端来支持 Hadoop 生态中的各种应用。

## 编译

```shell
cd sdk/java
make
```

## 部署 JuiceFS Java SDK

当编译完成后，你可以在 sdk/java/target 目录下找到编译好的 jar 文件。将此文件放到 hadoop 生态各组件的 classpath 里。 常见路径如下：

建议将 jar 文件放置在一个地方，然后其他地方均通过符号链接的方式放置。

发行版：

- CDH:
  ```shell
  /opt/cloudera/parcels/CDH/lib/hadoop/lib
  /opt/cloudera/parcels/CDH/spark/jars
  /var/lib/impala
  ```
- HDP
  ```shell
   /usr/hdp/current/hadoop-client/lib  
   /usr/hdp/current/hive-client/auxlib
   /usr/hdp/current/spark2-client/jars 
   ```
- AmazonEMR
  ```shell
  /usr/lib/hadoop/lib
  /usr/lib/spark/jars
  /usr/lib/hive/auxlib
  ```

- AliyunEMR
  ```shell
  /opt/apps/ecm/service/hadoop/*/package/hadoop*/share/hadoop/common/lib
  /opt/apps/ecm/service/spark/*/package/spark*/jars
  /opt/apps/ecm/service/presto/*/package/presto*/plugin/hive-hadoop2
  /opt/apps/ecm/service/hive/*/package/apache-hive*/lib
  /opt/apps/ecm/service/impala/*/package/impala*/lib
  ```


- TencentEMR
  ```shell
  /usr/local/service/hadoop/share/hadoop/common/lib
  /usr/local/service/presto/plugin/hive-hadoop2
  /usr/local/service/spark/jars
  /usr/local/service/hive/auxlib
  ```


- UHadoop
  ```shell
  /home/hadoop/share/hadoop/common/lib
  /home/hadoop/hive/auxlib
  /home/hadoop/spark/jars
  /home/hadoop/presto/plugin/hive-hadoop2
  ```


- BMR
  ```shell
  /opt/bmr/hadoop/share/hadoop/common/lib/
  /opt/bmr/hive/auxlib
  /opt/bmr/spark2/jars
  ```

社区开源组件：

- Spark

  ```shell
  ${SPARK_HOME}/jars
  ```

- Presto
  ```shell
  ${PRESTO_HOME}/plugin/hive-hadoop2
  ```

## 配置参数

### 核心配置

| 配置项                         | 默认值                     | 描述                                                         |
| ------------------------------ | -------------------------- | ------------------------------------------------------------ |
| fs.jfs.impl                    | io.juicefs.JuiceFileSystem | 指定 `jfs://` 这个存储类型所使用的实现。JuiceFS 支持修改 scheme，例如想要使用 cfs 作为 scheme，则将 fs.cfs.impl 的实现修改为此配置即可，当使用 cfs:// 访问数据的时候，仍然是访问的 JuiceFS 的数据。 |
| fs.AbstractFileSystem.jfs.impl | io.juicefs.JuiceFS         |                                                              |
| juicefs.meta                   |                            | redis 地址                                                   |
| juicefs.accesskey              |                            | 对象存储的访问ID（Access Key ID）。如果计算节点已经有访问对象存储的权限，则无需提供。 |
| juicefs.secretkey              |                            | 对象存储的私钥 (Secret Access Key)。如果计算节点已经有访问对象存储的权限，则无需提供。 |

### 缓存配置

| 配置项                     | 默认值 | 描述                                                         |
| -------------------------- | ------ | ------------------------------------------------------------ |
| juicefs.cache-dir          |        | 本地缓存目录，可以指定多个文件夹，用冒号 `:` 分隔，也可以使用通配符（比如 `*` ）。**通常应用没有权限创建这些目录，需要手动创建并给予 0777
权限，便于多个应用共享缓存数据** 。 |
| juicefs.cache-size         | 0      | 磁盘缓存容量，单位 MB。如果配置多个目录，这是所有缓存目录的空间总和。 |
| juicefs.discover-nodes-url |        | 指定发现集群节点列表的方式，每 10 分钟刷新一次。  <br />Yarn: `yarn` <br />Spark Standalone：`http://spark-master:web-ui-port/json/`<br />Spark ThriftServer: `http://thrift-server:4040/api/v1/applications/`<br />Presto：`http://coordinator:discovery-uri-port/v1/service/presto/` |

### 其他配置

| 配置项             | 默认值 | 描述                                                         |
| ------------------ | ------ | ------------------------------------------------------------ |
| juicefs.access-log |        | 访问日志的路径。需要所有应用都有写权限，可以配置为 `/tmp/juicefs.access.log` 。该文件会自动轮转，保留最近 7 个文件。 |
| juicefs.superuser  | hdfs   | 超级用户                                                     |
| juicefs.no-usage-report  | false   | 是否上报数据，它只上报诸如版本号等使用量数据，不包含任何用户信息                                                     |

当使用多个 JuiceFS 文件系统时，上述所有配置项均可对单个文件系统指定，需要将文件系统名字 JFS_NAME 放在配置项的中间，比如：

```xml

<property>
  <name>juicefs.{JFS_NAME}.meta</name>
  <value>redis://host:port/1</value>
</property>
```

### 常用配置

将以下配置参数加入到 Hadoop 配置文件 core-site.xml 中。

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

### 验证

- hadoop

```bash
hadoop fs -ls jfs://{JFS_NAME}/
```

- hive

```sql
create table if not exists person
(
    name
    string,
    age
    int
) location 'jfs://{JFS_NAME}/tmp/person';
```