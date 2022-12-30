---
title: 启动时自动挂载 JuiceFS
sidebar_position: 2
slug: /mount_juicefs_at_boot_time
---

## Linux

拷贝 `juicefs` 为 `/sbin/mount.juicefs`，然后按照下面的格式添加一行到 `/etc/fstab`：

```
<META-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

`<META-URL>` 的格式请参考[「如何设置元数据引擎」](how_to_set_up_metadata_engine.md)文档，比如 `redis://localhost:6379/1`。然后用你希望 JuiceFS 挂载的路径替换 `<MOUNTPOINT>` ，比如 `/jfs`。如果你想要设置[挂载选项](../reference/command_reference.md#mount)，用逗号分隔选项列表并替换 `[,<MOUNT-OPTIONS>]` 。下面是一个示例：

```
redis://localhost:6379/1    /jfs       juicefs     _netdev,max-uploads=50,writeback,cache-size=204800     0  0
```

:::tip 提示
默认情况下，CentOS 6 在启动后不会自动挂载网络文件系统，你可以使用下面的命令开启它：

```bash
sudo chkconfig --add netfs
```

:::

## macOS

在 `~/Library/LaunchAgents` 下创建名为 `io.juicefs.<NAME>.plist` 的文件。替换 `<NAME>` 为 JuiceFS 文件系统的名字。添加如下内容到文件中（再次替换 `NAME`、`PATH-TO-JUICEFS`、`META-URL`、`MOUNTPOINT` 和 `MOUNT-OPTIONS` 为适当的值）：

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
                <string>MOUNT-OPTIONS</string>
        </array>
        <key>RunAtLoad</key>
        <true/>
</dict>
</plist>
```

:::tip 提示
如果有多个挂载选项可以分为多行依次设置，例如：

```xml
                <string>--max-uploads</string>
                <string>50</string>
                <string>--cache-size</string>
                <string>204800</string>
```

:::

使用以下命令加载上一步创建的文件，并测试加载是否成功。**请确保元数据引擎已正常运行。**

```bash
launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
launchctl start ~/Library/LaunchAgents/io.juicefs.<NAME>
ls <MOUNTPOINT>
```

如果挂载失败，可以将以下配置添加到 `io.juicefs.<NAME>.plist` 文件来调试：

```xml
        <key>StandardOutPath</key>
        <string>/tmp/juicefs.out</string>
        <key>StandardErrorPath</key>
        <string>/tmp/juicefs.err</string>
```

使用以下命令重新加载最新的配置并检查输出：

```bash
launchctl unload ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
cat /tmp/juicefs.out
cat /tmp/juicefs.err
```

如果你是使用 Homebrew 安装的 Redis 服务，你可以使用以下命令让其在机器启动时启动它：

```bash
brew services start redis
```

然后添加以下配置到 `io.juicefs.<NAME>.plist` 文件确保 Redis 服务已经启动：

```xml
        <key>KeepAlive</key>
        <dict>
                <key>OtherJobEnabled</key>
                <string>homebrew.mxcl.redis</string>
        </dict>
```
