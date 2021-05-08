# Linux 系统使用 JuiceFS

[快速上手指南](quick_start_guide.md) 中介绍了 JuiceFS 在 Linux 系统的中的使用方法。

## 编译安装 JuiceFS 客户端

### 1. 依赖的软件包

使用源代码手动编译 JuiceFS 客户端，你需要先安装以下工具：

- [Go](https://golang.org/) 1.14+
- GCC 5.4+

### 2. 手动编译

克隆仓库到本地：

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

进入 juicefs 目录：

```shell
$ cd juicefs
```

执行编译：

```shell
$ make
```

> **提示**：中国地区用户，可以设置  `GOPROXY` 加快 Go 模块的下载速度，例如： [Goproxy China](https://github.com/goproxy/goproxy.cn)。

## 开机自动挂载 JuiceFS

将  `juicefs` 重命名为 `mount.juicefs` 并复制到 `/sbin/` 目录：

```shell
$ sudo cp ./juicefs /sbin/mount.juicefs
```

编辑 `/etc/fstab` 配置文件，另起新行，参照以下格式添加一条记录：

```
<REDIS-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

- 请将 `<REDIS-URL>` 替换成实际的 redis 数据库地址，格式为 `redis://<user>:<password>@<host>:<port>/<db>`，例如：`redis://localhost:6379/1`。
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

