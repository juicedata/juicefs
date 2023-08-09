---
title: Use JuiceFS on Docker
sidebar_position: 6
slug: /juicefs_on_docker
description: Different ways to use JuiceFS in Docker, including bind mount and Docker volume plugin, and mount inside container.
---

The simplest way would be using bind mount, you can directly mount JuiceFS into container using `-v`. Note that if host mount point isn't created by root, you'll have to enable [`allow_other`](../reference/fuse_mount_options.md#allow_other) to allow access inside container.

```shell
docker run -d --name nginx \
  -v /jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

If you wish to control mount points using Docker, so that different application containers may use different JuiceFS file systems, you can use our [Docker volume plugin](https://github.com/juicedata/docker-volume-juicefs).

## Docker volume plugin {#volume-plugin}

Every Docker plugin itself is a Docker image, and JuiceFS Docker volume plugin is packed with [JuiceFS Community Edition](../introduction/README.md) as well as [JuiceFS Enterprise Edition](https://juicefs.com/docs/cloud) clients, after installation, you'll be able to run this plugin, and create JuiceFS Volume inside Docker.

Install the plugin with the following command, grant permissions when asked:

```shell
docker plugin install juicedata/juicefs
```

You can manage volume plugin with the following commands:

```shell
# Disable the volume plugin
docker plugin disable juicedata/juicefs

# Upgrade plugin (need to disable first)
docker plugin upgrade juicedata/juicefs
docker plugin enable juicedata/juicefs

# Uninstall plugin
docker plugin rm juicedata/juicefs
```

### Create a storage volume {#create-volume}

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

To use Docker volume plugin with existing JuiceFS volumes, simply specify the file system name and database address:

```shell
docker volume create -d juicedata/juicefs \
  -o name=<VOLUME_NAME> \
  -o metaurl=<META_URL> \
  jfsvolume
```

If you need to pass extra environment variables to the mount process (e.g. [Google Cloud](../reference/how_to_set_up_object_storage.md#google-cloud)), append them as `-o env=FOO=bar,SPAM=egg`.

### Usage and management {#usage-and-management}

```shell
# Mount the volume in container
docker run -it -v jfsvolume:/opt busybox ls /opt

# After a volume has been unmounted, delete using the following command
# Deleting a volume only remove the relevant resources from Docker, which doesn't affect data stored in JuiceFS
docker volume rm jfsvolume
```

### Using Docker Compose {#using-docker-compose}

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

### Troubleshooting {#troubleshooting}

If JuiceFS Docker volume plugin is not working properly, it's recommend to [upgrade the volume plugin](#volume-plugin) first, and then check logs to debug.

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

Mounting JuiceFS in a Docker container usually serves two purposes, one is to provide storage for the applications in the container, and the other is to map the mount point inside container to the host. To do so, you can use the officially maintained images or build your own image for customization. See [Customize Container Image](https://juicefs.com/docs/csi/guide/custom-image).
