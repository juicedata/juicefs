---
title: 在 DigitalOcean 使用 JuiceFS
sidebar_position: 6
slug: /clouds/digitalocean
---

JuiceFS 是面向云设计的，使用云平台开箱即用的存储和数据库服务，最快几分钟就能完成配置投入使用，本文以 DigitalOcean 平台为例，介绍如何在云计算平台上快速简单的安装和使用 JuiceFS。

## 准备工作

JuiceFS 由存储和数据库组合驱动，因此你需要准备的东西应该包括：

### 1. 云服务器

DigitalOcean 上的云服务器被称为 Droplet。你不需要为使用 JuiceFS 而单独购买新的 Droplet，哪个云服务器上需要使用 JuiceFS 存储，就在它上面安装 JuiceFS 客户端即可。

#### 硬件配置

JuiceFS 对硬件配置没有特殊的要求，任何规格的 Droplet 都能稳定的使用。但建议选择性能更好的 SSD 并预留至少 1GB 的容量提供给 JuiceFS 作为本地缓存使用。

#### 操作系统

JuiceFS 支持 Linux、BSD、macOS 和 Windows，在本文中，我们会以 Ubuntu Server 20.04 为例进行介绍。

### 2. 对象存储

JuiceFS 使用对象存储来存储所有的数据，在 DigitalOcean 上使用 Spaces 是最简便的方案。Spaces 是一个 S3 兼容的对象存储服务，开箱即用。在创建时建议选择与 Droplet 相同的区域，这样可以获得最佳的访问速度，同时也能避免额外的流量开销。

当然，你也可以使用其他平台的对象存储服务，或是在 Droplet 上使用 Ceph 或 MinIO 手动搭建。总之，你可以自由选择要使用的对象存储，只要确保 JuiceFS 客户端能够访问到对象存储的 API 就可以。

这里，我们创建了一个名为 `juicefs` 的 Spaces 存储桶，区域为新加坡 `sgp1`，它的访问地址为：

- `https://juicefs.sgp1.digitaloceanspaces.com`

另外，还需要在 API 菜单创建 `Spaces access keys`，JuiceFS 需要用它访问 Spaces 的 API。

### 3. 数据库

