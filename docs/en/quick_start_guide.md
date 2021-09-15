# JuiceFS Quick Start Guide

To create a JuiceFS file system, you need the following 3 preparations:

1. Redis database for metadata storage
2. Object storage is used to store data blocks
3. JuiceFS Client

## 1. Redis Database

You can easily buy cloud Redis databases in various configurations on the cloud computing platform, but if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a Redis database instance on your local computer:

```shell
$ sudo docker run -d --name redis \
	-v redis-data:/data \
	-p 6379:6379 \
	--restart unless-stopped \
	redis redis-server --appendonly yes
```

After the container is successfully created, you can use `redis://127.0.0.1:6379` to access the Redis database.

> **Note**: The above command persists Redis data in the `redis-data` data volume of docker, and you can modify the storage location of data persistence as needed.

> **Security Tips**: The Redis database instance created by the above command does not enable authentication and exposes the host's `6379` port. If you want to access this database via the Internet, it is strongly recommended to refer to [Redis official documentation](https: //redis.io/topics/security) Enable protected mode.

For more information about Redis database, [click here to view](databases_for_metadata.md#Redis).

## 2. Object Storage

Like Redis databases, almost all public cloud computing platforms provide object storage services. Because JuiceFS supports object storage services on almost all platforms, you can choose freely according to your personal preferences. You can check our [Object Storage Support List and Setting Guide](how_to_setup_object_storage.md), which lists all the object storage services currently supported by JuiceFS and how to use them.

Of course, if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a MinIO object storage instance on your local computer:

```shell
$ sudo docker run -d --name minio \
    -p 9000:9000 \
    -p 9900:9900 \
    -v $PWD/minio-data:/data \
    --restart unless-stopped \
    minio/minio server /data --console-address ":9900"
```

Then, access the service:

- **MinIO Web Console**：http://127.0.0.1:9900
- **MinIO API**：http://127.0.0.1:9000

The initial Access Key and Secret Key of the root user are both `minioadmin`.

After the container is successfully created, use `http://127.0.0.1:9000` to access the MinIO management interface. The initial Access Key and Secret Key of the root user are both `minioadmin`.

> **Note**: The latest MinIO includes a new web console, the above command sets and maps port `9900` through  `--console-address ":9900"`  option. In addtion, it maps the data path in the MinIO container to the `minio-data` folder in the current directory. You can modify these options as needed.

## 3. JuiceFS Client

JuiceFS supports Linux, Windows, and MacOS. You can download the latest pre-compiled binary program from [here](https://github.com/juicedata/juicefs/releases/latest). Please refer to the actual system and Select the corresponding version of the architecture.

Take the x86-based Linux system as an example, download the compressed package containing `linux-amd64` in the file name:

```shell
$ JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v')
$ wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
```

Unzip and install:

```shell
$ tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz"
$ sudo install juicefs /usr/local/bin
```

> **Note**: You can also build the JuiceFS client manually from the source code. [Learn more](client_compile_and_upgrade.md)

## 4. Create JuiceFS file system

When creating a JuiceFS file system, you need to specify both the Redis database used to store metadata and the object storage used to store actual data.

The following command will create a JuiceFS file system named `pics`, use the database `1` in Redis to store metadata, and use the `pics` bucket created in MinIO to store actual data:

```shell
$ juicefs format \
	--storage minio \
	--bucket http://127.0.0.1:9000/pics \
	--access-key minioadmin \
	--secret-key minioadmin \
	redis://127.0.0.1:6379/1 \
	pics
```

After executing the command, you will see output similar to the following, indicating that the JuiceFS file system was created successfully.

```shell
2021/04/29 23:01:18.352256 juicefs[34223] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:01:18.354252 juicefs[34223] <INFO>: Ping redis: 132.185µs
2021/04/29 23:01:18.354758 juicefs[34223] <INFO>: Data use minio://127.0.0.1:9000/pics/pics/
2021/04/29 23:01:18.361674 juicefs[34223] <INFO>: Volume is formatted as {Name:pics UUID:9c0fab76-efd0-43fd-a81e-ae0916e2fc90 Storage:minio Bucket:http://127.0.0.1:9000/pics AccessKey:minioadmin SecretKey:removed BlockSize:4096 Compression:none Partitions:0 EncryptKey:}
```

> **Note**: You can create as many JuiceFS file systems as you need. But it should be noted that only one file system can be created in each Redis database. For example, when you want to create another file system named `memory`, you have to use another database in Redis, such as No.2, which is `redis://127.0.0.1:6379/2`.

> **Note**: If you don't specify `--storage` option, the JuiceFS client will use the local disk as data storage. When using local storage, JuiceFS can only be used on a local stand-alone machine and cannot be mounted by other clients in the network. [Click here](how_to_setup_object_storage.md#local) for details.

## 5. Mount JuiceFS file system

After the JuiceFS file system is created, you can mount it on the operating system and use it. The following command mounts the `pics` file system to the `/mnt/jfs` directory.

```shell
$ sudo juicefs mount -d redis://127.0.0.1:6379/1 /mnt/jfs
```

> **Note**: When mounting the JuiceFS file system, there is no need to explicitly specify the name of the file system, just fill in the correct Redis server address and database number.

After executing the command, you will see output similar to the following, indicating that the JuiceFS file system has been successfully mounted on the system.

```shell
2021/04/29 23:22:25.838419 juicefs[37999] <INFO>: Meta address: redis://127.0.0.1:6379/1
2021/04/29 23:22:25.839184 juicefs[37999] <INFO>: Ping redis: 67.625µs
2021/04/29 23:22:25.839399 juicefs[37999] <INFO>: Data use minio://127.0.0.1:9000/pics/pics/
2021/04/29 23:22:25.839554 juicefs[37999] <INFO>: Cache: /var/jfsCache/9c0fab76-efd0-43fd-a81e-ae0916e2fc90 capacity: 1024 MB
2021/04/29 23:22:26.340509 juicefs[37999] <INFO>: OK, pics is ready at /mnt/jfs
```

After the mounting is complete, you can access files in the `/mnt/jfs` directory. You can execute the `df` command to view the JuiceFS file system's mounting status:

```shell
$ df -Th
Filesystem     Type          Size    Used   Avail    Use%    Mounted on
JuiceFS:pics   fuse.juicefs  1.0P     64K    1.0P     1%     /mnt/jfs
```

> **Note**: By default, the cache of JuiceFS is located in the `/var/jfsCache` directory. In order to obtain the read and write permissions of this directory, the `sudo` command is used here to mount the JuiceFS file system with administrator privileges. When ordinary users read and write `/mnt/jfs`, please assign them the appropriate permissions.

## 6. Automatically mount JuiceFS on boot

Rename the `juicefs` client to `mount.juicefs` and copy it to the `/sbin/` directory:

```shell
$ sudo cp /usr/local/bin/juicefs /sbin/mount.juicefs
```

> **Note**: Before executing the above command, we assume that the `juicefs` client program is already in the `/usr/local/bin` directory. You can also unzip a copy of the `juicefs` program directly from the downloaded compression package, rename it according to the above requirements, and copy it to the `/sbin/` directory.

Edit the `/etc/fstab` configuration file, start a new line, and add a record according to the following format:

```
<META-URL> <MOUNTPOINT> juicefs _netdev[,<MOUNT-OPTIONS>] 0 0
```

- Please replace `<META-URL>` with the actual Redis database address in the format of `redis://<user>:<password>@<host>:<port>/<db>`, for example: `redis ://localhost:6379/1`.
- Please replace `<MOUNTPOINT>` with the actual mount point of the file system, for example: `/jfs`.
- If necessary, please replace `[,<MOUNT-OPTIONS>]` with the actual [mount option](command_reference.md#juicefs-mount) to be set, and multiple options are separated by commas.

For example:

```
redis://localhost:6379/1 /jfs juicefs _netdev,max-uploads=50,writeback,cache-size=2048 0 0
```

> **Note**: By default, CentOS 6 will not mount the network file system when the system starts. You need to execute the command to enable the automatic mounting support of the network file system:

```bash
$ sudo chkconfig --add netfs
```

## 7. Unmount JuiceFS

If you need to unmount a JuiceFS file system, you can first execute the `df` command to view the information of the mounted file systems:

```shell
$ sudo df -Th

File system type capacity used usable used% mount point
...
JuiceFS:pics fuse.juicefs 1.0P 1.1G 1.0P 1% /mnt/jfs
```

You can see that the mount point of the file system `pics` is `/mnt/jfs`, execute the `umount` subcommand:

```shell
$ sudo juicefs umount /mnt/jfs
```

> **Prompt**: Execute the `juicefs umount -h` command to obtain detailed help information for the unmount command.

### Unmount failed

If a file system fails to be unmounted after executing the command, it will prompt `Device or resource busy`:

```shell
2021-05-09 22:42:55.757097 I | fusermount: failed to unmount /mnt/jfs: Device or resource busy
exit status 1
```

This can happen because some programs are reading and writing files in the file system. To ensure data security, you should first check which programs are interacting with files in the file system (e.g. through the `lsof` command), and try to end the interaction between them, and then execute the uninstall command again.

> **Risk Tips**: The commands contained in the following content may cause files damage or loss, please be cautious!

Of course, you can also add the `--force` or `-f` parameter to the unmount command to force the file system to be unmounted, but you have to bear the possible catastrophic consequences:

```shell
$ sudo juicefs umount --force /mnt/jfs
```

You can also use the `fusermount` command to unmount the file system:

```shell
$ sudo fusermount -u /mnt/jfs
```

## Go further

- [JuiceFS on macOS](juicefs_on_macos.md)
- [JuiceFS on Windows](juicefs_on_windows.md)
