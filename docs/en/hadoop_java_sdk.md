# Use JuiceFS Hadoop Java SDK

JuiceFS provides [Hadoop-compatible FileSystem](https://hadoop.apache.org/docs/current/hadoop-project-dist/hadoop-common/filesystem/introduction.html) by Hadoop Java SDK to support variety of components in Hadoop ecosystem.

> **NOTICE**:
>
> JuiceFS use local mapping of user and UID. So, you should [sync all the needed users and their UIDs](sync_accounts_between_multiple_hosts.md) across the whole Hadoop cluster to avoid permission error. You can also specify a global user list and user group file, please refer to the [relevant configurations](#other-configurations).

## Hadoop Compatibility

JuiceFS Hadoop Java SDK is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.

In order to make JuiceFS works with other components, it usually takes 2 steps:

1. Put JAR file into the classpath of each Hadoop ecosystem component.
2. Put JuiceFS configurations into the configuration file of each Hadoop ecosystem component (usually `core-site.xml`).

## Compiling

You need first installing Go 1.13+, JDK 8+ and Maven, then run following commands:

```shell
$ cd sdk/java
$ make
```

> **Tip**: For users in China, it's recommended to set a local Maven mirror to speed-up compilation, e.g. [Aliyun Maven Mirror](https://maven.aliyun.com).

## Deploy JuiceFS Hadoop Java SDK

After compiling you could find the JAR file in `sdk/java/target` directory, e.g. `juicefs-hadoop-0.10.0.jar`. Beware that file with `original-` prefix, it doesn't contain third-party dependencies. It's recommended to use the JAR file with third-party dependencies.

> **Note**: The SDK could only be deployed to same operating system as it be compiled. For example, if you compile SDK in Linux then you must deploy it to Linux.

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
| `juicefs.cache-full-block`   | `true`        | Whether cache every read blocks, `false` means only cache random/small read blocks.                                                                                                                                                                                                                   |
| `juicefs.free-space`         | 0.2           | Min free space ratio of cache directory                                                                                                                                                                                                                                                               |
| `juicefs.discover-nodes-url` |               | The URL to discover cluster nodes, refresh every 10 minutes.<br /><br />YARN: `yarn`<br />Spark Standalone: `http://spark-master:web-ui-port/json/`<br />Spark ThriftServer: `http://thrift-server:4040/api/v1/applications/`<br />Presto: `http://coordinator:discovery-uri-port/v1/service/presto/` |

### I/O Configurations

| Configuration         | Default Value | Description                                     |
| -------------         | ------------- | -----------                                     |
| `juicefs.max-uploads` | 50            | The max number of connections to upload         |
| `juicefs.get-timeout` | 5             | The max number of seconds to download an object |
| `juicefs.put-timeout` | 60            | The max number of seconds to upload an object   |
| `juicefs.memory-size` | 300           | Total read/write buffering in MiB               |
| `juicefs.prefetch`    | 3             | Prefetch N blocks in parallel                   |

### Other Configurations

| Configuration             | Default Value | Description                                                                                                                                                                 |
| -------------             | ------------- | -----------                                                                                                                                                                 |
| `juicefs.debug`           | `false`       | Whether enable debug log                                                                                                                                                    |
| `juicefs.access-log`      |               | Access log path. Ensure Hadoop application has write permission, e.g. `/tmp/juicefs.access.log`. The log file will rotate  automatically to keep at most 7 files.           |
| `juicefs.superuser`       | `hdfs`        | The super user                                                                                                                                                              |
| `juicefs.users`           | `null`        | The path of username and UID list file, e.g. `jfs://name/etc/users`. The file format is `<username>:<UID>`, one user per line.                                              |
| `juicefs.groups`          | `null`        | The path of group name, GID and group members list file, e.g. `jfs://name/etc/groups`. The file format is `<group-name>:<GID>:<username1>,<username2>`, one group per line. |
| `juicefs.umask`           | `null`        | The umask used when creating files and directories (e.g. `0022`), default value is `fs.permissions.umask-mode`.                                                             |
| `juicefs.push-gateway`    |               | [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) address, format is `<host>:<port>`.                                                                     |
| `juicefs.push-interval`   | 10            | Prometheus push interval in seconds                                                                                                                                         |
| `juicefs.push-auth`       |               | [Prometheus basic auth](https://prometheus.io/docs/guides/basic-auth) information, format is `<username>:<password>`.                                                       |
| `juicefs.fast-resolve`    | `true`        | Whether enable faster metadata lookup using Redis Lua script                                                                                                                |
| `juicefs.no-usage-report` | `false`       | Whether disable usage reporting. JuiceFS only collects anonymous usage data (e.g. version number), no user or any sensitive data will be collected.                         |

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

#### CDH 6

Besides `core-site`, you also need to configure `mapreduce.application.classpath` of the YARN component, add:

```shell
$HADOOP_COMMON_HOME/lib/juicefs-hadoop.jar
```

#### HDP

Besides `core-site`, you also need to configure `mapreduce.application.classpath` of the MapReduce2 component, add (variables do not need to be replaced):

```shell
/usr/hdp/${hdp.version}/hadoop/lib/juicefs-hadoop.jar
```

### Configuration in Flink

Add configurations to `conf/flink-conf.yaml`. You could only setup Flink client without modify configurations in Hadoop.

## Restart Services

When the following components need to access JuiceFS, they should be restarted.

> **Note**: Before restart, you need to confirm JuiceFS related configuration has been written to the configuration file of each component, usually you can find them in `core-site.xml` on the machine where the service of the component was deployed.

| Components | Services                   |
| ---------- | --------                   |
| Hive       | HiveServer<br />Metastore  |
| Spark      | ThriftServer               |
| Presto     | Coordinator<br />Worker    |
| Impala     | Catalog Server<br />Daemon |
| HBase      | Master<br />RegionServer   |

HDFS, Hue, ZooKeeper and other services don't need to be restarted.

When `Class io.juicefs.JuiceFileSystem not found` or `No FilesSystem for scheme: jfs` exceptions was occurred after restart, reference [FAQ](#faq).

## Verification

### Hadoop

```bash
$ hadoop fs -ls jfs://{JFS_NAME}/
```

> **Note**: The `JFS_NAME` is the volume name when you format JuiceFS file system.

### Hive

```sql
CREATE TABLE IF NOT EXISTS person
(
  name STRING,
  age INT
) LOCATION 'jfs://{JFS_NAME}/tmp/person';
```

## Metrics

JuiceFS Hadoop Java SDK supports reporting metrics to [Prometheus Pushgateway](https://github.com/prometheus/pushgateway), then you can use [Grafana](https://grafana.com) and [dashboard template](k8s_grafana_template.json) to visualize these metrics.

Enable metrics reporting through following configurations:

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

> **Note**: Each process using JuiceFS Hadoop Java SDK will have a unique metric, and Pushgateway will always remember all the collected metrics, resulting in the continuous accumulation of metrics and taking up too much memory, which will also slow down Prometheus crawling metrics. It is recommended to clean up metrics which `job` is `juicefs` on Pushgateway regularly. It is recommended to use the following command to clean up once every hour. The running Hadoop Java SDK will continue to update after the metrics are cleared, which basically does not affect the use.
>
> ```bash
> $ curl -X DELETE http://host:9091/metrics/job/juicefs
> ```

For a description of all monitoring metrics, please refer to [JuiceFS Metrics](p8s_metrics.md).

## Benchmark

JuiceFS provides some benchmark tools for you when JuiceFS has been deployed

### Local environment
#### Meta

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation create -numberOfFiles 10000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

  It creates 10000 empty files without write data

- open

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation open -numberOfFiles 10000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

  It opens 10000 files without read data

- rename

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation rename -numberOfFiles 10000 -bytesPerBlock 134217728 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

- delete

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBenchWithoutMR -operation delete -numberOfFiles 10000 -bytesPerBlock 134217728 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench_local
  ```

- for reference

| Operation | TPS  | Delay (ms) |
| --------- | ---  | ---------- |
| create    | 546  | 1.83       |
| open      | 1135 | 0.88       |
| rename    | 364  | 2.75       |
| delete    | 289  | 3.46       |

#### IO Performance

- sequential write

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestFSIO -write -fileSize 20000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

- sequential read

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestFSIO -read -fileSize 20000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  When run the cmd for the second time, the result may be much better than the first run. It's because the data was cached in memory, just clean the local disk cache.

- for reference

| Operation | Throughput (MB/s) |
| --------- | ----------------- |
| write     | 453               |
| read      | 141               |

### Distribute Benchmark

Distribute benchmark use MapReduce program to test meta and IO throughput performance

Enough resources should be provided to make sure all Map task can be started at the same time

We use 3 4c32g ECS (5Gbit/s) and Aliyun Redis 5.0 4G redis for the benchmark

#### Meta

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation create -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  10 map task, each has 10 threads, each thread create 1000 empty file. 100000 files in total

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation open -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  10 map task, each has 10 threads, each thread open 1000 file. 100000 files in total

- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation rename -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  10 map task, each has 10 threads, each thread rename 1000 file. 100000 files in total


- create

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.NNBench -operation delete -threadsPerMap 10 -maps 10 -numberOfFiles 1000 -baseDir jfs://{JFS_NAME}/benchmarks/nnbench
  ```

  10 map task, each has 10 threads, each thread delete 1000 file. 100000 files in total

- for reference

    - 10 threads

  | Operation | TPS  | Delay (ms) |
  | --------- | ---  | ---------- |
  | create    | 2307 | 3.6        |
  | open      | 3215 | 2.3        |
  | rename    | 1700 | 5.22       |
  | delete    | 1378 | 6.7        |

    - 100 threads

  | Operation | TPS   | Delay (ms) |
  | --------- | ---   | ---------- |
  | create    | 8375  | 11.5       |
  | open      | 12691 | 7.5        |
  | rename    | 5343  | 18.4       |
  | delete    | 3576  | 27.6       |

#### IO Performance

- sequential write

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestDFSIO -write -nrFiles 10 -fileSize 10000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  10 map task, each task write 10000MB random data sequentially

- sequential read

  ```shell
  hadoop jar juicefs-hadoop.jar io.juicefs.bench.TestDFSIO -read -nrFiles 10 -fileSize 10000 -baseDir jfs://{JFS_NAME}/benchmarks/fsio
  ```

  10 map task, each task read 10000MB random data sequentially


- for reference

| Operation | Total Throughput (MB/s) |
| --------- | ----------------------- |
| write     | 1792                    |
| read      | 1409                    |


## FAQ

### `Class io.juicefs.JuiceFileSystem not found` exception

It means JAR file was not loaded, you can verify it by `lsof -p {pid} | grep juicefs`.

You should check whether the JAR file was located properly, or other users have the read permission.

Some Hadoop distribution also need to modify `mapred-site.xml` and put the JAR file location path to the end of the parameter `mapreduce.application.classpath`.

### `No FilesSystem for scheme: jfs` exception

It means JuiceFS Hadoop Java SDK was not configured properly, you need to check whether there is JuiceFS related configuration in the `core-site.xml` of the component configuration.
