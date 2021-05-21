# JuiceFS on macOS

## 1. Requirement

JuiceFS supports creating and mounting file systems in macOS. But you need to install [macFUSE](https://osxfuse.github.io/) before you can mount the JuiceFS file system.

> [macFUSE](https://github.com/osxfuse/osxfuse) is an open source file system enhancement tool that allows macOS to mount third-party file systems, allowing JuiceFS client to mount the file system to macOS.

## 2. Install JuiceFS on macOS 

You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest), download the compressed package containing `darwin-amd64` in the file name, for example:

```shell
$ curl -fsSL https://github.com/juicedata/juicefs/releases/download/v0.12.1/juicefs-0.12.1-darwin-amd64.tar.gz -o juicefs-0.12.1-darwin-amd64.tar.gz
```

Unzip and install:

```shell
$ tar -zxf juicefs-0.12.1-darwin-amd64.tar.gz
$ sudo install juicefs /usr/local/bin
```

## 3. Mount JuiceFS file system

It is assumed that you have prepared object storage, Redis database, and created JuiceFS file system. If you have not prepared these necessary resources, please refer to the previous section of [Quick Start](#Quick Start).

Suppose that the MinIO object storage and Redis database are deployed on the Linux host with the IP address of `192.168.1.8` in LAN, and then the following commands are executed to create a JuiceFS file system named `music`.

```shell
$ juicefs format --storage minio --bucket http://192.168.1.8:9000/music --access-key minioadmin --secret-key minioadmin redis://192.168.1.8:6379/1 music
```

Execute the following command to mount the `music` file system to the `~/music` folder in the current user's home directory.

```shell
$ juicefs mount redis://192.168.1.8:6379/1 ~/music
```

> **Tip**: In this guide, Windows and macOS mount the same file system. JuiceFS supports thousands of clients to mount the same file system at the same time, providing convenient mass data sharing capabilities.

## 4. Automatically mount JuiceFS on boot

Create a file named `io.juicefs.<NAME>.plist` under `~/Library/LaunchAgents`. Replace `<NAME>` with JuiceFS volume name. Add following contents to the file (again, replace `NAME`, `PATH-TO-JUICEFS`, `REDIS-URL` and `MOUNTPOINT` with appropriate value):

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
                <string>REDIS-URL</string>
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

## 5. Unmount a JuiceFS