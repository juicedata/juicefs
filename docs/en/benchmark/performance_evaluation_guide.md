---
sidebar_label: Performance Evaluation Guide
sidebar_position: 2
slug: /performance_evaluation_guide
---
# JuiceFS Performance Evaluation Guide

Before starting performance testing, it is a good idea to write down a general description of that usage scenario, including:

1. What is the application for? For example, Apache Spark, PyTorch, or a program you developed yourself.
2. the resource allocation for running the application, including CPU, memory, network, and node size
3. the expected size of the data, including the number and volume of files
4. the file size and access patterns (large or small files, sequential or random reads and writes)
5. performance requirements, such as the amount of data to be written or read per second, the QPS of the access or the latency of the operation, etc.

The clearer and more detailed these above elements are, the easier it will be to prepare a suitable test plan and the performance indicators that need to be focused on to determine the application requirements for various aspects of the storage system, including JuiceFS metadata configuration, network bandwidth requirements, configuration parameters, etc. Of course, it is not easy to write out all of the above clearly at the beginning, and some of the content can be clarified gradually during the testing process,** but at the end of a complete test, the above usage scenario descriptions and the corresponding test methods, test data, and test results should be complete**.

Even if the above is not yet clear, it does not matter, JuiceFS built-in test tools can be a one-line command to get the core indicators of single-computer benchmark performance. This article will also introduce two JuiceFS built-in performance analysis tools, which can help you analyze the reasons behind JuiceFS performance in a simple and clear way when doing more complex tests.

## Performance Testing Quick Start

The following example describes the basic usage of the bench tool built-in to JuiceFS.

### Working Environment

- Host: Amazon EC2 c5.xlarge one
- OS: Ubuntu 20.04.1 LTS (Kernel 5.4.0-1029-aws)
- Metadata Engine: Redis 6.2.3, storage (dir) configured on system disk
- Object Storage: Amazon S3
- JuiceFS Version: 0.17-dev (2021-09-23 2ec2badf)

### JuiceFS Bench

