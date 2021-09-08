# macOS 系统使用 JuiceFS

## 1. 安装依赖工具

JuiceFS 支持在 macOS 系统中创建和挂载文件系统。但你需要先安装 [macFUSE](https://osxfuse.github.io/) 才能在 macOS 系统中挂载 JuiceFS 文件系统。

> [macFUSE](https://github.com/osxfuse/osxfuse) 是一个开源的文件系统增强工具，它让 macOS 可以挂载第三方的文件系统，使得 JuiceFS 客户端可以将文件系统挂载到 macOS 系统中使用。

## 2. macOS 上安装 JuiceFS

您可以参考以下三种方法在 macOS 系统上安装 JuiceFS 客户端。

### 通过 Homebrew 安装

第一步，添加 Tap：

```bash
$ brew tap juicedata/homebrew-tap
```

第二步，安装客户端：

```bash
$ brew install juicefs
```

### 手动安装

你可以在 [这里下载](https://github.com/juicedata/juicefs/releases/latest) 最新的预编译的二进制程序，下载文件名包含 `darwin-amd64` 的压缩包，例如：

```shell
$ JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
$ curl -OL "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-darwin-amd64.tar.gz"
```

解压并安装：

```shell
$ tar -zxf "juicefs-${JFS_LATEST_TAG}-darwin-amd64.tar.gz"
$ sudo install juicefs /usr/local/bin
```

> **注意**：Apple M1 芯片也可以直接使用 `darwin-amd64` 架构的预编译版本，macOS 会自动通过 Rosetta 2 转译。如果希望使用 M1 原生版本，可以自行编译安装。

### 编译安装

你也可以从源代码手动编译 JuiceFS 客户端，[查看详情](client_compile_and_upgrade.md)。

## 3. 挂载 JuiceFS 文件系统

这里假设你已经准备好了对象存储、Redis 数据库，并且已经创建好了 JuiceFS 文件系统。如果你还没有准备好这些必须的资源，请参考 [快速上手指南](quick_start_guide.md)。

这里，我们假设在当前局域网中 IP 地址为 `192.168.1.8` 的 Linux 主机上部署了 MinIO 对象存储和 Redis 数据库，然后执行了以下命令，创建了名为 `music` 的 JuiceFS 文件系统。

```shell
$ juicefs format --storage minio --bucket http://192.168.1.8:9000/music --access-key minioadmin --secret-key minioadmin redis://192.168.1.8:6379/1 music
```

执行以下命令，将 `music` 文件系统挂载到当前用户家目录下的 `~/music` 文件夹。

```shell
$ juicefs mount redis://192.168.1.8:6379/1 ~/music
```

> **提示**：在本指南中，Windows 和 macOS 挂载的是同一个文件系统，JuiceFS 支持上千台客户端同时挂载同一个文件系统，提供便捷的海量数据共享能力。

## 4. 开机自动挂载 JuiceFS

在 `~/Library/LaunchAgents` 下创建一个名为 `io.juicefs.<NAME>.plist` 的文件。将 `<NAME>` 替换为 JuiceFS 卷的名称。将以下内容添加到文件中（同样，用适当的值替换 `NAME`、`PATH-TO-JUICEFS`、`META-URL` 和 `MOUNTPOINT`）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
        <key>Label</key>
        <string>io.juicefs.NAME</string>
        <key>ProgramArguments</key>
        <array>
                <string>PATH-TO-JUICEFS</string>
                <string>mount</string>
                <string>META-URL</string>
                <string>MOUNTPOINT</string>
        </array>
        <key>RunAtLoad</key>
        <true/>
</dict>
</plist>
```

使用以下命令加载上一步创建的文件，测试加载是否成功。**请确保 Redis 服务器已经在运行。**

```bash
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl start ~/Library/LaunchAgents/io.juicefs.<NAME>
$ ls <MOUNTPOINT>
```

如果挂载失败，您可以将以下配置添加到 `io.juicefs.<NAME>.plist` 文件中以进行调试：

```xml
        <key>StandardOutPath</key>
        <string>/tmp/juicefs.out</string>
        <key>StandardErrorPath</key>
        <string>/tmp/juicefs.err</string>
```

使用以下命令重新加载最新配置并检查输出：

```bash
$ launchctl unload ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ cat /tmp/juicefs.out
$ cat /tmp/juicefs.err
```

如果你通过 Homebrew 安装 Redis 服务器，则可以使用以下命令在开机时启动它：

```bash
$ brew services start redis
```

然后在 `io.juicefs.<NAME>.plist` 文件中添加以下配置以确保 Redis 服务器已加载：

```xml
        <key>KeepAlive</key>
        <dict>
                <key>OtherJobEnabled</key>
                <string>homebrew.mxcl.redis</string>
        </dict>
```

## 5. 卸载文件系统

执行 `umount` 子命令卸载 JuiceFS 文件系统：

```shell
$ juicefs umount ~/music
```

> **提示**：执行 `juicefs umount -h` 命令，可以获取卸载命令的详细帮助信息。

### 卸载失败

如果执行命令后，文件系统卸载失败，提示 `Device or resource busy`：

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount ~/music: Device or resource busy
exit status 1
```

发生这种情况，可能是因为某些程序正在读写文件系统中的文件。为了确保数据安全，你应该首先排查是哪些程序正在与文件系统中的文件进行交互（例如通过 `lsof` 命令），并尝试结束他们之间的交互动作，然后再重新执行卸载命令。

> **风险提示**：以下内容包含的命令可能会导致文件损坏、丢失，请务必谨慎操作！

当然，在你能够确保数据安全的前提下，也可以在卸载命令中添加 `--force` 或 `-f` 参数，强制卸载文件系统：

```shell
$ juicefs umount --force ~/music
```

## 你可能需要

- [Linux 系统使用 JuiceFS](juicefs_on_linux.md)
- [Windows 系统使用 JuiceFS](juicefs_on_windows.md)
