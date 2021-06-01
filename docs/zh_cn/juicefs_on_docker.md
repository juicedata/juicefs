# Docker 使用 JuiceFS

默认情况下，只有挂载 JuiceFS 存储的用户有存储的读写权限，当你需要将 JuiceFS 存储映射给 Docker 容器使用时，如果你没有使用 root 身份挂载 JuiceFS 存储，则需要先开启 FUSE 的 `user_allow_other` 选项，然后再添加  `-o allow_other` 选项重新挂载 JuiceFS 文件系统。

> **注意**：使用 root 用户身份或使用 sudo 挂载的 JuiceFS 存储，会自动添加 `allow_other`选项，无需手动设置。

## FUSE 设置

默认情况下，`allow_other` 选项只允许 root 用户使用，为了让普通用户也有权限使用该挂载选项，需要修改 FUSE 的配置文件。 

### 修改配置文件

编辑 FUSE 的配置文件，通常是 `/etc/fuse.conf`：

```
$ sudo nano /etc/fuse.conf
```

将配置文件中的 `user_allow_other` 前面的 `#` 注释符删掉，修改后类似下面这样：

```
# /etc/fuse.conf - Configuration file for Filesystem in Userspace (FUSE)

# Set the maximum number of FUSE mounts allowed to non-root users.
# The default is 1000.
#mount_max = 1000

# Allow non-root users to specify the allow_other or allow_root mount options.
user_allow_other
```

### 重新挂载 JuiceFS

FUSE 的 `user_allow_other` 启用后，你需要重新挂载 JuiceFS 文件系统，使用 `-o` 选项设置 `allow_other`，例如：

```
$ juicefs mount -d -o allow_other redis://<your-redis-url>:6379/1 /mnt/jfs
```

## Docker 映射 JuiceFS 存储

为 Docker 容器映射持久化存储时，使用 JuiceFS 存储与使用本地存储没有差别，假设你将 JuiceFS 文件系统挂载到了 `/mnt/jfs` 目录，在创建 Docker 容器时可以这样映射存储空间：

```
$ sudo docker run -d --name some-nginx \
	-v /mnt/jfs/html:/usr/share/nginx/html \
	nginx
```

## Docker Volume Plugin

JuiceFS 也支持使用 [volume plugin](https://docs.docker.com/engine/extend/) 方式访问。

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

将上面 `{{VOLUME_NAME}}, {{META_URL}}, {{ACCESS_KEY}}, {{SECRET_KEY}}` 替换成你自己的文件系统配置。想要了解更多 JuiceFS 卷插件内容，可以访问  [juicedata/docker-volume-juicefs](https://github.com/juicedata/docker-volume-juicefs) 代码仓库。
