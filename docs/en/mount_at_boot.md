# Mount JuiceFS at Boot

This is a guide about how to mount JuiceFS automatically at boot.

## Linux

Copy `juicefs` as `/sbin/mount.juicefs`, then edit `/etc/fstab` with following line:

```
<META-URL>    <MOUNTPOINT>       juicefs     _netdev[,<MOUNT-OPTIONS>]     0  0
```

The format of `<META-URL>` is `redis://<user>:<password>@<host>:<port>/<db>`, e.g. `redis://localhost:6379/1`. And replace `<MOUNTPOINT>` with specific path you wanna mount JuiceFS to, e.g. `/jfs`. If you need set [mount options](command_reference.md#juicefs-mount), replace `[,<MOUNT-OPTIONS>]` with comma separated options list. The following line is an example:

```
redis://localhost:6379/1    /jfs       juicefs     _netdev,max-uploads=50,writeback,cache-size=2048     0  0
```

**Note: By default, CentOS 6 will NOT mount network file system after boot, run following command to enable it:**

```bash
$ sudo chkconfig --add netfs
```

## macOS

Create a file named `io.juicefs.<NAME>.plist` under `~/Library/LaunchAgents`. Replace `<NAME>` with JuiceFS volume name. Add following contents to the file (again, replace `NAME`, `PATH-TO-JUICEFS`, `META-URL` and `MOUNTPOINT` with appropriate value):

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

Use following commands to load the file created in the previous step and test whether the loading is successful. **Please ensure Redis server is already running.**

```bash
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl start ~/Library/LaunchAgents/io.juicefs.<NAME>
$ ls <MOUNTPOINT>
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
$ launchctl unload ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ launchctl load ~/Library/LaunchAgents/io.juicefs.<NAME>.plist
$ cat /tmp/juicefs.out
$ cat /tmp/juicefs.err
```

If you install Redis server by Homebrew, you could use following command to start it at boot:

```bash
$ brew services start redis
```

Then add following configuration to `io.juicefs.<NAME>.plist` file for ensure Redis server is loaded:

```xml
        <key>KeepAlive</key>
        <dict>
                <key>OtherJobEnabled</key>
                <string>homebrew.mxcl.redis</string>
        </dict>
```

