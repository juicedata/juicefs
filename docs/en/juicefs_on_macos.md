# JuiceFS on macOS

## 1. Requirement

JuiceFS supports creating and mounting file systems in macOS. But you need to install [macFUSE](https://osxfuse.github.io/) before you can mount the JuiceFS file system.

> [macFUSE](https://github.com/osxfuse/osxfuse) is an open source file system enhancement tool that allows macOS to mount third-party file systems, allowing JuiceFS client to mount the file system to macOS.

## 2. Install JuiceFS on macOS

There are three ways to install the JuiceFS client on macOS.

### Homebrew

First, add the Tap:

```bash
$ brew tap juicedata/homebrew-tap
```

Then, install the client:

```bash
$ brew install juicefs
```

### Pre-compiled version

You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest), download the compressed package containing `darwin-amd64` in the file name, for example:

```shell
$ JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
$ curl -OL "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-darwin-amd64.tar.gz"
```

Unzip and install:

```shell
$ tar -zxf "juicefs-${JFS_LATEST_TAG}-darwin-amd64.tar.gz"
$ sudo install juicefs /usr/local/bin
```

> **Note**: Apple M1 chip can directly use the pre-compiled version of `darwin-amd64`, and macOS will automatically translate it through Rosetta 2. If you want to use the native version for M1, please compile it yourself.

### Compile from source

You can also build the JuiceFS client manually from the source code. [Learn more](client_compile_and_upgrade.md)

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

## 5. Unmount a JuiceFS

Execute the `umount` subcommand to unmount the JuiceFS file system:

```shell
$ juicefs umount ~/music
```

> **Prompt**: Execute the `juicefs umount -h` command to obtain detailed help information for the unmount command.

### Unmount failed

If a file system fails to be unmounted after executing the command, it will prompt `Device or resource busy`:

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount ~/music: Device or resource busy
exit status 1
```

This can happen because some programs are reading and writing files in the file system. To ensure data security, you should first check which programs are interacting with files in the file system (e.g. through the `lsof` command), and try to end the interaction between them, and then execute the uninstall command again.

> **Risk Tips**: The commands contained in the following content may cause files damage or loss, please be cautious!

Of course, you can also add the `--force` or `-f` parameter to the unmount command to force the file system to be unmounted, but you have to bear the possible catastrophic consequences:

```shell
$ juicefs umount --force ~/music
```

## Go further

- [JuiceFS on Linux](juicefs_on_linux.md)
- [JuiceFS on Windows](juicefs_on_windows.md)
