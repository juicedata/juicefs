---
title: 创建 NFS 共享
sidebar_position: 9
description: 本文介绍如何通过 NFS 共享 JuiceFS 文件系统中的目录。
---

NFS（Network File System）是一种网络文件共享协议，允许不同计算机之间通过网络共享文件和目录。它最初由 Sun Microsystems 开发，是一种在 Unix 和类 Unix 系统之间进行文件共享的标准方式。NFS 协议允许客户端像访问本地文件系统一样访问远程文件系统，从而实现透明的远程文件访问。

当需要将 JuiceFS 文件系统中的目录通过 NFS 共享时，只需使用 `juicefs mount` 命令挂载，然后使用 JuiceFS 挂载点或子目录创建 NFS 共享即可。

:::note
`juicefs mount` 以 FUSE 接口的形式挂载为本地的用户态文件系统，与本地文件系统在形态和用法上无异，因此可以直接被用于创建 NFS 共享。
:::

## 第 1 步：安装 NFS

配置 NFS 共享需要分别在服务端和客户端安装相应的软件包，以 Ubuntu/Debian 系统为例：

### 1. 服务端安装

创建 NFS 共享的主机（JuiceFS 文件系统也挂载在该服务器上）。

```shell
sudo apt install nfs-kernel-server
```

### 2. 客户端安装

所有需要访问 NFS 的 Linux 主机都需要安装客户端。

```shell
sudo apt install nfs-common
```

## 第 2 步：创建共享

这里假设 JuiceFS 在服务端系统的挂载点是 `/mnt/myjfs`，比如要将其中的 `media` 子目录设置为 NFS 共享，可以在服务端系统的 `/etc/exports` 文件中添加如下配置：

```
"/mnt/myjfs/media" *(rw,sync,no_subtree_check,fsid=1)
```

NFS 共享配置的语法为：

```
<Share Path> <Allowed IPs>(options)
```

比如要将这个共享设置为仅允许 `192.168.1.0/24` 这个 IP 段的主机挂载且避免挤压 root 权限，则可以修改为：

```
"/mnt/myjfs/media" 192.168.1.0/24(rw,async,no_subtree_check,no_root_squash,fsid=1)
```

### 共享选项说明

**其中涉及的共享选项：**

- `rw`：代表允许读和写，如果只允许读则使用 `ro`。
- `sync` 与 `async`：`sync` 为同步写入，当向 NFS 共享写入文件时，客户端会等待服务端确认数据写入成功后再进行后续操作。`async` 为异步写入，写入操作是异步的，在写数据到 NFS 共享时，客户端不会等待服务器确认是否成功写入，而是立即执行后续操作。
- `no_subtree_check`：禁用子目录检查，这将允许客户端挂载共享目录的父目录和子目录，会降低一些安全性但能提高 NFS 的兼容性。也可以设置为 `subtree_check` 来启用子目录检查，这样仅允许客户端挂载共享目录和它的子目录。
- `no_root_squash`：用于控制客户端 root 用户访问 NFS 共享时的身份映射行为。默认情况下，客户端以 root 身份挂载 NFS 共享时，服务端会将其映射为非特权用户（通常是 nobody 或 nfsnobody），这被称为 root 挤压。设置该选项后，则取消这种权限挤压，从而让客户端拥有服务端相同的 root 用户权限。该选项有一定安全风险，建议谨慎使用。
- `fsid`：文件系统标识符，用于在 NFS 上标识不同的文件系统。在 NFSv4 中，NFS 的根目录所在的文件系统被定义为 fsid=0，其他文件系统需要在它之下且编号唯一。在这里，JuiceFS 就是一个外挂的 FUSE 文件系统，因此需要给它设置一个唯一的标识。

### async 与 sync 模式的选择

对于 NFS 共享而言，sync（同步写入）模式可以提高数据的可靠性，但总是需要等待服务器确认成功写入才会执行下一个操作，这势必会导致写入速度降低。对于 JuiceFS 这种基于云上对象存储的文件系统，还需要进一步考虑网络延时的影响，使用 sync 模式往往会导致较低的写入性能。

通常情况下，在使用 JuiceFS 创建 NFS 共享时，建议将写入模式设置为 async（异步写入），从而避免损失写入性能。如果为了保证数据可靠性而必须使用 sync 模式时，建议为 JuiceFS 设置容量充足的高性能 SSD 磁盘作为本地缓存，并开启 writeback 写缓存模式。
