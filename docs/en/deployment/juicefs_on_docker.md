---
title: Use JuiceFS on Docker
sidebar_position: 3
slug: /juicefs_on_docker
---

There are several common ways to use JuiceFS as Docker persistent storage.

## 1. Volume Mapping {#volume-mapping}

Volume mapping maps the directories in the JuiceFS mount point to the Docker container. For example, assuming a JuiceFS file system is mounted to the `/mnt/jfs` directory, you can map this file system when creating a Docker container as follows:

```shell
sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

By default, only the user who mounts the JuiceFS file system has access permissions. To make a file system mappable for Docker containers created by others, you need to enable FUSE option `user_allow_other` first, and then re-mount the file system with option `-o allow_other`.

> **Note**: JuiceFS file system mounted with root privilege has already enabled the `allow_other` option. Thus, you don't need to set it manually.

### FUSE Settings

By default, the `allow_other` option is only available for users with root privilege. In order to allow other users to use this mount option, the FUSE configuration file needs to be modified.

### Change the configuration file

Edit the configuration file of FUSE, usually `/etc/fuse.conf`:

```sh
sudo nano /etc/fuse.conf
```

First, uncomment the line `# user_allow_other` by deleting the`#` symbol. Your configuration file should look like the following after the modification.

<!-- autocorrect: false -->
```conf
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```
<!-- autocorrect: true -->

#### Re-mount JuiceFS

Run the following command to re-mount the JuiceFS file system with `allow_other` option.

```sh
juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

## 2. Docker Volume Plugin {#docker-volume-plugin}

JuiceFS provides [volume plugin](https://docs.docker.com/engine/extend) for Docker environments to create storage volumes on JuiceFS as if they were local disks.

### Dependencies

Since JuiceFS mount depends on FUSE, please make sure that the FUSE driver is already installed on the host, in the case of Debian/Ubuntu.

```shell
sudo apt-get -y install fuse
```

### Installation

Install the Volume Plugin.

```shell
sudo docker plugin install juicedata/juicefs --alias juicefs
```

### Usage

The process of creating a storage volume using the JuiceFS Docker Volume Plugin is similar to creating and mounting a file system in a Docker container using the JuiceFS client, so you need to provide information about the database and object storage so that the volume plugin can perform the appropriate operations.

:::tip
Since SQLite is a standalone database, the volume plugin container cannot read the database created by the host. Therefore, when using the Docker volume plugin, you can only use network based databases such as Redis, MySQL, etc.
:::

#### Create a Storage Volume

Please replace `<VOLUME_NAME>`, `<META_URL>`, `<STORAGE_TYPE>`, `<BUCKET_NAME>`, `<ACCESS_KEY>`, `<SECRET_KEY>` in the following commands with your own.

```shell
sudo docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    -o storage=<STORAGE_TYPE> \
    -o bucket=<BUCKET_NAME> \
    -o access-key=<ACCESS_KEY> \
    -o secret-key=<SECRET_KEY> \
    jfsvolume
```

:::tip
You can create multiple file systems on the same object storage bucket by specifying different `<VOLUME_NAME>` volume names and `<META_URL>` database address.
:::

To use Docker volume plugin with existing JuiceFS volumes, simply specify the file system name and database address, e.g.

```shell
sudo docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    jfsvolume
```

#### Using Storage Volumes

Mounted volumes when creating containers.

```shell
sudo docker run -it -v jfsvolume:/opt busybox ls /opt
```

#### Delete a storage volume

```shell
sudo docker volume rm jfsvolume
```

### Upgrade and Uninstall Volume Plugin

Plugins need to be deactivated before upgrading or uninstalling the Docker volume plugin.

```shell
sudo docker plugin disable juicefs
```

Upgrade plugin:

```shell
sudo docker plugin upgrade juicefs
sudo docker plugin enable juicefs
```

Uninstall the plugin:

```shell
sudo docker plugin rm juicefs
```

### Troubleshooting

#### Storage volumes are not used but cannot be deleted

This may occur because the parameters set when creating the storage volume are incorrect. It is recommended to check the type of object storage, bucket name, Access Key, Secret Key, database address and other information. You can try disabling and re-enabling the JuiceFS volume plugin to release the failed volume, and then recreate the storage volume with the correct parameter information.

#### Log of the collection volume plugin

To troubleshoot, you can open a new terminal window and execute the following command while performing the operation to view the live log information.

```shell
journalctl -f -u docker | grep "plugin="
```

To learn more about the JuiceFS volume plugin, you can visit the [`juicedata/docker-volume-juicefs`](https://github.com/juicedata/docker-volume-juicefs) code repository.

## 3. Mount JuiceFS in a Container {#mount-juicefs-in-docker}

Mounting JuiceFS in a Docker container usually serves two purposes, one is to provide storage for the applications in the container, and the other is to map the JuiceFS storage mounted in the container to the host. To do this, you can use the official pre-built images of JuiceFS or write your own Dockerfile to package the JuiceFS client into a system image that meets your needs.

### Using pre-built images

[`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) is the official client image maintained by JuiceFS, in which both the community version and the cloud service client are packaged, and the program paths are:

- **Commnity Edition**: `/usr/local/bin/juicefs`
- **Cloud Service**：`/usr/bin/juicefs`

