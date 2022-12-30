---
title: 在 AWS 上使用 JuiceFS
sidebar_position: 6
slug: /clouds/aws
---

亚马逊 AWS 是全球领先的云计算平台，提供几乎所有类型的云计算服务。得益于 AWS 丰富的产品线，用户可以非常灵活的搭配选择 JuiceFS 组成部分。

## 准备

通过阅读文档可以了解到 JuiceFS 由以下三个部分组成：

1. 运行在服务器上 **JuiceFS 客户端**
2. 用来存储数据的**对象存储**
3. 用来存储元数据的**数据库**

### 1. 服务器

Amazon EC2 云服务器是 AWS 平台上最基础，也是应用最广泛的云服务之一。它提供 400 多种实例规格，在全球有 25 个数据中心 81 个可用区，用户可以根据实际需求灵活的选择和调整 EC2 实例的配置。

对于新用户来说，你并不需要过多考虑 JuiceFS 的配置要求，因为即便是配置最低的 EC2 实例，也能轻松创建和挂载使用 JuiceFS 存储。通常，你只需要考虑业务系统的硬件需求即可。

JuiceFS 客户端默认会占用 1GB 的磁盘作为缓存，在处理大量文件时，客户端会将数据先缓存在磁盘上，然后再异步上传到对象存储，选择 IO 更高的磁盘，预留并设置更大的缓存，可以让 JuiceFS 拥有更好的性能表现。

### 2. 对象存储

Amazon S3 是公有云对象存储服务的事实标准，其他主流云平台所提供的对象存储服务通常都兼容 S3 API，这使得面向 S3 开发的程序可以自由切换其他平台的对象存储服务。

JuiceFS 完全支持 Amazon S3 以及所有兼容 S3 API 对象存储服务，你可以查看文档了解 [JuiceFS 支持的所有存储类型](../guide/how_to_set_up_object_storage.md)。

Amazon S3 提供一系列适合不同使用案例的存储类，主要有：

- **Amazon S3 STANDARD**：适用于频繁访问数据的通用型存储
- **Amazon S3 STANDARD_IA**：适用于长期需要但访问频率不太高的数据
- **S3 Glacier**：适用于长期存档的数据

通常应该使用标准类型的 S3 用于 JuiceFS，因为除标准类型即 Amazon S3 STANDARD 之外，其他的类型虽然价格更低，但在检索（取回）数据时都会产生额外的费用。

