---
sidebar_label: FoundationDB
sidebar_position: 4
slug: /fdb_best_practices
---

# FoundationDB 最佳实践

fdb 支持横向扩容，一旦数据存储达到集群的最高负载，只需要在集群中添加新的机器即可。配置集群的详细教程可见官网 <https://apple.github.io/foundationdb/configuration.html> ，对于不同场景不同机器数量的性能测试可见 <https://apple.github.io/foundationdb/benchmarking.html>。

## 系统要求

- 以下 64 位操作系统之一
  - 受支持的 Linux 发行版
    - RHEL/CentOS 6.x and 7.x
    - Ubuntu 12.04 或更高版本
  - 未受支持的 Linux 发行版
    - 内核版本介于 2.6.33 和 3.0.x（含）或 3.7 或更高版本之间
    - 最好是.deb 或者.rpm
  - macOS 10.7 或更高版本
- 每个 fdbserver 需要至少 4GB 内存
- 存储
  - 存储数据小于内存时使用内存存储引擎
  - 存储数据大于内存时使用 SSD 存储引擎

## 如何配置 FoundationDB

### 在单机上配置 FoundationDB

**[Ubuntu](https://apple.github.io/foundationdb/getting-started-linux.html)**

```
//下载server和client deb包
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-clients_6.3.23-1_amd64.deb
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-server_6.3.23-1_amd64.deb
//安装
sudo dpkg -i foundationdb-clients_6.3.23-1_amd64.deb \
foundationdb-server_6.3.23-1_amd64.deb
```

**[RHEL/CentOS6/CentOS7](https://apple.github.io/foundationdb/getting-started-linux.html)**

```
//下载server和client rpm包
wget https://github.com/apple/foundationdb/releases/download/6.3.12/foundationdb-clients-6.3.23-1.el7.x86_64.rpm
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-server-6.3.23-1.el7.x86_64.rpm
//安装
sudo rpm -Uvh foundationdb-clients-6.3.23-1.el7.x86_64.rpm \
foundationdb-server-6.3.23-1.el7.x86_64.rpm
```

**[macOS](https://apple.github.io/foundationdb/getting-started-linux.html)**

详情请移步 FoundationDB 官网

### [在多台机器上配置 FoundationDB 集群](https://apple.github.io/foundationdb/administration.html#adding-machines-to-a-cluster)

> 部署单台机器的步骤与上述一致。

- 首先在每台机器上部署好单个 FoundationDB
- 选择一个节点将其 fdb.cluster 文件修改（路径默认`/etc/foundationdb/fdb.cluster`），此文件由一行字符串组成，格式为 description:ID@IP:PORT,IP:PORT,...，仅添加其他机器的 IP:PORT 即可。
- 将此修改完的 fdb.cluster 拷贝到其他节点
- 将机器重启（`sudo service foundationdb restart`）

## 冗余模式

FoundationDB 支持多种冗余模式。这些模式定义了存储要求、所需的集群大小和故障恢复能力，用户可根据不同的机器配置选择相对应的冗余模式。要更改冗余模式，请使用 的 configure 命令 fdbcli。示例如下：

```
user@host$ fdbcli
Using cluster file `/etc/foundationdb/fdb.cluster'.

The database is available.

Welcome to the fdbcli. For help, type `help'.
fdb> configure double
Configuration changed.
```

### `single` mode（1-2 台机器）

FoundationDB 不复制数据，只需要一台物理机器就可以进行处理。由于数据没有被复制，数据库没有容错能力。

建议在单个开发机器上进行测试时使用此模式。(单模式将用于由两台或两台以上计算机组成的集群，并将数据进行分区以提高性能，但集群不会容忍任何机器的丢失)

### `double` mode（3-4 台机器）

FoundationDB 将数据复制到两台机器上，因此需要两台或两台以上的机器进行处理。一台机器的丢失可以在不丢失数据的情况下存活，但如果最初只有两台机器，则数据库将不可用，直到恢复第二台机器、添加另一台机器或更改复制模式。

### `triple` mode（5+ 台机器）

FoundationDB 将数据复制到三台机器上，并且至少需要三台可用的机器才能进行处理。对于一个数据中心中有五台或更多机器的集群，推荐使用这种模式。

## 存储引擎

fdb 提供`ssd`和`memory`两种存储引擎，根据数据量大小来选择不同的存储引擎。我们在实际测试中发现两种存储引擎的性能相差不大，而`ssd`存储引擎支持较大的数据量，故推荐使用`ssd`存储引擎。

```
user@host$ fdbcli
Using cluster file `/etc/foundationdb/fdb.cluster'.

The database is available.

Welcome to the fdbcli. For help, type `help'.
fdb> configure ssd
Configuration changed.
```

### `ssd` 存储引擎（推荐）

数据以 B 树的格式存储在磁盘中，一般使用固态硬盘而非机械硬盘。当有合适的磁盘硬件时，这个引擎更加健壮，因为它可以存储大量数据。

关于性能，固态硬盘提供了很不错的随机读写性能，再加上热点数据的缓存，基本上于`memory`存储引擎相差无几，对于`JUICEFS`的元数据存储也是极力推荐使用`ssd`存储引擎。

需要注意的是，固态硬盘在损坏之后数据有可能不可恢复，所以需要注意硬盘的磨损程度以更换新的硬盘。

由于该存储引擎是针对于 SSD（固态硬盘），因此如果使用的机械硬盘，性能会受到很大影响。

### `memory` 存储引擎

数据存储在内存中，其通过顺序写日志的方式对数据进行持久化，数据库重启时通过回放日志的方式来进行数据恢复，此过程一般需要一些时间（几秒钟到几分钟）。

默认情况下，每个使用内存存储引擎的进程只能存储 1GB 的数据 (包括开销)。这个限制可以通过在`foundationdb.conf`中记录的`storage_memory`参数来更改。
