---
sidebar_label: 快速上手（分布式）
sidebar_position: 3
---

# 分布式部署快速上手指南

上一篇文档《[JuiceFS 本地快速体验](for_local.md)》通过采用「对象存储」和「SQLite」数据库的组合，实现了一个可以在任意主机上挂载的文件系统。得益于对象存储是可以被网络上任何有权限的计算机访问的特点，我们只需要把 SQLite 数据库文件复制到任何想要访问该存储的计算机，就可以实现在不同计算机上访问同一个 JuiceFS 文件系统。

很显然，想要依靠在计算机之间复制 SQLite 数据库的方式进行文件系统共享，虽然可行，但文件的实时性是得不到保证的。受限于 SQLite 这种单文件数据库无法被多个计算机同时读写访问的情况，为了能够让一个文件系统可以在分布式的计算环境中被多个计算机同时挂载读写，我们需要采用支持通过网络访问的基于网络的数据库，比如 Redis、PostgreSQL 或 MySQL 等。

本文以上一篇文档为基础，进一步将数据库从单用户的「SQLite」替换成多用户的「云数据库」，从而实现可以在网络上任何一台计算机上进行挂载读写的分布式文件系统。

## 基于网络的数据库

这里所谓的“基于网络的数据库”是指允许多个用户通过网络同时访问的数据库，从这个角度出发，可以简单的把数据库分成：

1. **单机数据库**：数据库是单个文件，通常只能单机访问，如 SQLite，Microsoft Access 等；
2. **基于网络的数据库**：数据库通常是复杂的多文件结构，提供基于网络的访问接口，支持多用户同时访问，如 Redis、PostgreSQL 等。

JuiceFS 目前支持的基于网络的数据库有：

- **键值数据库**： Redis、TiKV
- **关系型数据库**：PostgreSQL、MySQL、MariaDB

不同的数据库性能和稳定性表现也各不相同，比如 Redis 是内存型键值数据库，性能极为出色，但可靠性相对较弱。PostgreSQL 是关系型数据库，相比之下性能没有内存型强悍，但它的可靠性要更强。

有关数据库选择方面的内容，我们会专门编写文档进行介绍。

## 云数据库

云计算平台通常都有种类丰富的云数据库提供，比如 Amazon RDS 提供各类关系型数据库的版本，Amazon ElastiCache 提供兼容 Redis 的内存型数据库产品。经过简单的初始化设置就可以创建初多副本、高可用的数据库集群。

当然，如果愿意，你可以自己在服务器上搭建数据库。

简单起见，这里以阿里云数据库 Redis 版为例介绍。对于基于网络的数据库来说，最基本的是以下 2 项信息：

1. **数据库地址**：数据库的访问地址，云平台可能会针对内外网提供不同的链接；
2. **用户和密码**：用于访问数据库时的身份验证信息。

## 上手实践

### 1. 安装客户端

在所有需要挂载文件系统的计算机上安装 JuiceFS 客户端，详情参照[安装 & 升级](installation.md)。

### 2. 准备对象存储

