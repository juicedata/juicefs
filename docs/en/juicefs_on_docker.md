# JuiceFS on Docker

There are  three ways to use JuiceFS on Docker:

## Table of Content
1. [Volume Mapping](#1-Volume-Mapping)
2. [Docker Volume Plugin](#2-Docker-Volume-Plugin)
3. [Mount JuiceFS in a Container](#3-Mount-JuiceFS-in-a-Container)

## 1. Volume Mapping

This method is to map the directories in the JuiceFS mount point to the Docker container. For example, the JuiceFS storage is mounted in the `/mnt/jfs` directory. When creating a container, you can map JuiceFS storage to the Docker container as follows:

```shell
$ sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

By default, only the user who mounts the JuiceFS storage has the access permissions for the storage. When you need to map the JuiceFS storage to a Docker container, if you are not using the root identity to mount the JuiceFS storage, you need to turn on the FUSE `user_allow_other` first, and then re-mount the JuiceFS with `-o allow_other` option.

> **Note**: JuiceFS storage mounted with root user identity or `sudo` will automatically add the `allow_other` option, no manual setting is required.

### FUSE Setting

By default, the `allow_other` option is only allowed to be used by the root user. In order to allow other users to use this mount option, the FUSE configuration file needs to be modified.

### Change the configuration file

Edit the configuration file of FUSE, usually `/etc/fuse.conf`:

```sh
$ sudo nano /etc/fuse.conf
```

Delete the `# ` symbol in front of `user_allow_other` in the configuration file, and modify it as follows:

```conf
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```

#### Re-mount JuiceFS

After the `allow_other` of FUSE is enabled, you need to re-mount the JuiceFS file systemd with the `allow_other` option, for example:

```sh
$ juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

🏡 [Back to Top](#Table-of-Content)

## 2. Docker Volume Plugin

We can also use [volume plugin](https://docs.docker.com/engine/extend/) to access JuiceFS.

```sh
$ docker plugin install juicedata/juicefs
Plugin "juicedata/juicefs" is requesting the following privileges:
 - network: [host]
 - device: [/dev/fuse]
 - capabilities: [CAP_SYS_ADMIN]
Do you grant the above permissions? [y/N]

$ docker volume create -d juicedata/juicefs:latest -o name={{VOLUME_NAME}} -o metaurl={{META_URL}} -o access-key={{ACCESS_KEY}} -o secret-key={{SECRET_KEY}} jfsvolume
$ docker run -it -v jfsvolume:/opt busybox ls /opt
```

Replace above `{{VOLUME_NAME}}`, `{{META_URL}}`, `{{ACCESS_KEY}}`, `{{SECRET_KEY}}` to your own volume setting. For more details about JuiceFS volume plugin, refer [juicedata/docker-volume-juicefs](https://github.com/juicedata/docker-volume-juicefs) repository.

🏡 [Back to Top](#Table-of-Content)

## 3. Mount JuiceFS in a Container

This method is to mount and use the JuiceFS storage directly in the Docker container. Compared with the first method, directly mounting JuiceFS in the container can reduce the chance of file misoperation. It also makes container management clearer and more intuitive.

Since the file system mounting in the container needs to copy the JuiceFS client to the container, the process of downloading or copying the JuiceFS client and mounting the file system needs to be written into the Dockerfile, and then rebuilt the image. For example, you can refer to the following Dockerfile to package the JuiceFS client into the Alpine image.

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

ENTRYPOINT ["/usr/bin/juicefs", "mount"]
```

In addition, since the use of FUSE in the container requires corresponding permissions, when creating the container, you need to specify the `--privileged=true` option, for example:

```shell
$ sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  --privileged=true \
  nginx-with-jfs
```

🏡 [Back to Top](#Table-of-Content)