The mirror provides the following labels.

- **latest** - Latest stable version of the client included
- **nightly** - Latest development branch client included

:::tip
It is recommended to manually specify the [version tag](https://hub.docker.com/r/juicedata/mount/tags) of the image for production environments, e.g. `:v1.0.0-4.8.0`.
:::

### Compiling images manually

In some cases, you may need to integrate the JuiceFS client into a specific system image, which requires you to write your own Dockerfile file. In this process, you can either download the pre-compiled client directly or refer to [`juicefs.Dockerfile`](https://github.com/juicedata/juicefs-csi-driver/blob/master/docker/juicefs.Dockerfile) to compile the client from source code.

The following is an example of a Dockerfile file using the downloaded pre-compiled binaries.

```dockerfile
FROM alpine:latest
LABEL maintainer="Juicedata <https://juicefs.com>"

# Install JuiceFS client
RUN apk add --no-cache curl && \
  JFS_LATEST_TAG=$(curl -s https://api.github.com/repos/juicedata/juicefs/releases/latest | grep 'tag_name' | cut -d '"' -f 4 | tr -d 'v') && \
  wget "https://github.com/juicedata/juicefs/releases/download/v${JFS_LATEST_TAG}/juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  tar -zxf "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  install juicefs /usr/bin && \
  rm juicefs "juicefs-${JFS_LATEST_TAG}-linux-amd64.tar.gz" && \
  rm -rf /var/cache/apk/* && \
  apk del curl

ENTRYPOINT ["/usr/bin/juicefs", "--version"]
```

### Mapping the mount point in container to the host

JuiceFS makes it easy to connect object storage on the cloud to local, so you can using the cloud storage as if you were using local disks. If the entire mounting process is done in a Docker container, it not only simplifies the operation, but also facilitates maintenance and management. This approach is ideal for enterprise or home servers, NAS systems, and other devices to create data disaster recovery environments on the cloud.

The following is an example of a Docker Compose implementation that does the creation and mounting of a JuiceFS file system in a Docker container and maps the mount point in the container to the `$HOME/mnt` directory on the host.

#### Directories, files and structures

The example creates the following directories and files in the user's `$HOME` directory.

```shell
juicefs
├── .env
├── Dockerfile
├── db
│   └── home2cloud.db
├── docker-compose.yml
└── mnt
```

The following is the content of the `.env` file, which is used to define file system related information, such as file system name, object storage type, Bucket address, metadata address, etc. These settings are environment variables and are passed to the `docker-compose.yml` file at container build time.

```.env
# JuiceFS file system related configuration
JFS_NAME=home2nas
MOUNT_POINT=./mnt
STORAGE_TYPE=oss
BUCKET=https://abcdefg.oss-cn-shanghai.aliyuncs.com
ACCESS_KEY=<your-access-key>
SECRET_KEY=<your-secret-key>
METADATA_URL=sqlite3:///db/${JFS_NAME}.db
```

The following `docker-compose.yml` file is used to define container information, you can add options related to file system creation and mounting according to your needs.

```yml
version: "3"
services:
  makefs:
    image: juicedata/mount
    container_name: makefs
    volumes:
      - ./db:/db
    command: ["juicefs", "format", "--storage", "${STORAGE_TYPE}", "--bucket", "${BUCKET}", "--access-key", "${ACCESS_KEY}", "--secret-key", "${SECRET_KEY}", "${METADATA_URL}", "${JFS_NAME}"]

  juicefs:
    depends_on:
      - makefs
    image: juicedata/mount
    container_name: ${JFS_NAME}
    volumes:
      - ${MOUNT_POINT}:/mnt:rw,rshared
      - ./db:/db
    cap_add:
      - SYS_ADMIN
    devices:
      - /dev/fuse
    security_opt:
      - apparmor:unconfined
    command: ["/usr/local/bin/juicefs", "mount", "${METADATA_URL}", "/mnt"]
    restart: unless-stopped
```

You can adjust the parameters of the format and mount commands in the above code as needed. For example, when there is some latency in the network connection between local and object storage and local storage is reliable, you can mount the file system by adding the `--writeback` option so that files can be stored to the local cache first and then uploaded to the object storage asynchronously, see [client-side write cache](../guide/cache_management.md#writeback) for details.

For more file system creation and mounting parameters, please see [command reference](../reference/command_reference.md#mount).

#### Deployment and Usage

Complete the configuration of the `.env` and `docker-compose.yml` files, and execute the command to deploy the container:

```shell
sudo docker compose up -d
```

You can check the status of the container at any time by using the logs command.

```shell
sudo docker compose logs -f
```

If you need to stop the container, you can execute the stop command.

```shell
sudo docker compose stop
```

If you need to destroy the container, you can execute the down command.

```shell
sudo docker compose down
```

#### Notes

- The current example uses SQLite which is a standalone database, the database file will be saved in `$HOME/juicefs/db/` directory, please keep the database files safe.
- If you need to use another database, just adjust the value of `METADATA_URL` in the `.env` file, for example, set Reids as the metadata store: `METADATA_URL=redis://192.168.1.11/1`.
- The JuiceFS client automatically backs up metadata every hour, and the backed up data is exported in JSON format and uploaded to the `meta` directory of the object storage. In case of database failure, the latest backup can be used for recovery.
