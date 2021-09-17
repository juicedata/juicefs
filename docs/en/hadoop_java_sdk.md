# Use JuiceFS on Hadoop Ecosystem

## Table of Content

- [Requirements](#requirements)
  * [1. Hadoop and related components](#1-hadoop-and-related-components)
  * [2. User permissions](#2-user-permissions)
  * [3. File system](#3-file-system)
  * [4. Memory](#4-memory)
- [Client compilation](#client-compilation)
  * [Linux and macOS](#linux-and-macos)
  * [Windows](#windows)
- [Deploy the client](#deploy-the-client)
  * [Big data Platforms](#big-data-platforms)
  * [Community Components](#community-components)
  * [Client Configurations](#client-configurations)
    + [Core Configurations](#core-configurations)
    + [Cache Configurations](#cache-configurations)
    + [I/O Configurations](#io-configurations)
    + [Other Configurations](#other-configurations)
    + [Multiple file systems configuration](#multiple-file-systems-configuration)
    + [Configuration Example](#configurationexample)
- [Configuration in Hadoop](#configuration-in-hadoop)
  * [CDH6](#cdh6)
  * [HDP](#hdp)
  * [Flink](#flink)
  * [Restart Services](#restart-services)
- [Environmental Verification](#environmental-verification)
  * [Hadoop](#hadoop)
  * [Hive](#hive)
- [Monitoring metrics collection](#monitoring-metrics-collection)
- [Benchmark](#benchmark)
  * [1. Local Benchmark](#1-local-benchmark)
    + [Metadata](#metadata)
    + [I/O Performance](#io-performance)
  * [2. Distributed Benchmark](#2-distributed-benchmark)
    + [Metadata](#metadata-1)
    + [I/O Performance](#io-performance-1)
- [FAQ](#faq)

----

JuiceFS provides [Hadoop-compatible FileSystem](https://hadoop.apache.org/docs/current/hadoop-project-dist/hadoop-common/filesystem/introduction.html) by Hadoop Java SDK. Various applications in the Hadoop ecosystem can smoothly use JuiceFS to store data without changing the code.

## Requirements

### 1. Hadoop and related components

JuiceFS Hadoop Java SDK is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.

### 2. User permissions

JuiceFS use local mapping of `user` and `UID`. So, you should [sync all the needed users and their UIDs](sync_accounts_between_multiple_hosts.md) across the whole Hadoop cluster to avoid permission error. You can also specify a global user list and user group file, please refer to the [relevant configurations](#other-configurations).

### 3. File system

You should first create at least one JuiceFS file system to provide storage for components related to the Hadoop ecosystem through the JuiceFS Java SDK. When deploying the Java SDK, specify the metadata engine address of the created file system in the configuration file.

To create a file system, please refer to [JuiceFS Quick Start Guide](quick_start_guide.md).

> **Note**: If you want to use JuiceFS in a distributed environment, when creating a file system, please plan the object storage and database to be used reasonably to ensure that they can be accessed by each node in the cluster.

### 4. Memory

JuiceFS Hadoop Java SDK need extra 4 * [`juicefs.memory-size`](#io-configurations) off-heap memory at most. By default, up to 1.2 GB of additional memory is required (depends on write load).

## Client compilation

Compilation depends on the following tools:

- [Go](https://golang.org/) 1.15+
- JDK 8+
- [Maven](https://maven.apache.org/) 3.3+
- git
- make
- GCC 5.4+

> **Note**: If Ceph RADOS is used to store data, you need to install librados-dev and build `libjfs.so` with `-tag ceph`.

### Linux and macOS

Clone the repository:

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

Enter the directory and compile:

```shell
$ cd juicefs/sdk/java
$ make
```

After the compilation, you can find the compiled `JAR` file in the `sdk/java/target` directory, including two versions:

- Contains third-party dependent packages: `juicefs-hadoop-X.Y.Z.jar`
- Does not include third-party dependent packages: `original-juicefs-hadoop-X.Y.Z.jar`

It is recommended to use a version that includes third-party dependencies.

### Windows

The client used in the Windows environment needs to be obtained through cross-compilation on Linux or macOS. The compilation depends on [mingw-w64](https://www.mingw-w64.org/), which needs to be installed first.

The steps are the same as compiling on Linux or macOS. For example, on the Ubuntu system, install the `mingw-w64` package first to solve the dependency problem:

```shell
$ sudo apt install mingw-w64
```

Clone and enter the JuiceFS source code directory, execute the following code to compile:

```shell
$ cd juicefs/sdk/java
$ make win
```

> **Note**: No matter which system environment the client is compiled for, the compiled JAR file has the same name and can only be deployed in the matching system environment. For example, when compiled in Linux, it can only be used in the Linux environment. In addition, since the compiled package depends on glibc, it is recommended to compile with a lower version system to ensure better compatibility.

## Deploy the client

To enable each component of the Hadoop ecosystem to correctly identify JuiceFS, the following configurations are required:

1. Place the compiled JAR file and `$JAVA_HOME/lib/tools.jar` into the `classpath` of the component. The installation paths of common big data platforms and components are shown in the table below.
2. Put JuiceFS configurations into the configuration file of each Hadoop ecosystem component (usually `core-site.xml`), see [Client Configurations](#client-configurations) for details.

It is recommended to place the JAR file in a fixed location, and the other locations are called it through symbolic links.

### Big Data Platforms

| Name              | Installing Paths                                             |
| ----------------- | ------------------------------------------------------------ |
| CDH               | `/opt/cloudera/parcels/CDH/lib/hadoop/lib`<br>`/opt/cloudera/parcels/CDH/spark/jars`<br>`/var/lib/impala` |
| HDP               | `/usr/hdp/current/hadoop-client/lib`<br>`/usr/hdp/current/hive-client/auxlib`<br>`/usr/hdp/current/spark2-client/jars` |
| Amazon EMR        | `/usr/lib/hadoop/lib`<br>`/usr/lib/spark/jars`<br>`/usr/lib/hive/auxlib` |
| Alibaba Cloud EMR | `/opt/apps/ecm/service/hadoop/*/package/hadoop*/share/hadoop/common/lib`<br>`/opt/apps/ecm/service/spark/*/package/spark*/jars`<br>`/opt/apps/ecm/service/presto/*/package/presto*/plugin/hive-hadoop2`<br>`/opt/apps/ecm/service/hive/*/package/apache-hive*/lib`<br>`/opt/apps/ecm/service/impala/*/package/impala*/lib` |
| Tencent Cloud EMR | `/usr/local/service/hadoop/share/hadoop/common/lib`<br>`/usr/local/service/presto/plugin/hive-hadoop2`<br>`/usr/local/service/spark/jars`<br>`/usr/local/service/hive/auxlib` |
| UCloud UHadoop    | `/home/hadoop/share/hadoop/common/lib`<br>`/home/hadoop/hive/auxlib`<br>`/home/hadoop/spark/jars`<br>`/home/hadoop/presto/plugin/hive-hadoop2` |
| Baidu Cloud EMR   | `/opt/bmr/hadoop/share/hadoop/common/lib`<br>`/opt/bmr/hive/auxlib`<br>`/opt/bmr/spark2/jars` |

### Community Components

| Name   | Installing Paths                     |
| ------ | ------------------------------------ |
| Spark  | `${SPARK_HOME}/jars`                 |
| Presto | `${PRESTO_HOME}/plugin/hive-hadoop2` |
| Flink  | `${FLINK_HOME}/lib`                  |

### Client Configurations

Please refer to the following table to set the relevant parameters of the JuiceFS file system and write it into the configuration file, which is generally `core-site.xml`.

#### Core Configurations

| Configuration                    | Default Value                | Description                                                  |
| -------------------------------- | ---------------------------- | ------------------------------------------------------------ |
| `fs.jfs.impl`                    | `io.juicefs.JuiceFileSystem` | Specify the storage implementation to be used. By default, `jfs://` is used. If you want to use `cfs://` as the scheme, just modify it to `fs.cfs.impl`. When using `cfs://`, it is still access the data in JuiceFS. |
| `fs.AbstractFileSystem.jfs.impl` | `io.juicefs.JuiceFS`         |                                                              |
| `juicefs.meta`                   |                              | Specify the metadata engine address of the pre-created JuiceFS file system. You can configure multiple file systems for the client at the same time through the format of `juicefs.{vol_name}.meta`. |

#### Cache Configurations

| Configuration                | Default Value | Description                                                  |
| ---------------------------- | ------------- | ------------------------------------------------------------ |
| `juicefs.cache-dir`          |               | Directory paths of local cache. Use colon to separate multiple paths. Also support wildcard in path. **It's recommended create these directories manually and set `0777` permission so that different applications could share the cache data.** |
| `juicefs.cache-size`         | 0             | Maximum size of local cache in MiB. It's the total size when set multiple cache directories. |
| `juicefs.cache-full-block`   | `true`        | Whether cache every read blocks, `false` means only cache random/small read blocks. |
| `juicefs.free-space`         | 0.1           | Min free space ratio of cache directory                      |
| `juicefs.attr-cache`         | 0             | Expire of attributes cache in seconds                        |
| `juicefs.entry-cache`        | 0             | Expire of file entry cache in seconds                        |
| `juicefs.dir-entry-cache`    | 0             | Expire of directory entry cache in seconds                   |
| `juicefs.discover-nodes-url` |               | The URL to discover cluster nodes, refresh every 10 minutes.<br /><br />YARN: `yarn`<br />Spark Standalone: `http://spark-master:web-ui-port/json/`<br />Spark ThriftServer: `http://thrift-server:4040/api/v1/applications/`<br />Presto: `http://coordinator:discovery-uri-port/v1/service/presto/` |

#### I/O Configurations

| Configuration            | Default Value | Description                                     |
| ------------------------ | ------------- | ----------------------------------------------- |
| `juicefs.max-uploads`    | 20            | The max number of connections to upload         |
| `juicefs.get-timeout`    | 5             | The max number of seconds to download an object |
| `juicefs.put-timeout`    | 60            | The max number of seconds to upload an object   |
| `juicefs.memory-size`    | 300           | Total read/write buffering in MiB               |
| `juicefs.prefetch`       | 1             | Prefetch N blocks in parallel                   |
| `juicefs.upload-limit`   | 0             | Bandwidth limit for upload in Mbps              |
| `juicefs.download-limit` | 0             | Bandwidth limit for download in Mbps            |

#### Other Configurations

| Configuration             | Default Value | Description                                                  |
| ------------------------- | ------------- | ------------------------------------------------------------ |
| `juicefs.debug`           | `false`       | Whether enable debug log                                     |
| `juicefs.access-log`      |               | Access log path. Ensure Hadoop application has write permission, e.g. `/tmp/juicefs.access.log`. The log file will rotate  automatically to keep at most 7 files. |
| `juicefs.superuser`       | `hdfs`        | The super user                                               |
| `juicefs.users`           | `null`        | The path of username and UID list file, e.g. `jfs://name/etc/users`. The file format is `<username>:<UID>`, one user per line. |
| `juicefs.groups`          | `null`        | The path of group name, GID and group members list file, e.g. `jfs://name/etc/groups`. The file format is `<group-name>:<GID>:<username1>,<username2>`, one group per line. |
| `juicefs.umask`           | `null`        | The umask used when creating files and directories (e.g. `0022`), default value is `fs.permissions.umask-mode`. |
| `juicefs.push-gateway`    |               | [Prometheus Pushgateway](https://github.com/prometheus/pushgateway) address, format is `<host>:<port>`. |
| `juicefs.push-interval`   | 10            | Prometheus push interval in seconds                          |
| `juicefs.push-auth`       |               | [Prometheus basic auth](https://prometheus.io/docs/guides/basic-auth) information, format is `<username>:<password>`. |
| `juicefs.fast-resolve`    | `true`        | Whether enable faster metadata lookup using Redis Lua script |
| `juicefs.no-usage-report` | `false`       | Whether disable usage reporting. JuiceFS only collects anonymous usage data (e.g. version number), no user or any sensitive data will be collected. |

#### Multiple file systems configuration

When multiple JuiceFS file systems need to be used at the same time, all the above configuration items can be specified for a specific file system. You only need to put the file system name in the middle of the configuration item, such as `jfs1` and `jfs2` in the following example:

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

#### Configuration Example

The following is a commonly used configuration example. Please replace the `{HOST}`, `{PORT}` and `{DB}` variables in the `juicefs.meta` configuration with actual values.

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

## Configuration in Hadoop

Please refer to the aforementioned configuration tables and add configuration parameters to the Hadoop configuration file `core-site.xml`.

### CDH6

If you are using CDH 6, in addition to modifying `core-site`, you also need to modify `mapreduce.application.classpath` through the YARN service interface, adding:

```shell
$HADOOP_COMMON_HOME/lib/juicefs-hadoop.jar
```

### HDP

In addition to modifying `core-site`, you also need to modify the configuration `mapreduce.application.classpath` through the MapReduce2 service interface and add it at the end (variables do not need to be replaced):

```shell
/usr/hdp/${hdp.version}/hadoop/lib/juicefs-hadoop.jar
```

### Flink

Add configuration parameters to `conf/flink-conf.yaml`. If you only use JuiceFS in Flink, you don't need to configure JuiceFS in the Hadoop environment, you only need to configure the Flink client.

### Restart Services

When the following components need to access JuiceFS, they should be restarted.

> **Note**: Before restart, you need to confirm JuiceFS related configuration has been written to the configuration file of each component, usually you can find them in `core-site.xml` on the machine where the service of the component was deployed.

| Components | Services                   |
| ---------- | -------------------------- |
| Hive       | HiveServer<br />Metastore  |
| Spark      | ThriftServer               |
| Presto     | Coordinator<br />Worker    |
| Impala     | Catalog Server<br />Daemon |
| HBase      | Master<br />RegionServer   |

HDFS, Hue, ZooKeeper and other services don't need to be restarted.

When `Class io.juicefs.JuiceFileSystem not found` or `No FilesSystem for scheme: jfs` exceptions was occurred after restart, reference [FAQ](#faq).

## Environmental Verification

After the deployment of the JuiceFS Java SDK, the following methods can be used to verify the success of the deployment.

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

## Monitoring metrics collection

JuiceFS Hadoop Java SDK supports reporting metrics to [Prometheus Pushgateway](https://github.com/prometheus/pushgateway), then you can use [Grafana](https://grafana.com) and [dashboard template](grafana_template.json) to visualize these metrics.

Enable metrics reporting through following configurations:

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

**Note**: Each process using JuiceFS Hadoop Java SDK will have a unique metric, and Pushgateway will always remember all the collected metrics, resulting in the continuous accumulation of metrics and taking up too much memory, which will also slow down Prometheus crawling metrics. It is recommended to clean up metrics which `job` is `juicefs` on Pushgateway regularly.

It is recommended to use the following command to clean up once every hour. The running Hadoop Java SDK will continue to update after the metrics are cleared, which basically does not affect the use.

> ```bash
> $ curl -X DELETE http://host:9091/metrics/job/juicefs
> ```

For a description of all monitoring metrics, please refer to [JuiceFS Metrics](p8s_metrics.md).

## Benchmark

Here are a series of methods to use the built-in stress testing tool of the JuiceFS client to test the performance of the client environment that has been successfully deployed.


### 1. Local Benchmark
#### Metadata

- **create**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench create -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

  This command will create 10000 empty files

- **open**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench open -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

  This command will open 10000 files without reading data

- **rename**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench rename -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

- **delete**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench delete -files 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench -local
  ```

- **For reference**

| Operation | TPS  | Latency (ms) |
| --------- | ---- | ------------ |
| create    | 644  | 1.55         |
| open      | 3467 | 0.29         |
| rename    | 483  | 2.07         |
| delete    | 506  | 1.97         |

#### I/O Performance

- **sequential write**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -write -size 20000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO -local
  ```

- **sequential read**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -read -size 20000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO -local
  ```

  When run the cmd for the second time, the result may be much better than the first run. It's because the data was cached in memory, just clean the local disk cache.

- **For reference**

| Operation | Throughput (MB/s) |
| --------- | ----------------- |
| write     | 647               |
| read      | 111               |

If the network bandwidth of the machine is relatively low, it can generally reach the network bandwidth bottleneck.

### 2. Distributed Benchmark

The following command will start the MapReduce distributed task to test the metadata and IO performance. During the test, it is necessary to ensure that the cluster has sufficient resources to start the required map tasks.

Computing resources used in this test:

- **Server**: 4 cores and 32 GB memory, burst bandwidth 5Gbit/s x 3
- **Database**: Alibaba Cloud Redis 5.0 Community 4G Master-Slave Edition

#### Metadata

- **create**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench create -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  10 map task, each has 10 threads, each thread create 1000 empty file. 100000 files in total

- **open**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench open -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  10 map task, each has 10 threads, each thread open 1000 file. 100000 files in total

- **rename**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench rename -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  10 map task, each has 10 threads, each thread rename 1000 file. 100000 files in total

- **delete**

  ```shell
  hadoop jar juicefs-hadoop.jar nnbench delete -maps 10 -threads 10 -files 1000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/NNBench
  ```

  10 map task, each has 10 threads, each thread delete 1000 file. 100000 files in total

- **For reference**

  - 10 threads

  | Operation | IOPS | Latency (ms) |
  | --------- | ---- | ------------ |
  | create    | 4178 | 2.2          |
  | open      | 9407 | 0.8          |
  | rename    | 3197 | 2.9          |
  | delete    | 3060 | 3.0          |

  - 100 threads

  | Operation | IOPS  | Latency (ms) |
  | --------- | ----  | ------------ |
  | create    | 11773 | 7.9          |
  | open      | 34083 | 2.4          |
  | rename    | 8995  | 10.8         |
  | delete    | 7191  | 13.6         |

#### I/O Performance

- **sequential write**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -write -maps 10 -size 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO
  ```

  10 map task, each task write 10000MB random data sequentially

- **sequential read**

  ```shell
  hadoop jar juicefs-hadoop.jar dfsio -read -maps 10 -size 10000 -baseDir jfs://{JFS_NAME}/tmp/benchmarks/DFSIO
  ```

  10 map task, each task read 10000MB random data sequentially


- **For reference**

| Operation | Average throughput (MB/s) | Total Throughput (MB/s) |
| --------- | ------------------------- | ----------------------- |
| write     | 198                       | 1835                    |
| read      | 124                       | 1234                    |


## FAQ

### 1. `Class io.juicefs.JuiceFileSystem not found` exception

It means JAR file was not loaded, you can verify it by `lsof -p {pid} | grep juicefs`.

You should check whether the JAR file was located properly, or other users have the read permission.

Some Hadoop distribution also need to modify `mapred-site.xml` and put the JAR file location path to the end of the parameter `mapreduce.application.classpath`.

### 2. `No FilesSystem for scheme: jfs` exception

It means JuiceFS Hadoop Java SDK was not configured properly, you need to check whether there is JuiceFS related configuration in the `core-site.xml` of the component configuration.
