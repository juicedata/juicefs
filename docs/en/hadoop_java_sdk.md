# Use JuiceFS Hadoop Java SDK

JuiceFS provides [Hadoop-compatible FileSystem](https://hadoop.apache.org/docs/current/hadoop-project-dist/hadoop-common/filesystem/introduction.html) by Hadoop Java SDK to support variety of components in Hadoop ecosystem.

## Hadoop Compatibility

JuiceFS Hadoop Java SDK is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.

In order to make JuiceFS works with other components, it usually takes 2 steps:

1. Put JAR file into the classpath of each Hadoop ecosystem component.
2. Put JuiceFS conf into the conf file of each Hadoop ecosystem component(usually core-site.xml).

## Compiling

You need first installing Go 1.13+, JDK 8+ and Maven, then run following commands:

```shell
$ cd sdk/java
$ make
```

## Deploy JuiceFS Hadoop Java SDK

After compiling you could find the JAR file in `sdk/java/target` directory, e.g. `juicefs-hadoop-0.10.0.jar`. Beware that file with `original-` prefix, it doesn't contain third-party dependencies. It's recommended to use the JAR file with third-party dependencies.

**Note: The SDK could only be deployed to same operating system as it be compiled. For example, if you compile SDK in Linux then you must deploy it to Linux.**

Then put the JAR file and `$JAVA_HOME/lib/tools.jar` to the classpath of each Hadoop ecosystem component. It's recommended create a symbolic link to the JAR file. The following tables describe where the SDK be placed.

### Hadoop Distribution

| Name              | Installing Paths                                                                                                                                                                                                                                                                                                           |
| ----              | ----------------                                                                                                                                                                                                                                                                                                           |
| CDH               | `/opt/cloudera/parcels/CDH/lib/hadoop/lib`<br>`/opt/cloudera/parcels/CDH/spark/jars`<br>`/var/lib/impala`                                                                                                                                                                                                                  |
| HDP               | `/usr/hdp/current/hadoop-client/lib`<br>`/usr/hdp/current/hive-client/auxlib`<br>`/usr/hdp/current/spark2-client/jars`                                                                                                                                                                                                     |
| Amazon EMR        | `/usr/lib/hadoop/lib`<br>`/usr/lib/spark/jars`<br>`/usr/lib/hive/auxlib`                                                                                                                                                                                                                                                   |
| Alibaba Cloud EMR | `/opt/apps/ecm/service/hadoop/*/package/hadoop*/share/hadoop/common/lib`<br>`/opt/apps/ecm/service/spark/*/package/spark*/jars`<br>`/opt/apps/ecm/service/presto/*/package/presto*/plugin/hive-hadoop2`<br>`/opt/apps/ecm/service/hive/*/package/apache-hive*/lib`<br>`/opt/apps/ecm/service/impala/*/package/impala*/lib` |
| Tencent Cloud EMR | `/usr/local/service/hadoop/share/hadoop/common/lib`<br>`/usr/local/service/presto/plugin/hive-hadoop2`<br>`/usr/local/service/spark/jars`<br>`/usr/local/service/hive/auxlib`                                                                                                                                              |
| UCloud UHadoop    | `/home/hadoop/share/hadoop/common/lib`<br>`/home/hadoop/hive/auxlib`<br>`/home/hadoop/spark/jars`<br>`/home/hadoop/presto/plugin/hive-hadoop2`                                                                                                                                                                             |
| Baidu Cloud EMR   | `/opt/bmr/hadoop/share/hadoop/common/lib`<br>`/opt/bmr/hive/auxlib`<br>`/opt/bmr/spark2/jars`                                                                                                                                                                                                                              |

### CDH6

Besides `core-site`，you also need to configure `mapreduce.application.classpath` of the YARN component, add:

```shell
$HADOOP_COMMON_HOME/lib/juicefs-hadoop.jar
```

### HDP

Besides `core-site` 外，you also need to configure `mapreduce.application.classpath` of the MapReduce2 component, add:

```shell
/usr/hdp/${hdp.version}/hadoop/lib/juicefs-hadoop.jar
```

### Flink 配置

Write JuiceFS conf to `conf/flink-conf.yaml` of Flink, you can just do it in Flink Client machine.

### Community Components

| Name   | Installing Paths                     |
| ----   | ----------------                     |
| Spark  | `${SPARK_HOME}/jars`                 |
| Presto | `${PRESTO_HOME}/plugin/hive-hadoop2` |
| Flink  | `${FLINK_HOME}/lib`                  |

## Configurations

### Core Configurations

| Configuration                    | Default Value                | Description                                                                                                                                               |
| -------------                    | -------------                | -----------                                                                                                                                               |
| `fs.jfs.impl`                    | `io.juicefs.JuiceFileSystem` | The FileSystem implementation for `jfs://` URIs. If you wanna use different schema (e.g. `cfs://`), you could rename this configuration to `fs.cfs.impl`. |
| `fs.AbstractFileSystem.jfs.impl` | `io.juicefs.JuiceFS`         |                                                                                                                                                           |
| `juicefs.meta`                   |                              | Redis URL. Its format is `redis://<user>:<password>@<host>:<port>/<db>`.                                                                                  |
| `juicefs.accesskey`              |                              | Access key of object storage. See [this document](how_to_setup_object_storage.md) to learn how to get access key for different object storage.            |
| `juicefs.secretkey`              |                              | Secret key of object storage. See [this document](how_to_setup_object_storage.md) to learn how to get secret key for different object storage.            |

### Cache Configurations

| Configuration                | Default Value | Description                                                                                                                                                                                                                                                                                           |
| -------------                | ------------- | -----------                                                                                                                                                                                                                                                                                           |
| `juicefs.cache-dir`          |               | Directory paths of local cache. Use colon to separate multiple paths. Also support wildcard in path. **It's recommended create these directories manually and set `0777` permission so that different applications could share the cache data.**                                                      |
| `juicefs.cache-size`         | 0             | Maximum size of local cache in MiB. It's the total size when set multiple cache directories.                                                                                                                                                                                                          |
| `juicefs.discover-nodes-url` |               | The URL to discover cluster nodes, refresh every 10 minutes.<br /><br />YARN: `yarn`<br />Spark Standalone: `http://spark-master:web-ui-port/json/`<br />Spark ThriftServer: `http://thrift-server:4040/api/v1/applications/`<br />Presto: `http://coordinator:discovery-uri-port/v1/service/presto/` |

### Others

| Configuration             | Default Value | Description                                                                                                                                                       |
| -------------             | ------------- | -----------                                                                                                                                                       |
| `juicefs.access-log`      |               | Access log path. Ensure Hadoop application has write permission, e.g. `/tmp/juicefs.access.log`. The log file will rotate  automatically to keep at most 7 files. |
| `juicefs.superuser`       | `hdfs`        | The super user                                                                                                                                                    |
| `juicefs.no-usage-report` | `false`       | Whether disable usage reporting. JuiceFS only collects anonymous usage data (e.g. version number), no user or any sensitive data will be collected.               |

When you use multiple JuiceFS file systems, all these configurations could be set to specific file system alone. You need put file system name in the middle of configuration, for example (replace `{JFS_NAME}` with appropriate value):

```xml
<property>
  <name>juicefs.{JFS_NAME}.meta</name>
  <value>redis://host:port/1</value>
</property>
```

### Configurations Example

**Note: Replace `{HOST}`, `{PORT}` and `{DB}` in `juicefs.meta` with appropriate values.**

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

### Configuration in Hadoop

Add configurations to `core-site.xml`.

### Configuration in Flink

Add configurations to `conf/flink-conf.yaml`. You could only setup Flink client without modify configurations in Hadoop.

## Restart Services


When those components below need to access JuiceFS, they should be restarted.

**Note: Before restart, you need to confirm JuiceFS related conf has been writen to the conf file of each component,
usually you can find them in core-site.xml on the machine where the service of the component was deployed.**

| Components | Services                     |
| ------ | -------------------------- |
| Hive   | HiveServer<br />Metastore  |
| Spark  | ThriftServer               |
| Presto | Coordinator<br />Worker    |
| Impala | Catalog Server<br />Daemon |
| HBase  | Master<br />RegionServer   |

HDFS，HUE，ZooKeeper etc don't need to restart.

When `Class io.juicefs.JuiceFileSystem not found` or `No FilesSystem for scheme: jfs` exceptions was occurred after restart, 
reference [FAQ](#faq)

## Verification

### Hadoop

```bash
$ hadoop fs -ls jfs://{JFS_NAME}/
```

### Hive

```sql
CREATE TABLE IF NOT EXISTS person
(
  name STRING,
  age INT
) LOCATION 'jfs://{JFS_NAME}/tmp/person';
```

## FAQ

### `Class io.juicefs.JuiceFileSystem not found` exception

It means JAR file was not loaded, you can verify it by `lsof -p {pid} | grep juicefs`. 

You should check whether the JAR file was located properly, or it has the read permission by other users.

Some hadoop distribution also need to modify `mapred-site.xml` and put the JAR file location path to the end of the param `mapreduce.application.classpath`.

### `No FilesSystem for scheme: jfs` exception

It means JuiceFS conf was not configured properly, you need check `core-site.xml` on the local machine.