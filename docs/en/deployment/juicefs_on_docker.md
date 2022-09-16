---
sidebar_label: Use JuiceFS on Docker
sidebar_position: 2
slug: /juicefs_on_docker
---
# Use JuiceFS on Docker

There are three ways to use JuiceFS with Docker:

## 1. Volume Mapping {#volume-mapping}

Volume mapping maps the directories in the JuiceFS mount point to the Docker container. For example, assuming a JuiceFS file system is mounted to the `/mnt/jfs` directory, you can map this file system when creating a docker container as follows:

```shell
sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  nginx
```

By default, only the user who mounts the JuiceFS file system has access permissions. To make a file system mappable for docker containers created by others, you need to enable FUSE option `user_allow_other` first, and then re-mount the file system with option `-o allow_other`.

> **Note**: JuiceFS file system mounted with root privilege has already enabled the `allow_other` option. Thus, you don't need to set it manually.

### FUSE Settings

By default, the `allow_other` option is only available for users with root privilege. In order to allow other users to use this mount option, the FUSE configuration file needs to be modified.

### Change the configuration file

Edit the configuration file of FUSE, usually `/etc/fuse.conf`:

```sh
sudo nano /etc/fuse.conf
```

First, uncomment the line `# user_allow_other` by deleting the`#` symbol. Your configuration file should look like the following after the modification.

```conf
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```

#### Re-mount JuiceFS

Run the following command to re-mount the JuiceFS file system with `allow_other` option.

```sh
juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

## 2. Docker Volume Plugin

[Volume plugin](https://docs.docker.com/engine/extend/) is another option to access JuiceFS.

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

Replace `{{VOLUME_NAME}}`, `{{META_URL}}`, `{{ACCESS_KEY}}` and `{{SECRET_KEY}}` to fit your situation. For more details about JuiceFS volume plugin, please refer to [juicedata/docker-volume-juicefs](https://github.com/juicedata/docker-volume-juicefs) repository.

## 3. Mount JuiceFS in a Container

In this section, we introduce a way to mount and use JuiceFS file system directly in a Docker container. Compared with [volume mapping](#volume-mapping), directly mounting reduces the chance of misoperating files. It also makes container management clearer and more intuitive.

To mount a JuiceFS file system in a Docker container, the JuiceFS client executable needs to be copied into the image. Usually, this could be done by writing the commands that download or copy the executable and mount the file system into your Dockerfile, and rebuild the image. You can refer to the following Dockerfile as an example which packs the JuiceFS client into the Alpine image.

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

In addition, using FUSE in a container requires specific permissions. You need to specify the `--privileged=true` option on creating. For example:

```shell
sudo docker run -d --name nginx \
  -v /mnt/jfs/html:/usr/share/nginx/html \
  -p 8080:80 \
  --privileged=true \
  nginx-with-jfs
```
