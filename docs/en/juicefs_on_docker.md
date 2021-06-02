# JuiceFS on Docker

By default, only the user who mounts the JuiceFS storage has the access permissions for the storage. When you need to map the JuiceFS storage to a Docker container, if you are not using the root identity to mount the JuiceFS storage, you need to turn on the FUSE `user_allow_other` first, and then re-mount the JuiceFS with `-o allow_other` option.

> **Note**: JuiceFS storage mounted with root user identity or `sudo` will automatically add the `allow_other` option, no manual setting is required.

## FUSE Setting

By default, the `allow_other` option is only allowed to be used by the root user. In order to allow other users to use this mount option, the FUSE configuration file needs to be modified.

### Change the configuration file

Edit the configuration file of FUSE, usually `/etc/fuse.conf`:

```
$ sudo nano /etc/fuse.conf
```

Delete the `# ` symbol in front of `user_allow_other` in the configuration file, and modify it as follows:

```
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```

### Re-mount JuiceFS

After the `allow_other` of FUSE is enabled, you need to re-mount the JuiceFS file systemd with the `allow_other` option, for example:

```
$ juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

### Mapping storage to a Docker container

When mapping persistent storage for a Docker container, there is no difference between using JuiceFS storage and using local storage. Assuming you mount the JuiceFS file system to the `/mnt/jfs` directory, you can map the storage when creating a Docker container, like this:

```
$ sudo docker run -d --name some-nginx \
	-v /mnt/jfs/html:/usr/share/nginx/html \
	nginx
```

## Docker Volume Plugin

We can also use [volume plugin](https://docs.docker.com/engine/extend/) to access JuiceFS.

```
$ docker plugin install juicedata/juicefs
Plugin "juicedata/juicefs" is requesting the following privileges:
 - network: [host]
 - device: [/dev/fuse]
 - capabilities: [CAP_SYS_ADMIN]
Do you grant the above permissions? [y/N]

$ docker volume create -d juicedata/juicefs:latest -o name={{VOLUME_NAME}} -o metaurl={{META_URL}} -o access-key={{ACCESS_KEY}} -o secret-key={{SECRET_KEY}} jfsvolume
$ docker run -it -v jfsvolume:/opt busybox ls /opt
```

Replace above `{{VOLUME_NAME}}, {{META_URL}}, {{ACCESS_KEY}}, {{SECRET_KEY}}` to your own volume setting. For more details about JuiceFS volume plugin, refer [juicedata/docker-volume-juicefs](https://github.com/juicedata/docker-volume-juicefs) repository.
