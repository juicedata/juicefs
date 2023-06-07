---
title: 启动时自动挂载 JuiceFS
sidebar_position: 3
slug: /mount_juicefs_at_boot_time
---

在确认挂载成功，可以正常使用以后，可以参考本节内容设置开机自动挂载。

## Linux

从 JuiceFS v1.1.0 开始，挂载命令的 `--update-fstab` 选项能自动帮你设置好开机自动挂载：

```bash
$ sudo juicefs mount --update-fstab --max-uploads=50 --writeback --cache-size 204800 <META-URL> <MOUNTPOINT>
$ grep <MOUNTPOINT> /etc/fstab
<META-URL> <MOUNTPOINT> juicefs _netdev,max-uploads=50,writeback,cache-size=204800 0 0
$ ls -l /sbin/mount.juicefs
lrwxrwxrwx 1 root root 29 Aug 11 16:43 /sbin/mount.juicefs -> /usr/local/bin/juicefs
```

如果你有意自行控制，请注意：

* 需要创建一个从 `/sbin/mount.juicefs` 到 JuiceFS 可执行文件的软链接，比如 `ln -s /usr/local/bin/juicefs /sbin/mount.juicefs`。
* 挂载命令所包含的各种选项，也需要在 fstab options 列加以声明，注意去掉 `-` 前缀，并将选项取值以 `=` 连接，举例说明：

  ```bash
  $ sudo juicefs mount --update-fstab --max-uploads=50 --writeback --cache-size 204800 -o max_read=99 <META-URL> /jfs
  # -o 是 FUSE options，在 fstab 中需特殊对待
  $ grep jfs /etc/fstab
  redis://localhost:6379/1  /jfs juicefs _netdev,max-uploads=50,max_read=99,writeback,cache-size=204800 0 0
  ```

:::tip 提示
默认情况下，CentOS 6 在启动后不会自动挂载网络文件系统，你可以使用下面的命令开启它：

```bash
sudo chkconfig --add netfs
```

:::

### 使用 systemd.mount 实现自动挂载

基于安全考虑，JuiceFS 将命令行中的一些选项隐藏在环境变量中，所以像数据库访问密码、S3 访问密钥和密钥等设置不能直接应用于 `/etc/fstab` 文件。在这种情况下，你可以使用 systemd 来挂载 JuiceFS 实例。

以下是如何设置 systemd 配置文件的步骤：

1. 创建文件 `/etc/systemd/system/juicefs.mount`，并添加以下内容：

    ```conf
    [Unit]
    Description=Juicefs
    Before=docker.service

    [Mount]
    Environment="ALICLOUD_ACCESS_KEY_ID=mykey" "ALICLOUD_ACCESS_KEY_SECRET=mysecret" "META_PASSWORD=mypassword"
    What=mysql://juicefs@(mysql.host:3306)/juicefs
    Where=/juicefs
    Type=juicefs
    Options=_netdev,allow_other,writeback_cache

    [Install]
    WantedBy=remote-fs.target
    WantedBy=multi-user.target
    ```

    你可以根据需要更改环境变量、挂载选项等。

2. 使用以下命令启用和启动 JuiceFS 挂载：

    ```sh
    ln -s /usr/local/bin/juicefs /sbin/mount.juicefs
    systemctl enable juicefs.mount
    systemctl start juicefs.mount
    ```

完成这些步骤后，就可以访问 `/juicefs` 目录来存取文件了。

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
