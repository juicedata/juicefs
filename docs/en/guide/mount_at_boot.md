---
title: Mount JuiceFS at Boot Time
sidebar_position: 2
slug: /mount_juicefs_at_boot_time
---

## Linux

Copy `juicefs` as `/sbin/mount.juicefs`, then edit `/etc/fstab` with following line:

```
<META-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

For the format of `<META-URL>`, please refer to the ["How to Set Up Metadata Engine"](how_to_set_up_metadata_engine.md) document, such as `redis://localhost:6379/1`. Then replace `<MOUNTPOINT>` with the path you want JuiceFS to mount, e.g. `/jfs`. If you want to set [mount options](../reference/command_reference.md#mount), separate the options list with commas and replace `[,<MOUNT-OPTIONS>]`. Here is an example:

```
redis://localhost:6379/1    /jfs       juicefs     _netdev,max-uploads=50,writeback,cache-size=204800     0  0
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
