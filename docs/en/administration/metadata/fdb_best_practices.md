---
sidebar_label: FoundationDB
sidebar_position: 6
slug: /fdb_best_practices
---

# FoundationDB Best Practices

FoundationDB supports horizontal scaling. When data storage reaches the maximum capacity of a cluster, you can simply add new machines to the cluster. For detailed instructions on configuring a cluster, see the [FoundationDB configuration documentation](https://apple.github.io/foundationdb/configuration.html). For performance test results covering different use cases and cluster sizes, see the [FoundationDB benchmarking documentation](https://apple.github.io/foundationdb/benchmarking.html).

## System requirements

- One of the following 64-bit operating systems:
  - Supported Linux distributions:
    - RHEL/CentOS 6.x and 7.x
    - Ubuntu 12.04 or later
  - Unsupported Linux distributions:
    - Kernel versions from 2.6.33 through 3.0.x, inclusive, or 3.7 and later
    - Preferably distributions that use `.deb` or `.rpm` packages
  - macOS 10.7 or later
- At least 4 GB of memory for each `fdbserver` process
- Storage:
  - Use the memory storage engine when the data size is smaller than the available memory.
  - Use the SSD storage engine when the data size is larger than the available memory.

## Configure FoundationDB

### Configure FoundationDB on a single machine

**[Ubuntu](https://apple.github.io/foundationdb/getting-started-linux.html)**

```
// Download the server and client DEB packages
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-clients_6.3.23-1_amd64.deb
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-server_6.3.23-1_amd64.deb
// Install the packages
sudo dpkg -i foundationdb-clients_6.3.23-1_amd64.deb \
foundationdb-server_6.3.23-1_amd64.deb
```

**[RHEL/CentOS 6/CentOS 7](https://apple.github.io/foundationdb/getting-started-linux.html)**

```
// Download the server and client RPM packages
wget https://github.com/apple/foundationdb/releases/download/6.3.12/foundationdb-clients-6.3.23-1.el7.x86_64.rpm
wget https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-server-6.3.23-1.el7.x86_64.rpm
// Install the packages
sudo rpm -Uvh foundationdb-clients-6.3.23-1.el7.x86_64.rpm \
foundationdb-server-6.3.23-1.el7.x86_64.rpm
```

**[macOS](https://apple.github.io/foundationdb/getting-started-linux.html)**

For details, see the official FoundationDB documentation.

### [Configure a FoundationDB cluster on multiple machines](https://apple.github.io/foundationdb/administration.html#adding-machines-to-a-cluster)

> Follow the preceding instructions to deploy FoundationDB on each machine.

- First, deploy a standalone FoundationDB instance on every machine.
- On one node, edit its `fdb.cluster` file (located at `/etc/foundationdb/fdb.cluster` by default). This file contains a single line in the following format: `description:ID@IP:PORT,IP:PORT,...`. Add the `IP:PORT` entries for the other machines.
- Copy the modified `fdb.cluster` file to the other nodes.
- Restart FoundationDB on every machine (`sudo service foundationdb restart`).

## Redundancy modes

FoundationDB supports multiple redundancy modes. These modes determine storage requirements, the required cluster size, and fault tolerance. Choose the appropriate redundancy mode based on the number of machines in your cluster. To change the redundancy mode, use the `configure` command in `fdbcli`, as shown in the following example:

```
user@host$ fdbcli
Using cluster file `/etc/foundationdb/fdb.cluster'.

The database is available.

Welcome to the fdbcli. For help, type `help'.
fdb> configure double
Configuration changed.
```

### `single` mode (1–2 machines)

FoundationDB does not replicate data in this mode, so it requires only one physical machine. Because the data is not replicated, the database has no fault tolerance.

This mode is recommended for testing on a single development machine. You can also use `single` mode for a cluster of two or more machines. In that case, FoundationDB partitions the data to improve performance, but the cluster cannot tolerate the loss of any machine.

### `double` mode (3–4 machines)

FoundationDB stores two replicas of the data, so at least two machines are required. The cluster can survive the loss of one machine without losing data. However, if the cluster originally has only two machines, the database becomes unavailable until the second machine is restored, another machine is added, or the replication mode is changed.

### `triple` mode (5 or more machines)

FoundationDB stores three replicas of the data, and at least three machines must be available. This mode is recommended for clusters with five or more machines in a single data center.

## Storage engines

FoundationDB provides two storage engines: `ssd` and `memory`. Choose a storage engine based on the amount of data you need to store. In our tests, the two engines delivered similar performance. Because the `ssd` storage engine supports larger data sets, we recommend using it.

```
user@host$ fdbcli
Using cluster file `/etc/foundationdb/fdb.cluster'.

The database is available.

Welcome to the fdbcli. For help, type `help'.
fdb> configure ssd
Configuration changed.
```

### `ssd` storage engine (recommended)

The `ssd` storage engine stores data on disk in a B-tree and is generally used with solid-state drives rather than hard disk drives. With suitable disk hardware, this engine is more robust because it can store large amounts of data.

SSDs provide excellent random read and write performance. Together with caching for frequently accessed data, this makes the `ssd` storage engine nearly as fast as the `memory` storage engine. We strongly recommend the `ssd` storage engine for JuiceFS metadata.

Note that data might be unrecoverable after an SSD fails. Monitor drive wear and replace drives when necessary.

Because this storage engine is designed for SSDs, using hard disk drives significantly reduces performance.

### `memory` storage engine

The `memory` storage engine stores data in memory and persists it through a sequential write-ahead log. When the database restarts, it recovers data by replaying the log. This process usually takes anywhere from a few seconds to several minutes.

By default, each process using the memory storage engine can store up to 1 GB of data, including overhead. You can change this limit with the `storage_memory` parameter in `foundationdb.conf`.