另外，访问对象存储服务需要通过 `access key` 和 `secret key` 验证用户身份，你可以参照文档[《使用用户策略控制对存储桶的访问》](https://docs.aws.amazon.com/zh_cn/AmazonS3/latest/userguide/walkthrough1.html)进行创建。当通过 EC2 云服务器访问 S3 时，还可以为 EC2 分配 [IAM 角色](https://docs.aws.amazon.com/zh_cn/IAM/latest/UserGuide/id_roles.html)，实现在 EC2 上免密钥调用 S3 API。

### 3. 数据库

数据和元数据能够被多主机访问是分布式文件系统的关键，为了让 JuiceFS 产生的元数据信息能够像 S3 那样通过互联网请求访问，存储元数据的数据库也应该选择面向网络的数据库。

Amazon RDS 和 ElastiCache 是 AWS 提供的两种云数据库服务，都能直接用于 JuiceFS 的元数据存储。Amazon RDS 是关系型数据库，支持 MySQL、MariaDB、PostgreSQL 等多种引擎。ElastiCache 是基于内存的缓存集群服务，用于 JuiceFS 时应选择 Redis 引擎。

此外，你还可以在 EC2 云服务器上自行搭建数据库供 JuiceFS 存储元数据使用。

### 4. 注意事项

- 你无需为使用 JuiceFS 重新创建各种云服务资源，可以直接在现有的 EC2 云服务器上安装 JuiceFS 客户端立即开始使用。JuiceFS 没有业务入侵性，不会影响现有系统的正常运行。
- 在选择云服务时，建议将所有的云服务选择在相同的**区域**，这样就相当于所有服务都在同一个内网，互访的时延最低，速度最快。并且，根据 AWS 的计费规则，相同区域的基础云服务之间互传数据是免费的。换言之，当你选择了不同区域的云服务，例如，EC2 选择在 `ap-east-1`、ElastiCache 选择在 `ap-southeast-1`、S3 选择在 `us-east-2`，这种情况下每个云服务之间的互访都将产生流量费用。
- JuiceFS 不要求使用相同云平台的对象存储和数据库，你可以根据需要灵活搭配不同平台的云服务。比如，你可以使用 EC2 运行 JuiceFS 客户端，搭配阿里云的 Redis 数据库和 Backbalze B2 对象存储。当然，同一平台、相同区域的云服务组成的 JuiceFS 存储的性能会更出色。

## 部署和使用

接下来，我们以相同区域的 EC2 云服务器、S3 对象存储和 Redis 引擎的 ElastiCache 集群为例，简要的介绍如何安装和使用 JuiceFS。

### 1. 安装客户端

这里我们使用的是 x64 位架构的 Linux 系统，依次执行以下命令，会下载最新版 JuiceFS 客户端。

```shell
JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
```

```shell
wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
```

下载完成以后，解压程序到 `juice` 文件夹：

```shell
mkdir juice && tar -zxvf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" -C juice
```

将 JuiceFS 客户端安装系统的 $PATH 路径，例如：`/usr/local/bin` ：

```shell
sudo install juice/juicefs /usr/local/bin
```

执行命令，看到返回 `juicefs` 的命令帮助信息，代表客户端安装成功。

```shell
$ juicefs
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.17.0 (2021-09-24T04:17:26Z e115dc4)

COMMANDS:
   format   format a volume
   mount    mount a volume
   umount   unmount a volume
   gateway  S3-compatible gateway
   sync     sync between two storage
   rmr      remove directories recursively
   info     show internal information for paths or inodes
   bench    run benchmark to read/write/stat big/small files
   gc       collect any leaked objects
   fsck     Check consistency of file system
   profile  analyze access log
   stats    show runtime statistics
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump metadata into a JSON file
   load     load metadata from a previously dumped JSON file
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              disable pprof (:6060) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   Apache License 2.0
```

> **提示**：如果执行 `juicefs` 命令，终端返回 `command not found`，可能是因为 `/usr/local/bin` 目录不在系统的 `PATH` 可执行路径中。你可以通过 `echo $PATH` 命令查看系统已设置的可执行路径，并将客户端重新安装到正确的位置。也可以将 `/usr/local/bin` 添加到 `PATH` 中。

JuiceFS 具有良好的跨平台兼容性，同时支持在 Linux、Windows 和 macOS 上使用，如果你需要了解其他系统上的安装方法，请查阅[官方文档](../getting-started/installation.md)。

### 3. 创建文件系统

JuiceFS 客户端的 `format` 子命令用来创建（格式化）文件系统，这里我们使用 S3 作为数据存储，使用 ElastiCache 作为元数据存储，在 EC2 上安装客户端并创建 JuiceFS 文件系统，命令格式如下：

```shell
$ juicefs format \
    --storage s3 \
    --bucket https://<bucket>.s3.<region>.amazonaws.com \
    --access-key <access-key-id> \
    --secret-key <access-key-secret> \
    redis://[<redis-username>]:<redis-password>@<redis-url>:6379/1 \
    mystor
```

**选项说明：**

- `--storage`：指定对象存储类型，这里我们使用 S3。如需使用其他对象存储，请参考[《JuiceFS 支持的对象存储和设置指南》](../guide/how_to_set_up_object_storage.md)。
- `--bucket`：对象存储的 Bucket 域名。
- `--access-key` 和 `--secret-key`：访问 S3 API 的秘钥对。

> Redis 6.0 及以上版本，身份认证需要用户名和密码两个参数，地址格式为 `redis://username:password@redis-server-url:6379/1`。Reids 4.0 和 5.0，认证身份只需要密码，在设置 Redis 服务器地址时只需留空用户名，例如：`redis://:password@redis-server-url:6379/1`

使用 IAM 角色绑定 EC2 时，只需指定 `--storage` 和  `--bucket` 两个选项，无需提供 API 访问秘钥。同时，也可以给 IAM 角色分配 ElastiCache 访问权限，然后就可以不用提供 Redis 的身份认证信息，只需输入 Redis 的 URL 即可，命令可以改写成：

```shell
$ juicefs format \
    --storage s3 \
    --bucket https://herald-demo.s3.<region>.amazonaws.com \
    redis://herald-demo.abcdefg.0001.apse1.cache.amazonaws.com:6379/1 \
    mystor
```

看到类似下面的输出，代表文件系统创建成功了。

```shell
2021/10/14 08:38:32.211044 juicefs[10391] <INFO>: Meta address: redis://herald-demo.abcdefg.0001.apse1.cache.amazonaws.com:6379/1
2021/10/14 08:38:32.216566 juicefs[10391] <INFO>: Ping redis: 383.789µs
2021/10/14 08:38:32.216915 juicefs[10391] <INFO>: Data use s3://herald-demo/mystor/
2021/10/14 08:38:32.412112 juicefs[10391] <INFO>: Volume is formatted as {Name:mystor UUID:21a2cafd-f5d8-4a76-ae4d-482c8e2d408d Storage:s3 Bucket:https://herald-demo.s3.ap-southeast-1.amazonaws.com AccessKey: SecretKey: BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

### 4. 挂载文件系统

创建文件系统的过程会将对象存储包括 API 密钥等信息存入数据库，挂载时无需再输入对象存储的 Bucket 和秘钥等信息。

使用 JuiceFS 客户端的 `mount` 子命令，将文件系统挂载到 `/mnt/jfs` 目录：

```shell
sudo juicefs mount -d redis://[<redis-username>]:<redis-password>@<redis-url>:6379/1  /mnt/jfs
```

> **注意**：挂载文件系统时，只需填写数据库地址，不需要文件系统名称。默认的缓存路径为 `/var/jfsCache`，请确保当前用户有足够的读写权限。

你可以通过调整[挂载参数](../reference/command_reference.md#mount)，对 JuiceFS 进行优化，比如可以通过 `--cache-size` 将缓存修改为 20GB：

```shell
sudo juicefs mount --cache-size 20480 -d redis://herald-demo.abcdefg.0001.apse1.cache.amazonaws.com:6379/1  /mnt/jfs
```

看到类似下面的输出，代表文件系统挂载成功。

```shell
2021/10/14 08:47:49.623814 juicefs[10601] <INFO>: Meta address: redis://herald-demo.abcdefg.0001.apse1.cache.amazonaws.com:6379/1
2021/10/14 08:47:49.628157 juicefs[10601] <INFO>: Ping redis: 426.127µs
2021/10/14 08:47:49.628941 juicefs[10601] <INFO>: Data use s3://herald-demo/mystor/
2021/10/14 08:47:49.629198 juicefs[10601] <INFO>: Disk cache (/var/jfsCache/21a2cafd-f5d8-4a76-ae4d-482c8e2d408d/): capacity (20480 MB), free ratio (10%), max pending pages (15)
2021/10/14 08:47:50.132003 juicefs[10601] <INFO>: OK, mystor is ready at /mnt/jfs
```

使用 `df` 命令，可以看到文件系统的挂载情况：

```shell
$ df -Th
文件系统           类型          容量   已用  可用   已用% 挂载点
JuiceFS:mystor   fuse.juicefs  1.0P   64K  1.0P    1% /mnt/jfs
```

挂载之后就可以像本地硬盘那样使用了，存入 `/mnt/jfs` 目录的数据会由 JuiceFS 客户端协调管理，最终存储在 S3 对象存储。

> **多主机共享**：JuiceFS 支持被多主机同时挂载使用，你可以在任何平台的任何云服务器上安装 JuiceFS 客户端，使用 `redis://:<your-redis-password>@herald-sh-abc.redis.rds.aliyuncs.com:6379/1` 数据库地址挂载文件系统即可共享读写，但需要确保挂载文件系统的主机能够正常访问到该数据库和搭配使用的 S3。

### 5. 卸载 JuiceFS 存储

使用 JuiceFS 客户端提供的 `umount` 命令可卸载文件系统，比如：

```shell
sudo juicefs umount /mnt/jfs
```

> **注意**：强制卸载使用中的文件系统可能导致数据损坏或丢失，请务必谨慎操作。

### 6. 开机自动挂载

如果你不想每次重启系统都要重新手动挂载 JuiceFS 存储，可以设置自动挂载。

首先，需要将  `juicefs` 客户端重命名为 `mount.juicefs` 并复制到 `/sbin/` 目录：

```shell
sudo cp juice/juicefs /sbin/mount.juicefs
```

编辑 `/etc/fstab` 配置文件，新增一条记录：

```shell
redis://[<redis-username>]:<redis-password>@<redis-url>:6379/1    /mnt/jfs       juicefs     _netdev,cache-size=20480     0  0
```

挂载选项中 `cache-size=20480` 代表分配 20GB 本地磁盘空间作为 JuiceFS 的缓存使用，请根据你实际的 EBS 磁盘容量去决定分配的缓存大小。

你可以根据需要调整上述配置中的 FUSE 挂载选项，更多内容请[查阅文档](../reference/fuse_mount_options.md)。

> **注意**：请将上述配置文件中的 Redis 地址、挂载点以及挂载选项，替换成你实际的信息。
