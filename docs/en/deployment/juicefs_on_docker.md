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

Every Docker plugin itself is a Docker image, and JuiceFS Docker volume plugin is packed with [JuiceFS Community Edition](https://juicefs.com/docs/community/introduction) as well as [JuiceFS Cloud Service](./introduction/readme.md) clients, after installation, you'll be able to run this plugin, and create JuiceFS Volume inside Docker.

Install the plugin with the following command, grant permissions when asked.

```shell
docker plugin install juicedata/juicefs
```

### Create a Storage Volume

In the following command, replace `<VOLUME_NAME>`, `<META_URL>`, `<STORAGE_TYPE>`, `<BUCKET_NAME>`, `<ACCESS_KEY>`, `<SECRET_KEY>` accordingly.

```shell
docker volume create -d juicedata/juicefs \
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
docker volume create -d juicedata/juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  jfsvolume
```

### Usage and management

```shell
# Mount the volume in container
docker run -it -v jfsvolume:/opt busybox ls /opt

# After a volume has been unmounted, delete using the following command
# Deleting a volume only remove the relevant resources from Docker, which doesn't affect data stored in JuiceFS
docker volume rm jfsvolume

# Disable the volume plugin
docker plugin disable juicedata/juicefs

# Upgrade plugin (need to disable first)
docker plugin upgrade juicedata/juicefs
docker plugin enable juicedata/juicefs

# Uninstall
docker plugin rm juicedata/juicefs
```

### Using Docker Compose

Example for creating and mounting JuiceFS volume with `docker-compose`:

```yaml
version: '3'
services:
busybox:
  image: busybox
  command: "ls /jfs"
  volumes:
    - jfsvolume:/jfs
volumes:
  jfsvolume:
    driver: juicedata/juicefs
    driver_opts:
      name: ${VOL_NAME}
      metaurl: ${META_URL}
      storage: ${STORAGE_TYPE}
      bucket: ${BUCKET}
      access-key: ${ACCESS_KEY}
      secret-key: ${SECRET_KEY}
      # Pass extra environment variables using env
      # env: FOO=bar,SPAM=egg
```

Common management commands:

```shell
# Start the service
docker-compose up

# Shut down the service and remove Docker volumes
docker-compose down --volumes
```

### Troubleshooting

If JuiceFS Docker volume plugin is not working properly, upgrade to the [latest version](https://hub.docker.com/r/juicedata/juicefs/tags), and then check logs to debug.

* Collect JuiceFS Client logs, which is inside the Docker volume plugin container itself:

  ```shell
  # locate the docker plugins runtime directory, your environment may differ from below example
  # container directories will be printed, directory name is container ID
  ls /run/docker/plugins/runtime-root/plugins.moby

  # print plugin container info
  # if container list is empty, that means plugin container didn't start properly
  # read the next step to continue debugging
  runc --root /run/docker/plugins/runtime-root/plugins.moby list

  # collect log inside plugin container
  runc --root /run/docker/plugins/runtime-root/plugins.moby exec 452d2c0cf3fd45e73a93a2f2b00d03ed28dd2bc0c58669cca9d4039e8866f99f cat /var/log/juicefs.log
  ```

  If it is found that the container doesn't exist (`ls` found that the directory is empty), or that `juicefs.log` doesn't exist, this usually indicates a bad mount, check plugin logs to further debug.

* Collect plugin log, for example under systemd:

  ```shell
  journalctl -f -u docker | grep "plugin="
  ```

  `juicefs` is called to perform the actual mount inside the plugin container, if any error occurs, it will be shown in the Docker daemon logs, same when there's error with the volume plugin itself.

## Mount JuiceFS in a Container {#mount-juicefs-in-docker}

Mounting JuiceFS in a Docker container usually serves two purposes, one is to provide storage for the applications in the container, and the other is to map the JuiceFS storage mounted in the container to the host. To do this, you can use the official pre-built images of JuiceFS or write your own Dockerfile to package the JuiceFS client into a system image that meets your needs.

### Using mount pod image

[`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) is the Docker image used to mount JuiceFS in [JuiceFS CSI Driver](https://juicefs.com/docs/csi/introduction). This image contains both JuiceFS Community Edition and Cloud Service client executables, their respective path:

- Community Edition: `/usr/local/bin/juicefs`
- Cloud Service:`/usr/bin/juicefs`

The following image tags are provided:

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
