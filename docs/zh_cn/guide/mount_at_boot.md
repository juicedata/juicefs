---
sidebar_label: 如何在启动时自动挂载 JuiceFS
sidebar_position: 2
slug: /how_to_mount_at_boot
---

# 在启动时挂载 JuiceFS

这是一篇关于如何在启动时自动挂载 JuiceFS 的指南。

## Linux

拷贝 `juicefs` 为 `/sbin/mount.juicefs`, 然后按照下面的格式添加一行到 `/etc/fstab` :

```
<META-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

`<META-URL>` 的格式是 `redis://<user>:<password>@<host>:<port>/<db>`, 比如 `redis://localhost:6379/1`。 然后用你希望 JuiceFS 挂载的路径替换 `<MOUNTPOINT>` , 比如 `/jfs`。 如果你想要设置[挂载参数](https://juicefs.com/docs/zh/community/fuse_mount_options) ，用逗号分隔参数列表替换 `[,<MOUNT-OPTIONS>]` 。 下面是一个示例:

```
redis://localhost:6379/1    /jfs       juicefs     _netdev,max-uploads=50,writeback,cache-size=2048     0  0
```

**提示: 默认情况下, CentOS 6 在启动后不会自动挂载网络文件系统，你可以使用下面的命令开启该它：**

```bash
sudo chkconfig --add netfs
```

## macOS

在 `~/Library/LaunchAgents` 下创建名为 `io.juicefs.<NAME>.plist` 的文件。替换 `<NAME>` 为 JuiceFS 卷的名字。添加如下内容到文件中（再次替换 `NAME`, `PATH-TO-JUICEFS`, `META-URL` 和 `MOUNTPOINT` 为适当的值）：

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

使用以下命令加载上一步创建的文件，并测试加载是否成功。**请确保 Redis 服务已经在运行**

```bash
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl start ~/Library/LaunchAgents/io.juicefs.<NAME>
$ ls <MOUNTPOINT>
```

如果挂载失败，可以将以下配置添加到 `io.juicefs.<NAME>.plist` 文件来调试:

```xml
        <key>StandardOutPath</key>
        <string>/tmp/juicefs.out</string>
        <key>StandardErrorPath</key>
        <string>/tmp/juicefs.err</string>
```

使用以下命令重新加载最新的配置并检查输出:

```bash
$ launchctl unload ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ cat /tmp/juicefs.out
$ cat /tmp/juicefs.err
```

如果你是使用 Homebrew 安装的 Redis 服务，你可以使用以下命令让其在机器启动时启动它:

```bash
$ brew services start redis
```

然后添加以下配置到 `io.juicefs.<NAME>.plist` 文件确保 Redis 服务已经启动:

```xml
        <key>KeepAlive</key>
        <dict>
                <key>OtherJobEnabled</key>
                <string>homebrew.mxcl.redis</string>
        </dict>
```

