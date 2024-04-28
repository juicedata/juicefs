---
title: POSIX ACL
description: 本文介绍了 JuiceFS 支持的 POSIX ACL 功能，以及如何启用和使用 ACL 权限。
sidebar_position: 3
---

POSIX ACL（Portable Operating System Interface for Unix - Access Control List）是 Unix-like 操作系统中的一种访问控制机制，可以对文件和目录的访问权限进行更细粒度的控制。

## 版本及兼容性要求

- JuiceFS 从 v1.2 版本开始支持 POSIX ACL；
- 所有版本客户端都可以挂载没有开启 ACL 的卷，不论这些卷是由新版本客户端创建的还是由旧版本客户端创建的；
- ACL 开启后暂不支持取消，因此 `--enable-acl` 选项是关联到卷的。

:::caution 提示
如果计划使用 ACL 功能，建议将所有客户端升级的最新版，避免旧版本客户端影响 ACL 的正确性。
:::

## 启用 ACL

如前所述，可以用新版客户端在创建新卷时开启 ACL，也可以用新版客户端在已创建的卷上开启 ACL。

### 创建新卷并开启 ACL

```shell
juicefs format --enable-acl sqlite3://myjfs.db myjfs
```

### 在已有卷上开启 ACL

使用 `config` 命令为一个已创建的卷开启 ACL 功能：

```shell
juicefs config --enable-acl sqlite3://myjfs.db
```

## 使用方法

为一个文件或目录设置 ACL 权限，可以使用 `setfacl` 命令，例如：

```shell
setfacl -m u:alice:rw- /mnt/jfs/file
```

更多关于 POSIX ACL 的详细规则，请参考：

- [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html)
- [setfacl](https://linux.die.net/man/1/setfacl)
- [JuiceFS ACL 功能全解析，更精细的权限控制](https://juicefs.com/zh-cn/blog/release-notes/juicefs-v12-beta-1-acl)

## 注意事项

- ACL 权限检测需要 [Linux kernel 4.9](https://lkml.iu.edu/hypermail/linux/kernel/1610.0/01531.html) 及以上版本；
- 启用 ACL 会有额外的性能影响。但因为有内存缓存优化，大部分使用场景性能损耗都较低，可参考[压测结果](https://juicefs.com/zh-cn/blog/release-notes/juicefs-v12-beta-1-acl#03-%E6%80%A7%E8%83%BD)。
