---
title: Using JuiceFS in Docker
sidebar_position: 6
slug: /juicefs_on_docker
description: Using JuiceFS in Docker in different ways, including volume mapping, volume plugin, and mounting in containers.
---

You can use the JuiceFS file system in Docker by running the client directly in the container or using a volume plugin.

## Using a volume plugin {#volume-plugin}

If you have specific requirements for mount management, such as managing mount points through Docker to facilitate different application containers using different JuiceFS file systems, you can use a [Docker volume plugin](https://github.com/juicedata/docker-volume-juicefs).

Docker plugins are usually provided in the form of images. The [JuiceFS volume plugin image](https://hub.docker.com/r/juicedata/juicefs) contains the [JuiceFS Community Edition](../introduction/README.md) and [JuiceFS Cloud Service](https://juicefs.com/docs/cloud) clients. After installation, you can run the volume plugin to create JuiceFS volumes in Docker.

Install the plugin using the following command and provide the necessary permissions for FUSE as prompted:

```shell
docker plugin install juicedata/juicefs
```

You can use the following commands to manage the volume plugin:

```shell
# Disable the plugin
docker plugin disable juicedata/juicefs

# Upgrade the plugin (must be disabled first)
docker plugin upgrade juicedata/juicefs
docker plugin enable juicedata/juicefs

# Remove the plugin
docker plugin rm juicedata/juicefs
```

### Create a storage volume {#create-volume}

Replace `<VOLUME_NAME>`, `<META_URL>`, `<STORAGE_TYPE>`, `<BUCKET_NAME>`, `<ACCESS_KEY>`, and `<SECRET_KEY>` in the following command with your own file system configuration:

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

For pre-created file systems, you only need to specify the file system name and database address when creating the volume plugin, for example:

```shell
docker volume create -d juicedata/juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  jfsvolume
```

If you need to pass additional environment variables when mounting the file system, such as in [Google Cloud](../reference/how_to_set_up_object_storage.md#google-cloud), you can append parameters similar to `-o env=FOO=bar,SPAM=egg` to the above command.

### Usage and management {#usage-and-management}

```shell
# Mount the volume when creating a container
docker run -it -v jfsvolume:/opt busybox ls /opt

# After unmounting, you can delete the storage volume. Note that this only deletes the corresponding resources in Docker and does not affect the data stored in JuiceFS.
docker volume rm jfsvolume
```

### Using the plugin in Docker Compose {#using-plugin-in-docker-compose}

Here is an example of using the JuiceFS volume plugin in `docker-compose`:

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
      # SQLite creates the database file in the plugin container's local path,
      # and sqlite:// will fail when the service is restarted.
      # (See https://github.com/juicedata/docker-volume-juicefs/issues/37 for details)
      metaurl: ${META_URL}
      storage: ${STORAGE_TYPE}
      bucket: ${BUCKET}
      access-key: ${ACCESS_KEY}
      secret-key: ${SECRET_KEY}
      # If necessary, you can pass additional environment variables using env
      # env: FOO=bar,SPAM=egg
```

Usage and management:

```shell
# Start the service
docker-compose up

# Stop the service and unmount the JuiceFS file system from Docker
docker-compose down --volumes
```

### Troubleshooting the volume plugin {#troubleshooting}

If it is not working properly, it is recommended to first [upgrade the volume plugin](#volume-plugin), and then check the logs based on the problem.

* Collect JuiceFS client logs. The logs are located inside the Docker volume plugin container and need to be accessed by entering the container:

  ```shell
  # Confirm the docker plugins runtime directory, which may be different from the example below depending on the actual situation
  # The directory printed by ls is the container directory, and the name is the container ID
  ls /run/docker/plugins/runtime-root/plugins.moby

  # Print plugin container information
  # If the printed container list is empty, it means that the plugin container failed to be created
  # Read the plugin startup log below to continue troubleshooting
  runc --root /run/docker/plugins/runtime-root/plugins.moby list

  # Enter the container and print the log
  runc --root /run/docker/plugins/runtime-root/plugins.moby exec 452d2c0cf3fd45e73a93a2f2b00d03ed28dd2bc0c58669cca9d4039e8866f99f cat /var/log/juicefs.log
  ```

  If the container does not exist (`ls` finds an empty directory) or the `juicefs.log` does not exist in the final log printing stage, it is likely that the mount itself failed. Continue to check the plugin's own logs to find the cause.

* Collect plugin logs, using systemd as an example:

  ```shell
  journalctl -f -u docker | grep "plugin="
  ```

  If there is an error when the plugin calls `juicefs` or if the plugin itself reports an error, it will be reflected in the logs.

## Using the JuiceFS client in containers {#mount-juicefs-in-docker}

Compared to the volume plugin, using the JuiceFS client directly in the container is more flexible. You can directly mount the JuiceFS file system in the container or access it through S3 Gateway or WebDAV.

### Method 1: Build your own image

The JuiceFS client is a standalone binary program that provides versions for both AMD64 and ARM64 architectures. You can define the command to download and install the JuiceFS client in the Dockerfile, for example:

```Dockerfile
FROM ubuntu:22.04
...
# Use the official one-click installation script
RUN curl -sSL https://d.juicefs.com/install | sh - 
```

For more information, see [Customizing Container Images](https://juicefs.com/docs/csi/guide/custom-image).

### Method 2: Use the officially maintained image

The JuiceFS officially maintained image [`juicedata/mount`](https://hub.docker.com/r/juicedata/mount) is tagged to specify the desired version. **The community edition tags include `latest` and `ce`**, such as `ce-v1.1.2` and `ce-nightly`. The `latest` tag represents the latest community edition, and the `nightly` tag points to the latest development version. For details, see the [tags page](https://hub.docker.com/r/juicedata/mount/tags) on Docker Hub.

Before you start, you need to prepare [object storage](../reference/how_to_set_up_object_storage.md) and [metadata engine](../reference/how_to_set_up_metadata_engine.md).

#### Create a file system

Create a file system through a temporary container, for example:

```sh
docker run --rm \
    juicedata/mount:ce-v1.1.2 juicefs format \
    --storage s3 \
    --bucket https://xxx.your-s3-endpoint.com \
    --access-key=ACCESSKEY \
    --secret-key=SECRETKEY \
    rediss://user:password@xxx.your-redis-server.com:6379/1 myjfs
```

Replace `--storage`, `--bucket`, `--access-key`, `--secret-key`, and the metadata engine URL with your own configuration.

#### Mount the file system directly in the container

Create a container and mount the JuiceFS file system in the container, for example:

```sh
docker run --privileged --name myjfs \
    juicedata/mount:ce-v1.1.2 juicefs mount \
    rediss://user:password@xxx.your-redis-server.com:6379/1 /mnt
```

Replace the metadata engine URL with your own configuration. `/mnt` is the mount point and can be modified as needed. Since FUSE is used, `--privileged` permission is also required.

#### Mount the file system through Docker Compose

Here is an example using Docker Compose. Replace the metadata engine URL and mount point with your own configuration.

```yaml
version: "3"
services:
    juicefs:
      image: juicedata/mount:ce-v1.1.2
      container_name: myjfs
      volumes:
        - ./mnt:/mnt:rw,rshared
      cap_add:
        - SYS_ADMIN
      devices:
        - /dev/fuse
      security_opt: 
        - apparmor:unconfined
      command: ["juicefs", "mount", "rediss://user:password@xxx.your-redis-server.com:6379/1", "/mnt"]
      restart: unless-stopped
```

In the container, the JuiceFS file system is mounted to the `/mnt` directory, and the volumes section in the configuration file maps the `/mnt` in the container to the `./mnt` directory on the host, allowing direct access to the JuiceFS file system mounted in the container from the host.

#### Access the file system through S3 Gateway

Here is an example of exposing JuiceFS for access through S3 Gateway. Replace `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`, the metadata engine URL, and the address and port number to listen on with your own configuration.

```yaml
version: "3"
services:
    s3-gateway:
      image: juicedata/mount:ce-v1.1.2
      container_name: juicefs-s3-gateway
      environment:
        - MINIO_ROOT_USER=your-username
        - MINIO_ROOT_PASSWORD=your-password
      ports:
        - "9090:9090"
      command: ["juicefs", "gateway", "rediss://user:password@xxx.your-redis-server.com:6379/1", "0.0.0.0:9090"]
      restart: unless-stopped
```

Use port `9090` on the host to access the S3 Gateway console, and use the same address to read and write the JuiceFS file system through the S3 client or SDK.
