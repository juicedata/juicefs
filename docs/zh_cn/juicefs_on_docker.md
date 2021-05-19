# Docker 使用 JuiceFS

如果你希望使用 JuiceFS 为 Docker 容器提供持久化的存储，你需要先开启 FUSE 的 `user_allow_other` 选项，然后通过  `-o` 选项设置 `allow_other`重新挂载 JuiceFS 文件系统。

## FUSE 设置

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

> **注意**：如果 JuiceFS 文件系统在挂载时没有开启 `allow_other` 选项，则 Docker 容器将没有权限读写 JuiceFS 存储。

为 Docker 容器映射持久化存储时，使用 JuiceFS 存储与使用本地存储没有差别，假设你将 JuiceFS 文件系统挂载到了 `/mnt/jfs` 目录，在创建 Docker 容器时可以这样映射存储空间：

```
$ sudo docker run -d --name some-nginx \
	-v /mnt/jfs/html:/usr/share/nginx/html \
	nginx
```



## Docker Volume Plugin

JuiceFS 社区版本暂不支持



