# JuiceFS on Docker

If you want to use JuiceFS to provide persistent storage for Docker containers, you need to enable the `user_allow_other` option of FUSE, and then re-mount the JuiceFS file systemd with the `allow_other` option.

## FUSE Setting

### Change the configuration file

Edit the configuration file of FUSE, usually `/etc/fuse.conf`:

```
$ sudo nano /etc/fuse.conf
```

Delete the `# ` symbol in front of ``user_allow_ other` in the configuration file, and modify it as follows:

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
$ sudo juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

### Mapping storage to a Docker container

> **Note**: If the JuiceFS file system is mounted without the `allow_other` option, the Docker container will not have permission to read and write JuiceFS storage.

When mapping persistent storage for a Docker container, there is no difference between using JuiceFS storage and using local storage. Assuming you mount the JuiceFS file system to the `/mnt/jfs` directory, you can map the storage when creating a Docker container, like this:

```
$ sudo docker run -d --name some-nginx \
	-v /mnt/jfs/html:/usr/share/nginx/html \
	nginx
```

## Docker Volume Plugin

JuiceFS community version is not support it currently.