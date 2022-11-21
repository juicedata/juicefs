---
title: 排查案例
---

这里收录常见问题的具体排查步骤。

## 挂载相关问题

### 权限问题导致挂载错误 {#mount-permission-error}

使用 [Docker bind mounts](https://docs.docker.com/storage/bind-mounts) 把宿主机上的一个目录挂载到容器中时，可能遇到下方错误：

```
docker: Error response from daemon: error while creating mount source path 'XXX': mkdir XXX: file exists.
```

这往往是因为使用了非 root 用户执行了 `juicefs mount` 命令，进而导致 Docker 没有权限访问这个目录。这个问题有两种解决方法：

* 用 root 用户执行 `juicefs mount` 命令
* 在 FUSE 的配置文件，以及挂载命令中增加 [`allow_other`](../reference/fuse_mount_options.md#allow_other) 挂载选项。

使用普通用户执行 `juicefs mount` 命令时，可能遭遇下方错误：

```
fuse: fuse: exec: "/bin/fusermount": stat /bin/fusermount: no such file or directory
```

这个错误意味着使用了非 root 用户执行 ，并且 `fusermount` 这个命令也找不到。这个问题有两种解决方法：

1. 用 root 用户执行 `juicefs mount` 命令
2. 安装 `fuse` 包（例如 `apt-get install fuse`、`yum install fuse`）

而如果当前用户不具备 `fusermount` 命令的执行权限，则还会遇到以下错误：

```
fuse: fuse: fork/exec /usr/bin/fusermount: permission denied
```

此时可以通过下面的命令检查 `fusermount` 命令的权限：

```shell
$ ls -l /usr/bin/fusermount
-rwsr-x---. 1 root fuse 27968 Dec  7  2011 /usr/bin/fusermount
```

上面的例子表示只有 root 用户和 `fuse` 用户组的用户有权限执行。另一个例子：

```shell
$ ls -l /usr/bin/fusermount
-rwsr-xr-x 1 root root 32096 Oct 30  2018 /usr/bin/fusermount
```

上面的例子表示所有用户都有权限执行。

## 开发相关问题

编译 JuiceFS 需要 GCC 5.4 及以上版本，版本过低可能导致类似下方报错：

```
/go/pkg/tool/linux_amd64/link: running gcc failed: exit status 1
/go/pkg/tool/linux_amd64/compile: signal: killed
```