The JuiceFS `bench` command can help you quickly complete a single machine performance test to determine if the environment configuration and performance are normal by the test results. Assuming you have mounted JuiceFS to `/mnt/jfs` on your server (if you need help with JuiceFS initialization and mounting, please refer to the [Quick Start Guide](../getting-started/for_local.md), execute the following command (the `-p` parameter is recommended to set the number of CPU cores on the server).

```bash
juicefs bench /mnt/jfs -p 4
```

The test results will show each performance indicator as green, yellow or red. If you have red indicators in your results, please check the relevant configuration first, and if you need help, you can describe your problem in detail at [GitHub Discussions](https://github.com/juicedata/juicefs/discussions).

![bench](../images/bench-guide-bench.png)

The JuiceFS `bench` benchmark performance test flows as follows (its logic is very simple, and those interested in the details can look directly at the [source code](https://github.com/juicedata/juicefs/blob/main/cmd/bench.go).

1. N concurrently write 1 large file of 1 GiB each with IO size of 1 MiB
2. N concurrently read 1 large file of 1 GiB each previously written, IO size 1 MiB
3. N concurrently write 100 small files of 128 KiB each, IO size is 128 KiB
4. N concurrently read 100 small files of 128 KiB each written previously, IO size 128 KiB
5. N concurrently stat 100 each of previously written 128 KiB small files
6. clean up the temporary directory for testing

The value of the concurrency number N is specified by the `-p` parameter in the `bench` command.

Here's a performance comparison using a few common storage types provided by AWS.

- EFS 1TiB capacity at 150MiB/s read and 50MiB/s write at $0.08/GB-month
- EBS st1 is a throughput-optimized HDD with a maximum throughput of 500MiB/s, a maximum IOPS (1MiB I/O) of 500, and a maximum capacity of 16TiB, priced at $0.045/GB-month
- EBS gp2 is a general-purpose SSD with a maximum throughput of 250MiB/s, maximum IOPS (16KiB I/O) of 16,000, and maximum capacity of 16TiB, priced at $0.10/GB-month

It is easy to see that in the above test, JuiceFS has significantly better sequential read and write capabilities than AWS EFS and more throughput than the commonly used EBS, but writing small files is not as fast because every file written needs to be persisted to S3 and there is typically a fixed overhead of 10-30ms for calling the object storage API.

:::note
The performance of Amazon EFS is linearly related to capacity ([refer to the official documentation](https://docs.aws.amazon.com/efs/latest/ug/performance.html#performancemodes)), which makes it unsuitable for use in high throughput scenarios with small data sizes.
:::

:::note
Prices refer to [AWS US East, Ohio Region](https://aws.amazon.com/ebs/pricing/?nc1=h_ls), prices vary slightly by Region.
:::

:::note
The above data is from [AWS official documentation](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ebs-volume-types.html), and the performance metrics are maximum values. The actual performance of EBS is related to volume capacity and mounted EC2 instance type, in general The larger the volume, the better the EBS performance will be with the higher specification EC2, but not exceeding the maximum value mentioned above.
:::

## Performance Observation and Analysis Tools

The next two performance observation and analysis tools are essential tools for testing, using, and tuning JuiceFS.

### JuiceFS Stats

JuiceFS `stats` is a tool for real-time statistics of JuiceFS performance metrics, similar to the `dstat` command on Linux systems, which displays changes in metrics for JuiceFS clients in real time (see [documentation](stats_watcher.md) for detailed). When the `juicefs bench` is running, create a new session and execute the following command.

```bash
juicefs stats /mnt/jfs --verbosity 1
```

The results are as follows, which can be more easily understood when viewed against the benchmarking process described above.

![stats](../images/bench-guide-stats.png)

The specific meaning of each of these indicators is as follows:

- usage
  - cpu: CPU consumed by the JuiceFS process
  - mem: the physical memory consumed by the JuiceFS process
  - buf: internal read/write buffer size of JuiceFS process, limited by mount option `--buffer-size`
  - cache: internal metric, can be simply ignored
- fuse
  - ops/lat: number of requests per second processed by the FUSE interface and their average latency (in milliseconds)
  - read/write: bandwidth value of the FUSE interface to handle read and write requests per second
- meta
  - ops/lat: number of requests per second processed by the metadata engine and their average latency (in milliseconds). Please note that some requests that can be processed directly in the cache are not included in the statistics to better reflect the time spent by the client interacting with the metadata engine.
  - txn/lat: the number of **write transactions** processed by the metadata engine per second and their average latency (in milliseconds). Read-only requests such as `getattr` are only counted as ops and not txn.
  - retry: the number of **write transactions** that the metadata engine retries per second
- blockcache
  - read/write: read/write traffic per second for the client's local data cache
- object
  - get/get_c/lat: bandwidth value of object store per second for processing **read requests**, number of requests and their average latency (in milliseconds)
  - put/put_c/lat: bandwidth value of object store for **write requests** per second, number of requests and their average latency (in milliseconds)
  - del_c/lat: the number of **delete requests** per second and the average latency (in milliseconds) of the object store

### JuiceFS Profile

JuiceFS `profile` is used to output all access logs of the JuiceFS client in real time, including information about each request. It can also be used to play back and count the JuiceFS access logs, allowing users to visualize the operation of JuiceFS (for detailed description and usage see [documentation](operations_profiling.md)). When executing `juicefs bench`, the following command is executed in another session. When the `juicefs bench` is running, create a new session and execute the following command.

```bash
cat /mnt/jfs/.accesslog > access.log
```

where `.accessslog` is a virtual file that normally does not produce any data and only has JuiceFS access log output when it is read (e.g. by executing `cat`). When you are finished use <kbd>Ctrl-C</kbd> to end the `cat` command and run.

```bash
juicefs profile access.log --interval 0
```

The `---interval` parameter sets the sampling interval for accessing the log, and when set to 0 is used to quickly replay a specified log file to generate statistics, as shown in the following figure.

![profile](../images/bench-guide-profile.png)

From the description of the previous benchmarking process, a total of (1 + 100) * 4 = 404 files were created during this test, and each file went through the process of "Create → Write → Close → Open → Read → Close → Delete", so there are a total of:

- 404 create, open and unlink requests
- 808 flush requests: flush is automatically invoked whenever a file is closed
- 33168 write/read requests: each large file writes 1024 1 MiB IOs, while the default maximum value of requests at the FUSE level is 128 KiB, which means that each application IO is split into 8 FUSE requests, so there are (1024 * 8 + 100) * 4 = 33168 requests. The read IO is similar and the count is the same.

All these values correspond exactly to the results of `profile`. This is because JuiceFS `write` writes to the memory buffer first by default and then calls flush to upload data to the object store when the file is closed, as expected.

## Other Test Tool Configuration Examples

### Fio Standalone Performance Test

Fio is a common performance testing tool that can be used to do more complex performance tests after completing the JuiceFS bench.

#### Working Environment

Consistent with the JuiceFS Bench test environment described above.

#### Testing tasks

Perform the following 4 Fio tasks for sequential write, sequential read, random write, and random read tests.

Sequential write

```shell
fio --name=jfs-test --directory=/mnt/jfs --ioengine=libaio --rw=write --bs=1m --size=1g --numjobs=4 --direct=1 --group_reporting
```

Sequential read

```bash
fio --name=jfs-test --directory=/mnt/jfs --ioengine=libaio --rw=read --bs=1m --size=1g --numjobs=4 --direct=1 --group_reporting
```

Random write

```shell
fio --name=jfs-test --directory=/mnt/jfs --ioengine=libaio --rw=randwrite --bs=1m --size=1g --numjobs=4 --direct=1 --group_reporting
```

Random read

```shell
fio --name=jfs-test --directory=/mnt/jfs --ioengine=libaio --rw=randread --bs=1m --size=1g --numjobs=4 --direct=1 --group_reporting
```

Parameters description:

- `--name`: user-specified test name, which affects the test file name
- `--directory`: test directory
- `--ioengine`: the way to send IO when testing; usually `libaio` is used
- `--rw`: commonly used are read, write, randread, randwrite, which stand for sequential read/write and random read/write respectively
- `--bs`: the size of each IO
- `--size`: the total size of IO per thread; usually equal to the size of the test file
- `--numjobs`: number of concurrent test threads; by default each thread runs a separate test file
- `--direct`: add the `O_DIRECT` flag bit when opening the file, without using system buffering, which can make the test results more stable and accurate

The results are as follows:

```bash
# Sequential
WRITE: bw=703MiB/s (737MB/s), 703MiB/s-703MiB/s (737MB/s-737MB/s), io=4096MiB (4295MB), run=5825-5825msec
READ: bw=817MiB/s (856MB/s), 817MiB/s-817MiB/s (856MB/s-856MB/s), io=4096MiB (4295MB), run=5015-5015msec

# Random
WRITE: bw=285MiB/s (298MB/s), 285MiB/s-285MiB/s (298MB/s-298MB/s), io=4096MiB (4295MB), run=14395-14395msec
READ: bw=93.6MiB/s (98.1MB/s), 93.6MiB/s-93.6MiB/s (98.1MB/s-98.1MB/s), io=4096MiB (4295MB), run=43773-43773msec
```

### Vdbench Multi-computer Performance Test

Vdbench is a commonly used file system evaluation tool, and it supports concurrent multi-machine testing very well.

#### Working Environment

Similar to the JuiceFS Bench test environment, except that two more hosts with the same specifications were turned on, for a total of three.

#### Preparation

vdbench needs to be installed in the same path on each node: vdbench

1. Download version 50406 from the [Official Website](https://www.oracle.com/downloads/server-storage/vdbench-downloads.html)
2. Install Java: `apt-get install openjdk-8-jre`
3. Verify that vdbench is installed successfully: `./vdbench -t`

Then, assuming the names of the three nodes are node0, node1 and node2, you need to create a configuration file on node0 as follows (to test reading and writing a large number of small files):

```bash
$ cat jfs-test
hd=default,vdbench=/root/vdbench50406,user=root
hd=h0,system=node0
hd=h1,system=node1
hd=h2,system=node2

fsd=fsd1,anchor=/mnt/jfs/vdbench,depth=1,width=100,files=3000,size=128k,shared=yes

fwd=default,fsd=fsd1,operation=read,xfersize=128k,fileio=random,fileselect=random,threads=4
fwd=fwd1,host=h0
fwd=fwd2,host=h1
fwd=fwd3,host=h2

rd=rd1,fwd=fwd*,fwdrate=max,format=yes,elapsed=300,interval=1
```

Parameters description:

- `vdbench=/root/vdbench50406`: specifies the path where the vdbench tool is installed
- `anchor=/mnt/jfs/vdbench`: specifies the path to run test tasks on each node
- `depth=1,width=100,files=3000,size=128k`: defines the test task file tree structure, i.e. 100 more directories are created under the test directory, each directory contains 3000 files of 128 KiB size, 300,000 files in total
- `operation=read,xfersize=128k,fileio=random,fileselect=random`: defines the actual test task, i.e., randomly selecting files to send 128 KiB size read requests

The results are as follows:

```
FILE_CREATES        Files created:                              300,000        498/sec
READ_OPENS          Files opened for read activity:             188,317        627/sec
```

The overall system speed for creating 128 KiB files is 498 files per second and reading files is 627 files per second.

#### Other Reference Examples

For reference, here are some profiles available for simple local evaluation of file system performance; the exact test set size and number of concurrency can be adjusted to suit the actual situation.

##### Sequential reading and writing of large files

The file size is 1GiB, where `fwd1` is a sequential write large file and `fwd2` is a sequential read large file.

```bash
$ cat local-big
fsd=fsd1,anchor=/mnt/jfs/local-big,depth=1,width=1,files=4,size=1g,openflags=o_direct

fwd=fwd1,fsd=fsd1,operation=write,xfersize=1m,fileio=sequential,fileselect=sequential,threads=4
fwd=fwd2,fsd=fsd1,operation=read,xfersize=1m,fileio=sequential,fileselect=sequential,threads=4

rd=rd1,fwd=fwd1,fwdrate=max,format=restart,elapsed=120,interval=1
rd=rd2,fwd=fwd2,fwdrate=max,format=restart,elapsed=120,interval=1
```

##### Random reading and writing of small files

The file size is 128KiB, where `fwd1` is a random write small file, `fwd2` is a random read small file, and `fwd3` is a mixed read/write small file (read/write ratio = 7:3).

```bash
$ cat local-small
fsd=fsd1,anchor=/mnt/jfs/local-small,depth=1,width=20,files=2000,size=128k,openflags=o_direct

fwd=fwd1,fsd=fsd1,operation=write,xfersize=128k,fileio=random,fileselect=random,threads=4
fwd=fwd2,fsd=fsd1,operation=read,xfersize=128k,fileio=random,fileselect=random,threads=4
fwd=fwd3,fsd=fsd1,rdpct=70,xfersize=128k,fileio=random,fileselect=random,threads=4

rd=rd1,fwd=fwd1,fwdrate=max,format=restart,elapsed=120,interval=1
rd=rd2,fwd=fwd2,fwdrate=max,format=restart,elapsed=120,interval=1
rd=rd3,fwd=fwd3,fwdrate=max,format=restart,elapsed=120,interval=1
```
