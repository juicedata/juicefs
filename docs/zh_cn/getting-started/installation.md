---
title: 安装
sidebar_position: 1
description: 本文介绍 JuiceFS 在 Linux、macOS 和 Windows 上的安装方法，包括一键安装、编译安装和容器化安装。
---

JuiceFS 有良好的跨平台能力，支持在几乎所有主流架构的各类操作系统上运行，包括且不限于 Linux、macOS、Windows 等。

JuiceFS 客户端只有一个二进制文件，你可以下载预编译的版本直接解压使用，也可以用源代码手动编译。

## 一键安装 {#one-click-installation}

一键安装脚本适用于 Linux 和 macOS 系统，会根据你的硬件架构自动下载安装最新版 JuiceFS 客户端。

```shell
# 默认安装到 /usr/local/bin
curl -sSL https://d.juicefs.com/install | sh -
```

```shell
# 安装到 /tmp 目录下
curl -sSL https://d.juicefs.com/install | sh -s /tmp
```

## 安装预编译客户端 {#install-the-pre-compiled-client}

你可以在 [GitHub](https://github.com/juicedata/juicefs/releases) 找到最新版客户端下载地址，每个版本的下载列表中都提供了面向不同 CPU 架构和操作系统的预编译版本，请注意识别选择，例如：

| 文件名                               | 说明                                                                            |
|--------------------------------------|---------------------------------------------------------------------------------|
| `juicefs-x.y.z-darwin-amd64.tar.gz`  | 面向 Intel 芯片的 macOS 系统                                                    |
| `juicefs-x.y.z-darwin-arm64.tar.gz`  | 面向 M1 系列芯片的 macOS 系统                                                   |
| `juicefs-x.y.z-linux-amd64.tar.gz`   | 面向 x86 架构 Linux 发行版                                                      |
| `juicefs-x.y.z-linux-arm64.tar.gz`   | 面向 ARM 架构的 Linux 发行版                                                    |
| `juicefs-x.y.z-windows-amd64.tar.gz` | 面向 x86 架构的 Windows 系统                                                    |
| `juicefs-hadoop-x.y.z.jar`           | 面向 x86 和 ARM 架构的 Hadoop Java SDK（同时支持 Linux、macOS 及 Windows 系统） |

### Linux 发行版 {#linux}

以 x86 架构的 Linux 系统为例，下载文件名包含 `linux-amd64` 的压缩包，在终端依次执行以下命令。

1. 获取最新的版本号

   ```shell
   JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
   ```

2. 下载客户端到当前目录

   ```shell
   wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
   ```

3. 解压安装包

   ```shell
   tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
   ```

4. 安装客户端

   ```shell
   sudo install juicefs /usr/local/bin
   ```

完成上述 4 个步骤，在终端执行 `juicefs` 命令，返回帮助信息，则说明客户端安装成功。

:::info 说明
如果终端提示 `command not found`，可能是因为 `/usr/local/bin` 不在你的系统 `PATH` 环境变量中，可以执行 `echo $PATH` 查看系统设置了哪些可执行路径，根据返回结果选择一个恰当的路径，调整并重新执行第 4 步的安装命令。
:::

#### Ubuntu PPA

JuiceFS 也提供 [PPA](https://launchpad.net/~juicefs) 仓库，可以方便地在 Ubuntu 系统上安装最新版的客户端。根据你的 CPU 架构选择对应的 PPA 仓库：

- **x86 架构**：`ppa:juicefs/ppa`
- **ARM 架构**：`ppa:juicefs/arm64`

以 x86 架构的 Ubuntu 22.04 系统为例，执行以下命令。

```shell
sudo add-apt-repository ppa:juicefs/ppa
sudo apt-get update
sudo apt-get install juicefs
```

#### Fedora Copr

JuiceFS 也提供 [Copr](https://copr.fedorainfracloud.org/coprs/juicedata/juicefs) 仓库，可以方便地在 Red Hat 及其衍生系统上安装最新版的客户端，目前支持的系统有：

- **Amazonlinux 2023**
- **CentOS 8, 9**
- **Fedora 37, 38, 39, rawhide**
- **RHEL 7, 8, 9**

以 Fedora 38 系统为例，执行以下命令安装客户端：

```shell
# 启用 Copr 仓库
sudo dnf copr enable -y juicedata/juicefs
# 安装客户端
sudo dnf install juicefs
```

#### Snapcraft

我们也在 [Canonical Snapcraft](https://snapcraft.io) 平台打包并发布了 [Snap 版本的 JuiceFS 客户端](https://github.com/juicedata/juicefs-snapcraft)，对于 Ubuntu 16.04 及以上版本和其他支持 Snap 的操作系统，可以直接使用以下命令安装：

```shell
sudo snap install juicefs
# 由于 Snap 是一个封闭的沙箱环境，它会影响客户端的 FUSE 挂载，执行以下命令可以解除限制。
# 如果只需使用 WebDAV 和 Gateway 则不必执行以下命令。
sudo ln -s -f /snap/juicefs/current/juicefs /snap/bin/juicefs
```

当有新版本时，执行以下命令更新客户端：

```shell
sudo snap refresh juicefs
```

#### AUR (Arch User Repository) {#aur}

JuiceFS 也提供 [AUR](https://aur.archlinux.org/packages/juicefs) 仓库，可以方便地在 Arch Linux 及其衍生系统上安装最新版的客户端。

对于使用 Yay 包管理器的系统，执行以下命令安装客户端：

```shell
yay -S juicefs
```

:::info 说明
AUR 上存在多个 JuiceFS 客户端的打包，以下是 JuiceFS 官方维护的版本：

- [`aur/juicefs`](https://aur.archlinux.org/packages/juicefs)：是稳定编译版，安装时会拉取最新的稳定版源码并编译安装；
- [`aur/juicefs-bin`](https://aur.archlinux.org/packages/juicefs-bin)：是稳定预编译版，安装时会直接下载最新的稳定版预编译程序并安装；
- [`aur/juicefs-git`](https://aur.archlinux.org/packages/juicefs-git)：是开发版，安装时会拉取最新的开发版源码并编译安装；
:::

另外，你也可以使用 `makepkg` 手动编译安装，以 Arch Linux 系统为例：

```shell
# 安装依赖
sudo pacman -S base-devel git go
# 克隆要打包的 AUR 仓库
git clone https://aur.archlinux.org/juicefs.git
# 进入仓库目录
cd juicefs
# 编译安装
makepkg -si
```

### Windows 系统 {#windows}

在 Windows 系统安装 JuiceFS 有以下几种方法：

- [使用预编译的 Windows 客户端](#预编译的-windows-客户端)
- [使用 Scoop 安装](#scoop)
- [在 WSL 中使用 Linux 版客户端](#在-wsl-中使用-linux-版客户端)

#### 预编译的 Windows 客户端

JuiceFS 的 Windows 客户端也是一个独立的二进制程序，下载解压即可直接运行使用。

1. 安装依赖程序

   由于 Windows 没有原生支持 FUSE 接口，首先需要下载安装 [WinFsp](https://winfsp.dev) 才能实现对 FUSE 的支持。

   :::tip 提示
   **[WinFsp](https://github.com/winfsp/winfsp)** 是一个开源的 Windows 文件系统代理，它提供了一个 FUSE 仿真层，使得 JuiceFS 客户端可以将文件系统挂载到 Windows 系统中使用。
   :::

2. 安装客户端

   以 Windows 10 系统为例，下载文件名包含 `windows-amd64` 的压缩包，解压后得到 `juicefs.exe` 即是 JuiceFS 的客户端程序。

   为了便于使用，可以在 `C:\` 盘根目录创建一个名为 `juicefs` 的文件夹，把 `juicefs.exe` 解压到该文件夹中。然后将 `C:\juicefs` 文件夹路径添加到系统的环境变量，重启系统让设置生效以后，可直接使用使用系统自带的「命令提示符」或「PowerShell」等终端程序运行 `juicefs` 命令。

   ![Windows ENV path](../images/windows-path.png)

#### 使用 Scoop 安装 {#scoop}

如果你的 Windows 系统中安装了 [Scoop](https://scoop.sh)，可以使用以下命令安装最新版的 JuiceFS 客户端：

```shell
scoop install juicefs
```

#### 在 WSL 中使用 Linux 版客户端

[WSL](https://docs.microsoft.com/zh-cn/windows/wsl/about) 全称 Windows Subsystem for Linux，即 Windows 的 Linux 子系统，从 Windows 10 版本 2004 以上或 Windows 11 开始支持该功能。它可以让你在 Windows 系统中运行原生的 GNU/Linux 的大多数命令行工具、实用工具和应用程序且不会产生传统虚拟机或双启动设置开销。

详情查看「[在 WSL 中使用 JuiceFS](../tutorials/juicefs_on_wsl.md)」

### macOS 系统 {#macos}

由于 macOS 默认不支持 FUSE 接口，需要先安装 [macFUSE](https://osxfuse.github.io) 实现对 FUSE 的支持。

:::tip 提示
[macFUSE](https://github.com/osxfuse/osxfuse) 是一个开源的文件系统增强工具，它让 macOS 可以挂载第三方的文件系统，使得 JuiceFS 客户端可以将文件系统挂载到 macOS 系统中使用。
:::

#### Homebrew 安装

如果你的系统安装了 [Homebrew](https://brew.sh) 包管理器，可以执行以下命令安装 JuiceFS 客户端：

```shell
brew install juicefs
```

*请参考 [Homebrew Formulae](https://formulae.brew.sh/formula/juicefs#default) 页面了解命令详情。*

#### 预编译二进制程序

你也可以下载文件名包含 `darwin-amd64` 的二进制程序，解压后使用 `install` 命令将程序安装到系统的任意可执行路径，例如：

```shell
sudo install juicefs /usr/local/bin
```

### Docker 容器 {#docker}

对于要在 Docker 容器中使用 JuiceFS 的情况，这里提供一份构建 JuiceFS 客户端镜像的 `Dockerfile`，可以以此为基础单独构建 JuiceFS 客户端镜像或与其他应用打包在一起使用。

```dockerfile
FROM ubuntu:20.04

RUN apt update && apt install -y curl fuse && \
    apt-get autoremove && \
    apt-get clean && \
    rm -rf \
    /tmp/* \
    /var/lib/apt/lists/* \
    /var/tmp/*

RUN set -x && \
    mkdir /juicefs && \
    cd /juicefs && \
    JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v') && \
    curl -s -L "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" \
    | tar -zx && \
    install juicefs /usr/bin && \
    cd .. && \
    rm -rf /juicefs

CMD [ "juicefs" ]
```

## 手动编译客户端 {#manually-compiling}

如果预编译的客户端中没有适用于你的版本（比如 FreeBSD），这时可以采用手动编译的方式编译适合你的 JuiceFS 客户端。

另外，手动编译客户端可以让你优先体验到 JuiceFS 开发中的各种新功能，但这需要你具备一定的软件编译相关的基础知识。

:::tip 提示
对于中国地区用户，为了加快获取 Go 模块的速度，建议通过执行 `go env -w GOPROXY=https://goproxy.cn,direct` 来将 `GOPROXY` 环境变量设置国内的镜像服务器。详情请参考：[Goproxy China](https://github.com/goproxy/goproxy.cn)。
:::

### 类 Unix 客户端

编译面向 Linux、macOS、BSD 等类 Unix 系统的客户端需要满足以下依赖：

- [Go](https://golang.org) 1.20+
- GCC 5.4+

1. 克隆源码

   ```shell
   git clone https://github.com/juicedata/juicefs.git
   ```

2. 进入源代码目录

   ```shell
   cd juicefs
   ```

3. 切换分支

   源代码默认使用 `main` 分支，你可以切换到任何正式发布的版本，比如切换到 `v1.0.0` 版本：

   ```shell
   git checkout v1.0.0
   ```

   :::caution 注意
   开发分支经常涉及较大的变化，请不要将「开发分支」编译的客户端用于生产环境。
   :::

4. 执行编译

   ```shell
   make
   ```

   编译好的 `juicefs` 二进制程序位于当前目录。

### 在 Windows 下编译

在 Windows 系统编译 JuiceFS 客户端需要安装以下依赖：

- [WinFsp](https://github.com/winfsp/winfsp)
- [Go](https://golang.org) 1.20+
- GCC 5.4+

其中，WinFsp 和 Go 直接下载安装即可。GCC 需要使用第三方提供的版本，可以使用 [MinGW-w64](https://www.mingw-w64.org) 或 [Cygwin](https://www.cygwin.com)，这里以 MinGW-w64 为例介绍。

在 [MinGW-w64 的下载页面](https://www.mingw-w64.org/downloads) 选择一个适用于 Windows 的预编译版本，比如 [mingw-builds-binaries](https://github.com/niXman/mingw-builds-binaries/releases)。下载完成后，将其解压到 `C` 盘根目录，然后在系统环境变量设置中找到 PATH 并添加 `C:\mingw64\bin` 目录，重启系统后在命令行或 PowerShell 中执行 `gcc -v` 命令，如果能看到版本信息则说明 MingGW-w64 安装成功，接下来就可以开始编译了。

1. 克隆并进入项目目录

   ```shell
   git clone https://github.com/juicedata/juicefs.git && cd juicefs
   ```

2. 复制 WinFsp 头文件

   ```shell
   mkdir "C:\WinFsp\inc\fuse"
   ```

   ```shell
   copy .\hack\winfsp_headers\* C:\WinFsp\inc\fuse\
   ```

   ```shell
   dir "C:\WinFsp\inc\fuse"
   ```

   ```shell
   set CGO_CFLAGS=-IC:/WinFsp/inc/fuse
   ```

   ```shell
   go env -w CGO_CFLAGS=-IC:/WinFsp/inc/fuse
   ```

3. 编译客户端

   ```shell
   go build -ldflags="-s -w" -o juicefs.exe .
   ```

编译好的 `juicefs.exe` 二进制程序位于当前目录。为了方便使用，可以将其移动到 `C:\Windows\System32` 目录下，这样就可以在任何地方直接使用 `juicefs.exe` 命令了。

### 在 Linux 中交叉编译 Windows 客户端

为 Windows 编译特定版本客户端的过程与[类 Unix 客户端](#类-unix-客户端)基本一致，可以直接在 Linux 系统中进行编译，但除了 `go` 和 `gcc` 必须安装以外，还需要安装 [MinGW-w64](https://www.mingw-w64.org/downloads)

安装 Linux 发行版包管理器提供的最新版本即可，例如 Ubuntu 20.04+ 可以直接安装：

```shell
sudo apt install mingw-w64
```

编译 Windows 客户端：

```shell
make juicefs.exe
```

编译好的客户端是一个名为 `juicefs.exe` 的二进制文件，位于当前目录。

### 在 macOS 中交叉编译 Linux 客户端

1. 克隆并进入项目目录

   ```shell
   git clone https://github.com/juicedata/juicefs.git && cd juicefs
   ```

2. 安装依赖

   ```shell
   brew install FiloSottile/musl-cross/musl-cross
   ```

3. 编译客户端

   ```shell
   make juicefs.linux
   ```

## 卸载客户端 {#uninstall}

JuiceFS 客户端只有一个二进制文件，只需找到程序所在位置删除即可。例如，参照本文档 Linux 系统安装的客户端，执行以下命令卸载客户端：

```shell
sudo rm /usr/local/bin/juicefs
```

你还可以通过 `which` 命令查看程序所在位置：

```shell
which juicefs
```

命令返回的路径即 JuiceFS 客户端在你系统上的安装位置。其他操作系统卸载方法依此类推。
