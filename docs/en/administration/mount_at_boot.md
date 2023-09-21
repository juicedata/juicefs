---
title: Mount JuiceFS at Boot Time
sidebar_position: 3
slug: /mount_juicefs_at_boot_time
---

After JuiceFS has been successfully mounted, follow this guide to set up auto-mount on boot.

## Linux

Starting with JuiceFS v1.1.0, the `--update-fstab` option of the mount command will automatically help you set up mount at boot:

```bash
$ sudo juicefs mount --update-fstab --max-uploads=50 --writeback --cache-size 204800 <META-URL> <MOUNTPOINT>
$ grep <MOUNTPOINT> /etc/fstab
<META-URL> <MOUNTPOINT> juicefs _netdev,max-uploads=50,writeback,cache-size=204800 0 0
$ ls -l /sbin/mount.juicefs
lrwxrwxrwx 1 root root 29 Aug 11 16:43 /sbin/mount.juicefs -> /usr/local/bin/juicefs
```

If you'd like to control this process by hand, note that:

* A symlink needs to be created from `/sbin/mount.juicefs` to the JuiceFS executable, e.g. `ln -s /usr/local/bin/juicefs /sbin/mount.juicefs`.
* All mount options must also be included in the fstab options to take effect. Remember to remove the prefixing hyphen(s), and add their values with `=`, for example:

  ```bash
  $ sudo juicefs mount --update-fstab --max-uploads=50 --writeback --cache-size 204800 -o max_read=99 <META-URL> /jfs
  # -o stands for FUSE options, and is handled differently
  $ grep jfs /etc/fstab
  redis://localhost:6379/1  /jfs juicefs _netdev,max-uploads=50,max_read=99,writeback,cache-size=204800 0 0
  ```

:::tip
By default, CentOS 6 will NOT mount network file system after boot, run following command to enable it:

```bash
sudo chkconfig --add netfs
```

:::

### Automating Mounting with systemd.mount

If you're using JuiceFS and need to apply settings like database access password, S3 access key, and secret key, which are hidden from the command line using environment variables for security reason, it may not be easy to configure them in the `/etc/fstab` file. In such cases, you can utilize systemd to mount your JuiceFS instance.

Here's how you can set up your systemd configuration file:

1. Create the file `/etc/systemd/system/juicefs.mount` and add the following content:

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

    Feel free to modify the options and environments according to your needs.

2. Enable and start the JuiceFS mount using the following commands:

    ```sh
    ln -s /usr/local/bin/juicefs /sbin/mount.juicefs
    systemctl enable juicefs.mount
    systemctl start juicefs.mount
    ```

After completing these steps, you will be able to access `/juicefs` and store your files there.

## macOS

Create a file named `io.juicefs.<NAME>.plist` under `~/Library/LaunchAgents`. Replace `<NAME>` with JuiceFS file system name. Add following contents to the file (again, replace `NAME`, `PATH-TO-JUICEFS`, `META-URL`, `MOUNTPOINT` and `MOUNT-OPTIONS` with appropriate value):

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

:::tip
If there are multiple mount options, they can be set in multiple lines, for example:

```xml
                <string>--max-uploads</string>
                <string>50</string>
                <string>--cache-size</string>
                <string>204800</string>
```

:::

Use following commands to load the file created in the previous step and test whether the loading is successful. **Please make sure the metadata engine is running properly.**

```bash
launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
launchctl start ~/Library/LaunchAgents/io.juicefs.<NAME>
ls <MOUNTPOINT>
```

If mount failed, you can add following configuration to `io.juicefs.<NAME>.plist` file for debug purpose:

```xml
        <key>StandardOutPath</key>
        <string>/tmp/juicefs.out</string>
        <key>StandardErrorPath</key>
        <string>/tmp/juicefs.err</string>
```

Use following commands to reload the latest configuration and inspect the output:

```bash
launchctl unload ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
cat /tmp/juicefs.out
cat /tmp/juicefs.err
```

If you install Redis server by Homebrew, you could use following command to start it at boot:

```bash
brew services start redis
```

Then add following configuration to `io.juicefs.<NAME>.plist` file for ensure Redis server is loaded:

```xml
        <key>KeepAlive</key>
        <dict>
                <key>OtherJobEnabled</key>
                <string>homebrew.mxcl.redis</string>
        </dict>
```
