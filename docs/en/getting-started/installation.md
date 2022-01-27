---
sidebar_label: Installation & Upgrade
sidebar_position: 1
slug: /installation
---

# Installation & Upgrade

JuiceFS has good cross-platform capability and supports running on all kinds of operating systems of almost all major architectures, including and not limited to Linux, macOS, Windows, BSD, etc.

The JuiceFS client has only one binary file, you can download the pre-compiled version to unzip it and use it directly, or you can compile it manually with the source code.

## Install The Pre-compiled Client

You can find the latest version of the client for download at [GitHub](https://github.com/juicedata/juicefs/releases). Pre-compiled versions for different CPU architectures and operating systems are available in the download list for each version, so please take care to identify your choice, e.g.

| File Name                              | Description                                                     |
| ------------------------------------   | ----------------------------                                    |
| `juicefs-x.x.x-darwin-amd64.tar.gz`    | For macOS systems with Intel chips                              |
| `juicefs-x.x.x-linux-amd64.tar.gz`     | For Linux distributions on the x86 architecture                 |
| `juicefs-x.x.x-linux-arm64.tar.gz`     | For Linux distributions on the ARM architecture                 |
| `juicefs-x.x.x-windows-amd64.tar.gz`   | For Windows on the x86 architecture                             |
| `juicefs-hadoop-x.x.x-linux-amd64.jar` | Hadoop Java SDK for Linux distributions on the x86 architecture |

:::tip
For macOS on M1 series chips, you can use the `darwin-amd64` version of the client dependent on [Rosetta 2](https://support.apple.com/zh-cn/HT211861), or you can refer to [Manually Compiling](#manually-compiling) to compile the native version.
:::

### Linux

For Linux systems with x86 architecture, download the file with the file name `linux-amd64` and execute the following command in the terminal.

1. Get the latest version number

   ```shell
   JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
   ```

2. Download the client to the current directory

   ```shell
   wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
   ```

3. Unzip the installation package

   ```shell
   tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
   ```

4. Install the client

   ```shell
   sudo install juicefs /usr/local/bin
   ```

After completing the above 4 steps, execute the `juicefs` command in the terminal and the help message will be returned, then the client installation is successful.

:::info
If the terminal prompts `command not found`, it may be that `/usr/local/bin` is not in your system's `PATH` environment variable. You can run `echo $PATH` to see which executable paths are set, select an appropriate path based on the return result, adjust and re-execute the installation command in step 4.
:::

### Windows

There are two ways to use JuiceFS on Windows systems.

1. [Using Pre-compiled Windows client](#pre-compiled-windows-client)
2. [Using the Linux client in WSL](#using-the-linux-client-in-wsl)

#### Pre-compiled Windows Client

The Windows client of JuiceFS is also a standalone binary that can be downloaded and unpacked to run directly.

1. Installing Dependencies

   Since Windows does not natively support the FUSE interface, you first need to download and install [WinFsp](http://www.secfs.net/winfsp/) in order to implement FUSE support.

   :::tip
   **[WinFsp](https://github.com/billziss-gh/winfsp)** is an open source Windows file system agent that provides a FUSE emulation layer that allows JuiceFS clients to mount file systems for use on Windows systems.
   :::

2. Install the client

   Take Windows 10 system as an example, download the file with the filename `windows-amd64`, unzip it and get `juicefs.exe` which is the JuiceFS client binary.

   To make it easier to use, you can create a folder named `juicefs` in the root directory of the `C:\` disk, and extract `juicefs.exe` to that folder. Then add `C:\juicefs` to the environment variables of your system, restart the system to let the settings take effect, and then you can run `juicefs` commands directly using the `Command Prompt` or `PowerShell` terminal that come with your system.

   ![Windows ENV path](../images/windows-path-en.png)

#### Using the Linux client in WSL

[WSL](https://docs.microsoft.com/en-us/windows/wsl/about) is the full name of Windows Subsystem for Linux, which is supported from Windows 10 version 2004 onwards or Windows 11. It allows you to run most of the command-line tools, utilities, and applications of GNU/Linux natively on a Windows system without incurring the overhead of a traditional virtual machine or dual-boot setup.

For details, see "[Using JuiceFS on WSL](../tutorials/juicefs_on_wsl.md)"

### macOS

Since macOS does not support the FUSE interface by default, you need to install [macFUSE](https://osxfuse.github.io/) first to implement support for FUSE.

:::tip
[macFUSE](https://github.com/osxfuse/osxfuse) is an open source file system enhancement tool that allows macOS to mount third-party file systems, enabling JuiceFS clients to mount file systems for use on macOS systems.
:::

#### Homebrew

If you have the [Homebrew](https://brew.sh/) package manager installed on your system, you can install the JuiceFS client by executing the following command.

```shell
brew tap juicedata/homebrew-tap
brew install juicefs
```

#### Pre-compiled Binary

You can also download the binary with the filename of `darwin-amd64`, unzip it and install the program to any executable path on your system using the `install` command, e.g.

```shell
sudo install juicefs /usr/local/bin
```

### Docker

For cases where you want to use JuiceFS in a Docker container, here is a `Dockerfile` for building a JuiceFS client image, which can be used as a base to build a JuiceFS client image alone or packaged together with other applications.

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

## Manually Compiling

If the pre-compiled client does not have a version for you, such as FreeBSD or macOS on the M1 chip, then you can use manual compilation to compile the JuiceFS client for you.

In addition, manually compiling the client will give you priority access to various new features in JuiceFS development, but it requires some basic knowledge of software compilation.

### Unix-like Client

Compiling clients for Linux, macOS, BSD and other Unix-like systems requires the following dependencies:

- [Go](https://golang.org) 1.16+
- GCC 5.4+

1. Cloning source code

   ```shell
   git clone https://github.com/juicedata/juicefs.git
   ```

2. Enter the source code directory

   ```shell
   cd juicefs
   ```

3. Switching the branch

   The source code uses the `main` branch by default, and you can switch to any official release, for example to `v0.17.4`.

   ```shell
   git checkout v0.17.4
   ```

   :::caution
   The development branch often involves large changes, so please do not use the clients compiled in the "development branch" for the production environment.
   :::

4. Compiling

   ```shell
   make
   ```

   The compiled `juicefs` binary is located in the current directory.

### Compiling on Windows

To compile the JuiceFS client on Windows, you need to install [Go](https://golang.org) 1.16+ and GCC 5.4+.

Since GCC does not have a native Windows client, you need to use the version provided by a third party, either [MinGW-w64](https://sourceforge.net/projects/mingw-w64/) or [Cygwin](https://www.cygwin.com/). Here is the example of MinGW-w64.

Download MinGW-w64 and add its `bin` directory to the system environment variables.

1. Clone and enter the project directory at:

   ```shell
   git clone https://github.com/juicedata/juicefs.git && cd juicefs
   ```

2. Copy winfsp headers

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

3. Compile client

   ```shell
   go build -ldflags="-s -w" -o juicefs.exe ./cmd
   ```

### Cross-compiling Windows clients in Linux

Compiling a specific version of the client for Windows is essentially the same as [Unix-like Client](#unix-like-client) and can be done directly on a Linux system, but in addition to `go` and `gcc`, which must be installed, you also need to install:

- [mingw-w64](https://www.mingw-w64.org/downloads/)

Just install the latest version provided by the Linux distribution package manager, e.g. Ubuntu 20.04+ can be installed directly as follows.

```shell
sudo apt install mingw-w64
```

Compile the Windows client:

```shell
make juicefs.exe
```

The compiled client is a binary file named `juicefs.exe`, located in the current directory.

## Upgrade

The JuiceFS client has only one binary file, so to upgrade the new version you only need to replace the old one with the new one.

- **Use pre-compiled client**: You can refer to the installation method of the corresponding system in this document, download the latest client, and overwrite the old one.
- **Manually compile the client**: You can pull the latest source code and recompile it to overwrite the old version of the client.

:::caution
For the file system that has been mounted using the old version of JuiceFS client, you need to [unmount file system](for_distributed.md#6-unmounting-the-file-system), and then re-mount it with the new version of JuiceFS client.
:::

## Uninstall

The JuiceFS client has only one binary file, which can be deleted by simply finding the location of the program. For example, referring to the client installed on the Linux system in this document, execute the following command to uninstall the client.

```shell
sudo rm /usr/local/bin/juicefs
```

You can also see where the program is located by using the `which` command.

```shell
which juicefs
```

The path returned by the command is the location where the JuiceFS client is installed on your system. For other operating systems uninstallation methods follow the same pattern.
