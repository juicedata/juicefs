---
sidebar_position: 3
---
# POSIX ACL

1.2 版本支持 POSIX ACL, 详细规则参考

- [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html)
- [setfacl](https://linux.die.net/man/1/setfacl)

## 使用

<!-- markdownlint-disable MD044 enhanced-proper-names -->

目前 ACL 开启后暂不支持取消，所以--enable-acl flag 与卷关联。

### 新卷创建启用 ACL

```shell
juicefs format sqlite3://myjfs.db myjfs --enable-acl
```

### 已有卷启用 ACL

- 所有旧客户端升级到 v1.2, 并且重新 mount 卷
- 使用 v1.2 版本客户端执行下面指令进行配置

```shell
juicefs config sqlite3://myjfs.db --enable-acl
```

<!-- markdownlint-enable MD044 enhanced-proper-names -->

## 兼容

- 新版本客户端兼容老版本卷
- 老版本客户端兼容 (不开启 ACL 的) 新版本卷
:::caution 提示
如果启用 ACL 功能，建议所有客户端都升级。老版本客户端挂载了新卷 (没有开启 ACL), 后续如果卷开启 ACL, 老版本客户端的操作会影响 ACL 的正确性
:::

## 其他

- 开启 ACL 后，需要[Linux kernel 4.9](https://lkml.iu.edu/hypermail/linux/kernel/1610.0/01531.html)及以上版本，才支持 ACL 权限检测
- 开启 ACL 后，客户端版本要求会提升到 v1.2
- 开启 ACL 会有额外的性能影响，对于 ACL 变动不频繁的场景，有内存 cache 优化影响不大
