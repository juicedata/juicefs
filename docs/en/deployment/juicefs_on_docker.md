---
title: Use JuiceFS on Docker
sidebar_position: 3
slug: /juicefs_on_docker
description: Different ways to use JuiceFS in Docker, including bind mount and Docker volume plugin, and mount inside container.
---

The simplest way would be using bind mount, you can directly mount JuiceFS into container using `-v`, assuming the host mount point being `/jfs`:

```shell
docker run -d --name nginx \
  -v /jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

If you wish to control mount points using Docker, so that different application containers may use different JuiceFS file systems, you can use our [Docker volume plugin](https://github.com/juicedata/docker-volume-juicefs).

## Docker Volume Plugin {#docker-volume-plugin}

Every Docker plugin itself is a docker image, and JuiceFS Docker volume plugin is packed with [JuiceFS Community Edition](https://juicefs.com/docs/community/introduction/) as well as [JuiceFS Cloud Service](./introduction/readme.md) clients, after installation, you'll be able to run this plugin, and create JuiceFS Volume inside docker.

Install the plugin with the following command, grant permissions when asked.

```shell
docker plugin install juicedata/juicefs --alias juicefs
```

### Create a Storage Volume

In the following command, replace `<VOLUME_NAME>`, `<META_URL>`, `<STORAGE_TYPE>`, `<BUCKET_NAME>`, `<ACCESS_KEY>`, `<SECRET_KEY>` accordingly.

```shell
docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    -o storage=<STORAGE_TYPE> \
    -o bucket=<BUCKET_NAME> \
    -o access-key=<ACCESS_KEY> \
    -o secret-key=<SECRET_KEY> \
    jfsvolume
```

To use Docker volume plugin with existing JuiceFS volumes, simply specify the file system name and database address, e.g.

```shell
docker volume create -d juicefs \
    -o name=<VOLUME_NAME> \
    -o metaurl=<META_URL> \
    jfsvolume
```

### Using Storage Volumes

Mounted volumes when creating containers.

```shell
docker run -it -v jfsvolume:/opt busybox ls /opt
```

### Delete a storage volume

```shell
docker volume rm jfsvolume
```

### Upgrade and Uninstall Volume Plugin

Plugins need to be deactivated before upgrading or uninstalling the Docker volume plugin.

```shell
docker plugin disable juicefs
```

Upgrade plugin:

```shell
docker plugin upgrade juicefs
docker plugin enable juicefs
```

Uninstall the plugin:

```shell
docker plugin rm juicefs
```

### Troubleshooting

#### Plugin log

Check plugin log within the Docker daemon log:

```shell
journalctl -f -u docker | grep "plugin="
```

To learn more about the JuiceFS volume plugin, visit [`juicedata/docker-volume-juicefs`](https://github.com/juicedata/docker-volume-juicefs).

## Mount JuiceFS in a Container {#mount-juicefs-in-docker}

Mounting JuiceFS in a Docker container usually serves two purposes, one is to provide storage for the applications in the container, and the other is to map the JuiceFS storage mounted in the container to the host. To do this, you can use the official pre-built images of JuiceFS or write your own Dockerfile to package the JuiceFS client into a system image that meets your needs.

### Using pre-built images

[`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) is the official client image maintained by JuiceFS, in which both the community version and the cloud service client are packaged, and the program paths are:

- Community Edition: `/usr/local/bin/juicefs`
- Cloud Service：`/usr/bin/juicefs`

The mirror provides the following image tags.

- `latest` - Latest stable version of the client included
- `nightly` - Latest development branch client included
- Tag that looks like `v1.0.4-4.9.0` - Both client version specified in tag, recommend for production use

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
docker compose up -d
```

You can check the status of the container at any time by using the logs command.

```shell
docker compose logs -f
```

If you need to stop the container, you can execute the stop command.

```shell
docker compose stop
```

If you need to destroy the container, you can execute the down command.

```shell
docker compose down
```

#### Notes

- The current example uses SQLite which is a standalone database, the database file will be saved in `$HOME/juicefs/db/` directory, please keep the database files safe.
- If you need to use another database, just adjust the value of `METADATA_URL` in the `.env` file, for example, set Reids as the metadata store: `METADATA_URL=redis://192.168.1.11/1`.
- The JuiceFS client automatically backs up metadata every hour, and the backed up data is exported in JSON format and uploaded to the `meta` directory of the object storage. In case of database failure, the latest backup can be used for recovery.