与一般的文件系统不同，JuiceFS 将数据所对应的所有元数据都存储在独立的数据库，存储的数据规模越大性能越出色。目前，JuiceFS 支持 Redis、TiKV、MySQL/MariaDB、PostgreSQL、SQLite 等常见数据库，同时也在持续开发对其他数据库的支持。如果你需要的数据库暂未支持，请提交 [Issue](https://github.com/juicedata/juicefs/issues) 反馈。

在性能、规模和可靠性等方面，每种数据库都有各自的优缺点，你应该根据实际的场景需要进行选择。

在数据库的选择方面请不要有顾虑，JuiceFS 客户端提供了元数据迁移功能，你可以将元数据从一种数据库中轻松的导出并迁移到其他的数据库中。

本文我们使用 DigitalOcean 的 Redis 6 数据库托管服务，区域选择 `新加坡`，选择与已存在的 Droplet 相同的 VPC 私有网络。创建 Redis 大概需要 5 分钟左右的时间，我们跟随设置向导对数据库进行初始化设置。

![DigitalOcean-Redis-guide](../images/digitalocean-redis-guide.png)

默认情况下 Redis 允许所有入站连接，出于安全考虑，应该在设置向导的安全设置环节，在 `Add trusted sources` 中选中有权访问 Redis 的 Droplet，即仅允许选中的主机访问 Redis。

在数据回收策略的设置环节，建议选择 `noeviction`，即当内存耗尽时，仅报告错误，不回收任何数据。

> **注意**：为了确保元数据的安全和完整，回收策略请不要选择 `allkeys-lru` 和 `allkey-random`。

Redis 的访问地址可以从控制台的 `Connection Details` 中找到，如果所有计算资源都在 DigitalOcean，则建议优先使用 VPC 私有网络进行连接，这样能最大程度的提升安全性。

![DigitalOcean-Redis-url](../images/digitalocean-redis-url.png)

## 安装和使用

### 1. 安装 JuiceFS 客户端

我们当前使用的是 Ubuntu Server 20.04，执行以下命令即可安装最新版本客户端。

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

执行命令，看到返回 `juicefs` 的命令帮助信息，代表客户端安装成功。

```shell
$ juicefs

NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.16.2 (2021-08-25T04:01:15Z 29d6fee)

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
   stats    show runtime stats
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

另外，你也可以访问 [JuiceFS GitHub Releases](https://github.com/juicedata/juicefs/releases) 页面选择其他版本进行手动安装。

### 2. 创建文件系统

创建文件系统使用 `format` 子命令，格式为：

```shell
juicefs format [command options] META-URL NAME
```

以下命令创建了一个名为 `mystor` 的文件系统：

```shell
$ juicefs format \
    --storage space \
    --bucket https://juicefs.sgp1.digitaloceanspaces.com \
    --access-key <your-access-key-id> \
    --secret-key <your-access-key-secret> \
    rediss://default:your-password@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1 \
    mystor
```

**参数说明：**

- `--storage`：指定数据存储引擎，这里使用的是 `space`，点此查看所有[支持的存储](../reference/how_to_set_up_object_storage.md)。
- `--bucket`：指定存储桶访问地址。
- `--access-key` 和 `--secret-key`：指定访问对象存储 API 的秘钥。
- DigitalOcean 托管的 Redis 需要使用 TLS/SSL 加密访问，因此需要使用 `rediss://` 协议头，链接最后添加的 `/1` 代表使用 Redis 的 1 号数据库。

看到类似下面的输出，代表文件系统创建成功。

```shell
2021/08/23 16:36:28.450686 juicefs[2869028] <INFO>: Meta address: rediss://default@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1
2021/08/23 16:36:28.481251 juicefs[2869028] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/08/23 16:36:28.481763 juicefs[2869028] <INFO>: Ping redis: 331.706µs
2021/08/23 16:36:28.482266 juicefs[2869028] <INFO>: Data uses space://juicefs/mystor/
2021/08/23 16:36:28.534677 juicefs[2869028] <INFO>: Volume is formatted as {Name:mystor UUID:6b0452fc-0502-404c-b163-c9ab577ec766 Storage:space Bucket:https://juicefs.sgp1.digitaloceanspaces.com AccessKey:7G7WQBY2QUCBQC5H2DGK SecretKey:removed BlockSize:4096 Compression:none Shards:0 Partitions:0 Capacity:0 Inodes:0 EncryptKey:}
```

### 3. 挂载文件系统

挂载文件系统使用 `mount` 子命令，使用 `-d` 参数以守护进程的形式挂载。以下命令将刚刚创建的文件系统挂载到当前目录下的 `mnt` 目录：

```shell
$ sudo juicefs mount -d \
    rediss://default:your-password@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1 mnt
```

使用 sudo 执行挂载操作的目的是为了让 JuiceFS 能够有权限在 `/var/` 下创建缓存目录。值得注意的是，在挂载文件系统时，只需要指定`数据库地址`和`挂载点`，并不需要指定文件系统的名称。

看到类似下面的输出，代表文件系统挂载成功。

```shell
2021/08/23 16:39:14.202151 juicefs[2869081] <INFO>: Meta address: rediss://default@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1
2021/08/23 16:39:14.234925 juicefs[2869081] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/08/23 16:39:14.235536 juicefs[2869081] <INFO>: Ping redis: 446.247µs
2021/08/23 16:39:14.236231 juicefs[2869081] <INFO>: Data use space://juicefs/mystor/
2021/08/23 16:39:14.236540 juicefs[2869081] <INFO>: Disk cache (/var/jfsCache/6b0452fc-0502-404c-b163-c9ab577ec766/): capacity (1024 MB), free ratio (10%), max pending pages (15)
2021/08/23 16:39:14.738416 juicefs[2869081] <INFO>: OK, mystor is ready at mnt
```

使用 `df` 命令，可以看到文件系统的挂载情况：

```shell
$ df -Th
文件系统           类型          容量   已用  可用   已用% 挂载点
JuiceFS:mystor fuse.juicefs  1.0P   64K  1.0P   1% /home/herald/mnt
```

从挂载命令的输出信息中可以看到，JuiceFS 默认设置了 1024 MB 的作为本地缓存。设置更大的缓存，可以让 JuiceFS 有更好的性能表现，可以在挂载文件系统时通过 `--cache-size` 选项设置缓存（单位 MiB），例如，设置 20GB 的本地缓存：

```shell
$ sudo juicefs mount -d --cache-size 20000 \
    rediss://default:your-password@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1 mnt
```

文件系统挂载成功以后，就可以像使用本地硬盘那样，在 `~/mnt` 目录中存储数据了。

### 4. 查看文件系统

使用 `status` 子命令可以查看一个文件系统的基本信息和连接状态，只需指定数据库访问地址即可。

```shell
$ juicefs status rediss://default:bn8l7ui2cun4iaji@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1
2021/08/23 16:48:48.567046 juicefs[2869156] <INFO>: Meta address: rediss://default@private-db-redis-sgp1-03138-do-user-2500071-0.b.db.ondigitalocean.com:25061/1
2021/08/23 16:48:48.597513 juicefs[2869156] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/08/23 16:48:48.598193 juicefs[2869156] <INFO>: Ping redis: 491.003µs
{
  "Setting": {
    "Name": "mystor",
    "UUID": "6b0452fc-0502-404c-b163-c9ab577ec766",
    "Storage": "space",
    "Bucket": "https://juicefs.sgp1.digitaloceanspaces.com",
    "AccessKey": "7G7WQBY2QUCBQC5H2DGK",
    "SecretKey": "removed",
    "BlockSize": 4096,
    "Compression": "none",
    "Shards": 0,
    "Partitions": 0,
    "Capacity": 0,
    "Inodes": 0
  },
  "Sessions": [
    {
      "Sid": 1,
      "Heartbeat": "2021-08-23T16:46:14+08:00",
      "Version": "0.16.2 (2021-08-25T04:01:15Z 29d6fee)",
      "Hostname": "ubuntu-s-1vcpu-1gb-sgp1-01",
      "MountPoint": "/home/herald/mnt",
      "ProcessID": 2869091
    },
    {
      "Sid": 2,
      "Heartbeat": "2021-08-23T16:47:59+08:00",
      "Version": "0.16.2 (2021-08-25T04:01:15Z 29d6fee)",
      "Hostname": "ubuntu-s-1vcpu-1gb-sgp1-01",
      "MountPoint": "/home/herald/mnt",
      "ProcessID": 2869146
    }
  ]
}
```

### 5. 卸载文件系统

使用 `umount` 子命令卸载文件系统，比如：

```shell
sudo juicefs umount ~/mnt
```

> **注意**：强制卸载使用中的文件系统可能导致数据损坏或丢失，请务必谨慎操作。

### 6. 开机自动挂载

请参考[「启动时自动挂载 JuiceFS」](../administration/mount_at_boot.md)

### 7. 多主机共享挂载

JuiceFS 文件系统支持被多台云服务器同时挂载，而且对云服务器的地理位置没有要求，可以很容的实现同平台之间、跨云平台之间、公有云和私有云之间服务器的数据实时共享。

不单如此，JuiceFS 的共享挂载功能还能提供数据的强一致性保证，在多台服务器挂载了同一个文件系统时，文件系统上确认的写入会在所有主机上实时可见。

使用共享挂载功能，务必要确保组成文件系统的数据库和对象存储服务，能够被每一台要挂载它的主机正常访问。在本文的演示环境中，Spaces 对象存储是对整个互联网开放访问的，只要使用正确的秘钥就能够通过 API 进行读写。但对于平台托管的 Redis 数据库，你需要合理的配置访问策略，确保平台外的主机有访问权限。

在使用多主机共享挂载功能时，首先在任何一台主机上创建文件系统，然后在其他主机上安装 JuiceFS 客户端，使用同一个数据库地址通过 `mount` 命令挂载即可。特别注意，文件系统只需创建一次，不应该也不需要在其他主机上重复执行文件系统创建操作。