以下是以阿里云 OSS 为例的伪样本，你可以改用其他对象存储，详情参考 [JuiceFS 支持的存储](../reference/how_to_setup_object_storage.md#支持的存储服务)。

- **Bucket Endpoint**：`https://myjfs.oss-cn-shanghai.aliyuncs.com`
- **Access Key ID**: `ABCDEFGHIJKLMNopqXYZ`
- **Access Key Secret**: `ZYXwvutsrqpoNMLkJiHgfeDCBA`

### 3. 准备数据库

以下是以阿里云数据库 Redis 版为例的伪样本，你可以改用其他类型的数据库，详情参考 [JuiceFS 支持的数据库](../reference/how_to_setup_metadata_engine.md)。

- **数据库地址**：`myjfs-sh-abc.redis.rds.aliyuncs.com:6379`
- **数据库用户名**：`tom`
- **数据库密码**：`mypassword`

在 JuiceFS 中使用 Redis 数据库的格式如下：

```
redis://<username>:<password>@<Database-IP-or-URL>:6379/1
```

:::tip 提示
Redis 6.0 之前的版本没有用户名，请省略 URL 中的 username 部分，例如 `redis://:mypassword@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1`（请注意密码前面的冒号是分隔符，需要保留）。
:::

### 4. 创建文件系统

以下命令使用「对象存储」和「Redis」数据库的组合创建了一个支持跨网络、多机同时挂载、共享读写的文件系统。

```shell
juicefs format \
    --storage oss \
    --bucket https://myjfs.oss-cn-shanghai.aliyuncs.com \
    --access-key ABCDEFGHIJKLMNopqXYZ \
    --secret-key ZYXwvutsrqpoNMLkJiHgfeDCBA \
    redis://tom:mypassword@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1 \
    myjfs
```

文件系统创建完成后，终端将返回类似下面的内容：

```shell
2021/12/16 16:37:14.264445 juicefs[22290] <INFO>: Meta address: redis://@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1
2021/12/16 16:37:14.277632 juicefs[22290] <WARNING>: maxmemory_policy is "volatile-lru", please set it to 'noeviction'.
2021/12/16 16:37:14.281432 juicefs[22290] <INFO>: Ping redis: 3.609453ms
2021/12/16 16:37:14.527879 juicefs[22290] <INFO>: Data uses oss://myjfs/myjfs/
2021/12/16 16:37:14.593450 juicefs[22290] <INFO>: Volume is formatted as {Name:myjfs UUID:4ad0bb86-6ef5-4861-9ce2-a16ac5dea81b Storage:oss Bucket:https://myjfs AccessKey:ABCDEFGHIJKLMNopqXYZ SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

:::info 说明
文件系统一经创建，相关的信息包括名称、对象存储、访问密钥等信息会完整的记录到数据库中。在当前的示例中，文件系统的信息被记录在 Redis 数据库中，因此在任何一台计算机上，只要拥有数据库地址、用户名和密码信息，就可以挂载读写该文件系统。
:::

### 5. 挂载文件系统

由于这个文件系统的「数据」和「元数据」都存储在基于网络的云服务中，因此在任何安装了 JuiceFS 客户端的计算机上都可以同时挂载该文件系统进行共享读写。例如：

```shell
juicefs mount redis://tom:mypassword@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1 mnt
```

#### 数据强一致性保证

对于多客户端同时挂载读写同一个文件系统的情况，JuiceFS 提供数据强一致性保证，即任何一个客户端上完成的写操作，可以保证在其他客户端上及时可见。请查阅《[读写请求处理流程](../development/io_processing.md)》了解详情。

#### 调大缓存提升性能

由于「对象存储」是基于网络的存储服务，不可避免会产生访问延时。为了解决这个问题，JuiceFS 提供并默认启用了缓存机制，即划拨一部分本地存储作为数据与对象存储之间的一个缓冲层，文件的读写会先写入本地缓存，再由客户端在后台异步传送到对象存储，详情查阅《[缓存](../administration/cache_management.md)》。

缓存机制让 JuiceFS 可以高效处理海量数据的读写任务，默认情况下，JuiceFS 会在 `$HOME/.juicefs/cache` 或 `/var/jfsCache` 目录设置 `1024MiB` 的缓存。在速度更快的 SSD 上设置更大的缓存空间可以有效提升 JucieFS 的读写性能。

你可以使用 `--cache-dir` 调整缓存目录的位置，使用 `--cache-size` 调整缓存空间的大小，例如：

```shell
juicefs mount --background \
              --cache-dir /mycache \
              --cache-size 512000 \
redis://tom:mypassword@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1 mnt
```

上述命令将缓存目录设置在了 `/mycache` 目录，并指定缓存空间为 `500GiB`。

#### 开机自动挂载

以 Linux 系统为例，假设客户端位于 `/usr/local/bin` 目录。将 juicefs 客户端重命名为 mount.juicefs 并复制到 /sbin/ 目录，：

```shell
sudo cp /usr/local/bin/juicefs /sbin/mount.juicefs
```

编辑 `/etc/fstab` 配置文件，遵照 fstab 的规则添加一条新记录：

```
redis://tom:mypassword@myjfs-sh-abc.redis.rds.aliyuncs.com:6379/1    /mnt/myjfs    juicefs    _netdev,max-uploads=50,writeback,cache-size=512000     0  0
```

:::note 注意
默认情况下，CentOS 6 在系统启动时不会挂载网络文件系统，你需要执行命令开启网络文件系统的自动挂载支持：`sudo chkconfig --add netfs`
:::