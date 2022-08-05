---
sidebar_label: Installation
sidebar_position: 1
slug: /installation
---

# Installation

JuiceFS has good cross-platform capability and supports running on all kinds of operating systems of almost all major architectures, including and not limited to Linux, macOS, Windows, etc.

The JuiceFS client has only one binary file, you can download the pre-compiled version to unzip it and use it directly, or you can compile it manually with the source code.

## Install the pre-compiled client

You can download the latest version of the client at [GitHub](https://github.com/juicedata/juicefs/releases). Pre-compiled versions for different CPU architectures and operating systems are available in the download list of each client version. Please find the version suit your application the best, e.g.,

| File Name                            | Description                                                                          |
|--------------------------------------|--------------------------------------------------------------------------------------|
| `juicefs-x.x.x-darwin-amd64.tar.gz`  | For macOS systems with Intel chips                                                   |
| `juicefs-x.x.x-darwin-arm64.tar.gz`  | For macOS systems with M1 series chips                                               |
| `juicefs-x.x.x-linux-amd64.tar.gz`   | For Linux distributions on x86 architecture                                          |
| `juicefs-x.x.x-linux-arm64.tar.gz`   | For Linux distributions on ARM architecture                                          |
| `juicefs-x.x.x-windows-amd64.tar.gz` | For Windows on x86 architecture                                                      |
| `juicefs-hadoop-x.x.x-amd64.jar`     | Hadoop Java SDK on x86 architecture (supports both Linux, macOS and Windows systems) |

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

After completing the above 4 steps, execute the `juicefs` command in the terminal. A help message will be returned if the client installation is successful.

:::info
If the terminal prompts `command not found`, it is probably because `/usr/local/bin` is not in your system's `PATH` environment variable. You can check which executable paths are set by running `echo $PATH`, then select an appropriate path based on the return result, adjust and re-execute the installation command following the above step 4.
:::

### Windows

There are two ways to use JuiceFS on Windows systems.

1. [Using pre-compiled Windows client](#pre-compiled-windows-client)
2. [Using Linux client in WSL](#using-linux-client-in-wsl)

#### Pre-compiled Windows client

The Windows client of JuiceFS is also a standalone binary. Once downloaded and unpacked, you can run it right away.

1. Installing Dependencies

   Since Windows does not natively support the FUSE interface, you need to download and install [WinFsp](https://winfsp.dev) first in order to implement FUSE support.

   :::tip
   **[WinFsp](https://github.com/winfsp/winfsp)** is an open source Windows file system agent that provides a FUSE emulation layer that allows JuiceFS clients to mount file systems on Windows systems for use.
   :::

2. Install the client

   Take Windows 10 system as an example, download the file with the filename `windows-amd64`, unzip it and get `juicefs.exe` which is the JuiceFS client binary.

   To make it easier to use, it is recommended to create a folder named `juicefs` in the root directory of the `C:\` disk, and extract `juicefs.exe` to that folder. Then add `C:\juicefs` to the environment variables of your system, and restart the system to let the settings take effect. Lastly, you can run `juicefs` commands directly using the "Command Prompt" or "PowerShell" terminal that comes with your system.

   ![Windows ENV path](../images/windows-path-en.png)

#### Using Linux client in WSL

[WSL](https://docs.microsoft.com/en-us/windows/wsl/about) is short for Windows Subsystem for Linux, which is supported from Windows 10 version 2004 onwards or Windows 11. It allows you to run most of the command-line tools, utilities, and applications of GNU/Linux natively on a Windows system without incurring the overhead of a traditional virtual machine or dual-boot setup.

For details, see "[Using JuiceFS on WSL](../tutorials/juicefs_on_wsl.md)"

### macOS

Since macOS does not support the FUSE interface by default, you need to install [macFUSE](https://osxfuse.github.io/) first to implement the support for FUSE.

:::tip
[macFUSE](https://github.com/osxfuse/osxfuse) is an open source file system enhancement tool that allows macOS to mount third-party file systems, enabling JuiceFS clients to mount file systems on macOS systems.
:::

#### Homebrew

If you have the [Homebrew](https://brew.sh/) package manager installed on your system, you can install the JuiceFS client by executing the following command.

```shell
brew tap juicedata/homebrew-tap
brew install juicefs
```

#### Pre-compiled binary

You can also download the binary with the filename of `darwin-amd64`, unzip it and install the program to any executable path on your system using the `install` command, e.g.

```shell
sudo install juicefs /usr/local/bin
```

### Docker

In cases one wants to use JuiceFS in a Docker container, a `Dockerfile` for building a JuiceFS client image is provided below, which can be used as a base to build a JuiceFS client image alone or packaged together with other applications.

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

## Manually compiling

If there is no pre-compiled client versions that are suitable for your operating system, such as FreeBSD or macOS on the M1 chip, then you can manually compile the JuiceFS client.

One of the advantages of manually compiling client is that you have priority access to various new features in JuiceFS development, but it requires some basic knowledge of software compilation.

:::tip
For users in China, in order to speed up the acquisition of Go modules, it is recommended to set the `GOPROXY` environment variable to the domestic mirror server by executing `go env -w GOPROXY=https://goproxy.cn,direct`. For details, please refer to: [Goproxy China](https://github.com/goproxy/goproxy.cn).
:::

### Unix-like client

Compiling clients for Linux, macOS, BSD and other Unix-like systems requires the following dependencies:

- [Go](https://golang.org) 1.17+
- GCC 5.4+

1. Clone source code

   ```shell
   git clone https://github.com/juicedata/juicefs.git
   ```

2. Enter the source code directory

   ```shell
   cd juicefs
   ```

3. Switching the branch

   The source code uses the `main` branch by default, and you can switch to any official release, for example to the release `v1.0.0`:

   ```shell
   git checkout v1.0.0
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

Compiling the JuiceFS client on Windows requires [Go](https://golang.org) 1.17+ and GCC 5.4+.

Since GCC does not have a native Windows client, the version provided by a third party, either [MinGW-w64](https://sourceforge.net/projects/mingw-w64/) or [Cygwin](https://www.cygwin.com/) is needed. Here is an example of using MinGW-w64.

Download MinGW-w64 and add its `bin` directory to the system environment variables.

1. Clone and enter the project directory

   ```shell
   git clone https://github.com/juicedata/juicefs.git && cd juicefs
   ```

2. Copy WinFsp headers

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
   go build -ldflags="-s -w" -o juicefs.exe .
   ```

### Cross-compiling Windows clients on Linux

Compiling a specific version of the client for Windows is essentially the same as [Unix-like Client](#unix-like-client) and can be done directly on a Linux system. However, in addition to `go` and `gcc`, you also need to install:

- [MinGW-w64](https://www.mingw-w64.org/downloads)

The latest version can be installed from software repositories on many Linux distributions. Take an example of Ubuntu 20.04+: `mingw-w64` can be installed as follows.

```shell
sudo apt install mingw-w64
```

Compile the Windows client:

```shell
make juicefs.exe
```

The compiled client is a binary file named `juicefs.exe`, located in the current directory.

### Cross-compiling Linux clients on macOS

1. Clone and enter the project directory

   ```shell
   git clone https://github.com/juicedata/juicefs.git && cd juicefs
   ```

2. Install dependencies

   ```shell
   brew install FiloSottile/musl-cross/musl-cross
   ```

3. Compile client

   ```shell
   make juicefs.linux
   ```

## Uninstall

The JuiceFS client has only one binary file, so it can be easily deleted once you find the location of the program. For example, to uninstall the client that is installed on the Linux system as described above, you only need to execute the following command:

```shell
sudo rm /usr/local/bin/juicefs
```

You can also check where the program is located by using `which` command.

```shell
which juicefs
```

The path returned by the command is the location where the JuiceFS client is installed on your system. The uninstallation of the JuiceFS client on other operating systems follows the same way.
