# JuiceFS 快速上手

创建 JuiceFS 文件系统，需要以下 3 个方面的准备：

1. 准备 Redis 数据库
2. 准备对象存储
3. 下载安装 JuiceFS 客户端

> 还不了解 JuiceFS？可以先查阅 [JuiceFS 是什么？](introduction.md)

## 1. 准备 Redis 数据库

你可以很容易的在云计算平台购买到各种配置的云 Redis 数据库，但如果你只是想要快速评估 JuiceFS，可以使用 Docker 快速的在本地电脑上运行一个 Redis 数据库实例：

```shell
$ sudo docker run -d --name redis \
	-v redis-data:/data \
	-p 6379:6379 \
	--restart unless-stopped \
	redis redis-server --appendonly yes
```

容器创建成功以后，可使用 `redis://127.0.0.1:6379` 访问 redis 数据库。

> **注意**：以上命令将 Redis 的数据持久化在 Docker 的 `redis-data` 数据卷当中，你可以按需修改数据持久化的存储位置。

> **安全提示**：以上命令创建的 Redis 数据库实例没有启用身份认证，且暴露了主机的 `6379` 端口，如果你要通过互联网访问这个数据库实例，请参考 [Redis Security](https://redis.io/topics/security) 中的建议。

有关 Redis 数据库相关的更多内容，[点此查看](databases_for_metadata.md#Redis)。

## 2. 准备对象存储

和 Redis 数据库一样，几乎所有的公有云计算平台都提供对象存储服务。因为 JuiceFS 支持几乎所有主流平台的对象存储服务，因此你可以根据个人偏好自由选择。你可以查看我们的 [对象存储支持列表和设置指南](how_to_setup_object_storage.md)，其中列出了 JuiceFS 目前支持的所有对象存储服务，以及具体的使用方法。

当然，如果你只是想要快速评估 JuiceFS，使用 Docker 可以很轻松的在本地电脑运行一个 MinIO 对象存储实例：

```shell
$ sudo docker run -d --name minio \
    -p 9000:9000 \
    -p 9900:9900 \
    -v $PWD/minio-data:/data \
    --restart unless-stopped \
    minio/minio server /data --console-address ":9900"
```

容器创建成功以后使用以下地址访问：

- **MinIO 管理界面**：http://127.0.0.1:9900
- **MinIO API**：http://127.0.0.1:9000

对象存储初始的 Access Key 和 Secret Key 均为 `minioadmin`。

> **注意**：最新的 MinIO 集成了新版控制台界面，以上命令通过 `--console-address ":9900"` 为控制台设置并映射了 `9900` 端口。另外，还将 MinIO 对象存储的数据路径映射到了当前目录下的 `minio-data` 文件夹中，你可以按需修改这些参数。

## 3. 安装 JuiceFS 客户端

JuiceFS 同时支持 Linux、Windows、macOS 三大操作系统平台，你可以在 [这里下载](https://github.com/juicedata/juicefs/releases/latest) 最新的预编译的二进制程序，请根据实际使用的系统和架构选择对应的版本。

以 x86 架构的 Linux 系统为例，下载文件名包含 `linux-amd64` 的压缩包：

```shell
$ JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
$ wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
```

解压并安装：

```shell
$ tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
$ sudo install juicefs /usr/local/bin
```

> **提示**：你也可以从源代码手动编译 JuiceFS 客户端。[查看详情](client_compile_and_upgrade.md)

## 4. 创建 JuiceFS 文件系统

创建 JuiceFS 文件系统要使用 `format` 子命令，需要同时指定用来存储元数据的 Redis 数据库和用来存储实际数据的对象存储。

以下命令将创建一个名为 `pics` 的 JuiceFS 文件系统，使用 Redis 中的 `1` 号数据库存储元数据，使用 MinIO 中创建的 `pics` 存储桶存储实际数据。

```shell
$ juicefs format \
	--storage minio \
	--bucket http://127.0.0.1:9000/pics \
	--access-key minioadmin \
	--secret-key minioadmin \
	redis://127.0.0.1:6379/1 \
	pics
```

执行命令后，会看到类似下面的内容输出，说明 JuiceFS 文件系统创建成功了。

```shell
2021/04/29 23:01:18.352256 juicefs[34223] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:01:18.354252 juicefs[34223] <INFO>: Ping redis: 132.185µs
2021/04/29 23:01:18.354758 juicefs[34223] <INFO>: Data use minio://127.0.0.1:9000/pics/pics/
2021/04/29 23:01:18.361674 juicefs[34223] <INFO>: Volume is formatted as {Name:pics UUID:9c0fab76-efd0-43fd-a81e-ae0916e2fc90 Storage:minio Bucket:http://127.0.0.1:9000/pics AccessKey:minioadmin SecretKey:removed BlockSize:4096 Compression:none Partitions:0 EncryptKey:}
```

可以通过 `juicefs format -h` 命令，获得创建文件系统的完整帮助信息。

> **注意**：你可以根据需要，创建无限多个 JuiceFS 文件系统。但需要注意的是，每个 Redis 数据库中只能创建一个文件系统。比如要再创建一个名为 `memory` 的文件系统时，可以使用 Redis 中的 2 号数据库，即 `redis://127.0.0.1:6379/2` 。

> **注意**：如果不指定 `--storage` 选项，JuiceFS 客户端会使用本地磁盘作为数据存储。使用本地存储时，JuiceFS 只能在本地单机使用，无法被网络内其他客户端挂载，[点此](how_to_setup_object_storage.md#local)查看详情。

## 5. 挂载 JuiceFS 文件系统

JuiceFS 文件系统创建完成以后，接下来就可以把它挂载到操作系统上使用了。以下命令将 `pics` 文件系统挂载到 `/mnt/jfs` 目录中。

```shell
$ sudo juicefs mount -d redis://127.0.0.1:6379/1 /mnt/jfs
```

> **注意**：挂载 JuiceFS 文件系统时，不需要显式指定文件系统的名称，只要填写正确的 Redis 服务器地址和数据库编号即可。

执行命令后，会看到类似下面的内容输出，说明 JuiceFS 文件系统已经成功挂载到系统上了。

```shell
2021/04/29 23:22:25.838419 juicefs[37999] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:22:25.839184 juicefs[37999] <INFO>: Ping redis: 67.625µs
2021/04/29 23:22:25.839399 juicefs[37999] <INFO>: Data use minio://127.0.0.1:9000/pics/pics/
2021/04/29 23:22:25.839554 juicefs[37999] <INFO>: Cache: /var/jfsCache/9c0fab76-efd0-43fd-a81e-ae0916e2fc90 capacity: 1024 MB
2021/04/29 23:22:26.340509 juicefs[37999] <INFO>: OK, pics is ready at /mnt/jfs
```

挂载完成以后就可以在 `/mnt/jfs` 目录中存取文件了，你可以执行 `df` 命令查看 JuiceFS 文件系统的挂载情况：

```shell
$ df -Th
文件系统       类型          容量  已用  可用 已用% 挂载点
JuiceFS:pics   fuse.juicefs  1.0P   64K  1.0P    1% /mnt/jfs
```

> **注意**：默认情况下， JuiceFS 的缓存位于 `/var/jfsCache` 目录，为了获得该目录的读写权限，这里使用了 sudo 命令，以管理员权限挂载的 JuiceFS 文件系统。普通用户在读写 `/mnt/jfs` 时，需要为用户赋予该目录的操作权限。

## 6. 开机自动挂载 JuiceFS

将  `juicefs` 客户端重命名为 `mount.juicefs` 并复制到 `/sbin/` 目录：

```shell
$ sudo cp /usr/local/bin/juicefs /sbin/mount.juicefs
```

> **注意**：执行以上命令之前，我们假设 `juicefs` 客户端程序已经在 `/usr/local/bin` 目录。你也可以直接从下载的客户端压缩包中再解压一份  `juicefs`  程序出来，按上述要求重命名并复制到 `/sbin/` 目录。

编辑 `/etc/fstab` 配置文件，另起新行，参照以下格式添加一条记录：

```
<META-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

- 请将 `<META-URL>` 替换成实际的 Redis 数据库地址，格式为 `redis://<user>:<password>@<host>:<port>/<db>`，例如：`redis://localhost:6379/1`。
- 请将 `<MOUNTPOINT>` 替换成文件系统实际的挂载点，例如：`/jfs`。
- 如果需要，请将 `[,<MOUNT-OPTIONS>]` 替换为实际要设置的 [挂载选项](command_reference.md#juicefs-mount)，多个选项之间用逗号分隔。

**例如：**

```
redis://localhost:6379/1    /jfs       juicefs     _netdev,max-uploads=50,writeback,cache-size=2048     0  0
```

> **注意**：默认情况下，CentOS 6 在系统启动时不会挂载网络文件系统，你需要执行命令开启网络文件系统的自动挂载支持：

```bash
$ sudo chkconfig --add netfs
```

## 7. 卸载文件系统

如果你需要卸载 JuiceFS 文件系统，可以先执行 `df` 命令查看系统中已挂载的文件系统信息：

```shell
$ sudo df -Th

文件系统       类型          容量  已用  可用 已用% 挂载点
...
JuiceFS:pics   fuse.juicefs  1.0P  1.1G  1.0P    1% /mnt/jfs
```

通过命令输出，可以看到，文件系统 `pics` 挂载点为 `/mnt/jfs`，执行 `umount` 子命令卸载：

```shell
$ sudo juicefs umount /mnt/jfs
```

> **提示**：执行 `juicefs umount -h` 命令，可以获取卸载命令的详细帮助信息。

### 卸载失败

如果执行命令后，文件系统卸载失败，提示 `Device or resource busy`：

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount /mnt/jfs: Device or resource busy
exit status 1
```

发生这种情况，可能是因为某些程序正在读写文件系统中的文件。为了确保数据安全，你应该首先排查是哪些程序正在与文件系统中的文件进行交互（例如通过 `lsof` 命令），并尝试结束他们之间的交互动作，然后再重新执行卸载命令。

> **风险提示**：以下内容包含的命令可能会导致文件损坏、丢失，请务必谨慎操作！

当然，在你能够确保数据安全的前提下，也可以在卸载命令中添加 `--force` 或 `-f` 参数，强制卸载文件系统：

```shell
$ sudo juicefs umount --force /mnt/jfs
```

也可以使用 `fusermount` 命令卸载文件系统：

```shell
$ sudo fusermount -u /mnt/jfs
```

## 你可能需要

- [macOS 系统使用 JuiceFS](juicefs_on_macos.md)
- [Windows 系统使用 JuiceFS](juicefs_on_windows.md)
