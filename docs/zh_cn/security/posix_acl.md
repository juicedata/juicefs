---
sidebar_position: 3
---
# POSIX ACL

1.2版本支持POSIX ACL, 详细规则参考
- [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html#:~:text=Access%20Check%20Algorithm&text=The%20ACL%20entries%20are%20looked,matching%20entry%20contains%20sufficient%20permissions.)
- [setfacl](https://linux.die.net/man/1/setfacl)

## 使用
目前ACL开启后暂不支持取消, 所以--enable-acl flag与卷关联.

- 创建新卷
```shell
juicefs format sqlite3://myjfs.db myjfs --enable-acl
```

- 修改已有卷配置
```shell
juicefs config sqlite3://myjfs.db --enable-acl
```

## 兼容
- 新版本客户端兼容老版本卷
- 老版本客户端兼容(不开启acl的)新版本卷
:::caution 提示
如果启用acl功能, 建议所有客户端都升级. 老版本客户端挂载了新卷(没有开启acl), 后续如果卷开启了acl, 老版本客户端的操作会影响ACL的正确性
:::

## 其他
- 开启ACL后, 客户端版本要求会提升到v1.2
- 开启ACL会有额外的性能影响, 对于ACL变动不频繁的场景, 有内存cache优化影响不大
- 开启ACL会启用扩展属性 (xattr) 功能
- 开启ACL建议使用[「多主机间同步账户」](../administration/sync_accounts_between_multiple_hosts.md)