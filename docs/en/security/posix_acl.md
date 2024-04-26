---
title: POSIX ACL
description: This article introduces the POSIX ACL feature supported by JuiceFS and how to enable and use ACL permissions.
sidebar_position: 1
---

POSIX ACL (Portable Operating System Interface for Unix - Access Control List) is a type of access control mechanism in Unix-like operating systems that allows for finer-grained control over file and directory access permissions.

## Versions and Compatibility Requirements

* JuiceFS supports POSIX ACL from version 1.2 onwards;
* All versions of the client can mount volumes without ACL enabled, regardless of whether they were created by a new or old version of the client;
* Once ACL is enabled, it cannot be disabled; therefore, the `--enable-acl` option is tied to the volume.

:::caution
If you plan to use ACL functionality, it is recommended to upgrade all clients to the latest version to avoid potential issues with older versions affecting the accuracy of ACLs.
:::

## Enabling ACL

As mentioned earlier, you can enable ACL when creating a new volume or on an existing volume using a new version of the client.

### Creating a New Volume and Enabling ACL

```shell
juicefs format --enable-acl sqlite3://myjfs.db myjfs
```

### Enabling ACL on an Existing Volume

Use the `config` command to enable ACL functionality on an existing volume:

```
juicefs config --enable-acl sqlite3://myjfs.db
```

## Usage

To set ACL permissions for a file or directory, you can use the `setfacl` command, for example:

```
setfacl -m u:alice:rw- /mnt/jfs/file
```

For more detailed rules and guidelines on POSIX ACLs, please refer to:

* [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html)
* [setfacl](https://linux.die.net/man/1/setfacl)
* [JuiceFS ACL Functionality: A Detailed Explanation of Fine-Grained Permission Control](https://juicefs.com/en/blog/release-notes/juicefs-12-beta-1)

## Notes

* ACL permission checks require a [Linux kernel 4.9](https://lkml.iu.edu/hypermail/linux/kernel/1610.0/01531.html) or later;
* Enabling ACL will have an additional performance impact. However, due to memory cache optimization, most usage scenarios experience relatively low performance degradation.
