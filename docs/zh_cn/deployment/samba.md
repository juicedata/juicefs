---
title: 创建 Samba 共享
sidebar_position: 8
description: 本文介绍如何通过 Samba 共享 JuiceFS 文件系统中的目录。
---
import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Samba 是一个开源的软件套件，它实现了 SMB/CIFS（Server Message Block / Common Internet File System）协议，该协议是 Windows 系统中常用的文件共享协议。通过 Samba，可以在 Linux/Unix 服务器上创建共享目录，允许 Windows 计算机通过网络访问和使用这些共享资源。

在安装了 Samba 的 Linux 系统上通过编辑 `smb.conf` 配置文件即可将本地目录创建成为共享文件夹，Windows 和 macOS 系统使用文件管理器就可以直接访问读写，Linux 需要安装 Samba 客户端访问。

当需要将 JuiceFS 文件系统中的目录通过 Samba 共享时，只需使用 `juicefs mount` 命令挂载，然后使用 JuiceFS 挂载点或子目录创建 Samba 共享即可。

:::note
`juicefs mount` 以 FUSE 接口的形式挂载为本地的用户态文件系统，与本地文件系统在形态和用法上无异，因此可以直接被用于创建 Samba 共享。
:::

## 第 1 步：安装 Samba

主流 Linux 发行版的包管理器都会提供 Samba：

<Tabs>
<TabItem value="debian" label="Debian 及衍生版本">

```shell
sudo apt install samba
```

</TabItem>
    <TabItem value="redhat" label="RHEL 及衍生版本">

```shell
sudo dnf install samba
```

</TabItem>
</Tabs>

如果需要配置 AD/DC，还需要安装其他的软件包，详情参考 [Samba 官方安装指南](https://wiki.samba.org/index.php/Distribution-specific_Package_Installation)。

## 第 2 步：启用 JuiceFS 的扩展属性支持

根据 [Samba 官方文档](https://wiki.samba.org/index.php/File_System_Support#File_systems_without_xattr_support)，建议使用支持扩展属性（xattr）的文件系统，JuiceFS 文件系统需要在挂载时使用 `--enable-xattr` 选项来启用扩展属性，例如：

```shell
sudo juicefs mount -d --enable-xattr sqlite3://myjfs.db /mnt/myjfs
```

对于通过 `/etc/fstab` 配置自动挂载的情况，可以在挂载选项部分添加 `enable-xattr` 选项，例如：

```ini
# <元数据引擎 URL> <挂载点> <文件系统类型> <挂载选项>
redis://127.0.0.1:6379/0 /mnt/myjfs juicefs _netdev,max-uploads=50,writeback,cache-size=1024000,enable-xattr 0 0
```

### 知识拓展：Samba 为什么需要文件系统支持扩展属性？

Samba 是一个基于 Linux/Unix 的软件，用途是面向 Windows 系统提供文件共享。由于 Windows 系统中很多文件和目录具有附加元数据（文件作者、关键字、图标位置等），这些信息通常是 POSIX 文件系统之外，需要以 xattr 的形式存储在 Windows 中的。为了保证这类文件可以正确的保存在 Linux 系统中，因此 Samba 建议使用支持扩展属性的文件系统创建共享。

## 第 3 步：创建 Samba 共享

假设 JuiceFS 的挂载点是 `/mnt/myjfs`，比如要把其中的 `media` 目录创建成为 Samba 共享，可以这样配置：

```ini
[Media]
    path = /mnt/myjfs/media
    guest ok = no
    read only = no
    browseable = yes
```

## 面向 macOS 的共享

苹果 macOS 系统支持直接访问 Samba 共享，与 Windows 类似，macOS 也存在一些额外的元数据（图标位置、Spotlight 搜索等）需要通过 xattr 来保存，Samba 4.9 及以上版本默认开启了对苹果系统的扩展属性支持。

如果 [Samba 版本低于 4.9](https://wiki.samba.org/index.php/Configure_Samba_to_Work_Better_with_Mac_OS_X)，需要在 Samba 的 [global] 全局配置部分添加 `ea support = yes` 选项来启用面向苹果系统的扩展属性支持，编辑配置文件 `/etc/samba/smb.conf`，例如：

```ini
[global]
    workgroup = SAMBA
    security = user
    passdb backend = tdbsam
    ea support = yes
```

## Samba 的用户管理

Samba 有一套自己的用户数据库，它与操作系统用户之间是独立的，但是 Samba 共享的是系统中的目录，因此必须有恰当的用户权限才能读写。

### 创建 Samba 用户

在为 Samba 创建用户时，要求该用户必须是系统中已经存在的用户，系统会自动进行映射，从而让 Samba 用户具有同名系统用户的权限。

- 如果系统中已存在该用户，假设该账户是 herald，则这样创建 Samba 账户：

    ```shell
    sudo smbpasswd -a herald
    ```

    根据命令提示设置密码即可，Samba 账户可以设置与系统用户不同的密码。

- 如果需要创建一个新的用户，以创建一个名为 `abc` 的用户为例，则这样操作：
    1. 创建用户：

        ```shell
        sudo adduser abc
        ```

    2. 创建同名的 Samba 用户：

        ```shell
        sudo smbpasswd -a abc
        ```

### 查看已创建的 Samba 用户

`pdbedit` 是一个 Samba 自带的用于管理 Samba 用户数据库的工具，可以使用该工具来列出所有已创建的 Samba 用户：

```shell
sudo pdbedit -L
```

它会列出所有已创建的 Samba 用户列表，包括用户名、用户的 SID（Security Identifier）和所属的组等信息。

## 扩展阅读

[《如何基于 JuiceFS 配置 Samba 和 NFS 共享》](https://juicefs.com/zh-cn/blog/usage-tips/configure-samba-and-nfs-shares-based-juicefs)，这篇文章介绍了如何使用 Cockpit 在浏览器中以图形化界面方式来管理 Samba 和 NFS 共享。
