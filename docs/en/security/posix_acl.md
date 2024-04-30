---
title: POSIX ACLs
description: Learn about POSIX ACL support in JuiceFS and how to enable and use ACL permissions.
sidebar_position: 1
---

POSIX ACLs (Portable Operating System Interface for Unix - Access Control Lists) are an access control mechanism in Unix-like operating systems that allows for finer-grained control over file and directory access permissions.

This document introduces how to enable and use POSIX ACL permissions in JuiceFS.

## Versions and compatibility requirements

* Since version 1.2, JuiceFS has supported POSIX ACLs.
* All client versions can mount volumes without ACLs enabled, regardless of their creation by new or old client versions.
* Once ACLs are enabled, they cannot be disabled. Therefore, the `--enable-acl` option is tied to the volume.

:::caution
If you plan to use ACL functionality, it is recommended to upgrade all clients to the latest version to avoid potential issues with older versions affecting the accuracy of ACLs.
:::

## Enable ACLs

As mentioned earlier, you can enable ACLs when creating a new volume or on an existing volume using a new version of the client.

### Create a new volume and enable ACLs

Execute the following command to create a new volume and enable ACLs:

```shell
juicefs format --enable-acl sqlite3://myjfs.db myjfs
```

### Enable ACLs on an existing volume

Use the `config` command to enable ACL functionality on an existing volume:

```
juicefs config --enable-acl sqlite3://myjfs.db
```

## Usage

To set ACL permissions for a file or directory, you can use the `setfacl` command, for example:

```
setfacl -m u:alice:rw- /mnt/jfs/file
```

For detailed rules, guidelines, and implementation of POSIX ACLs, see:

* [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html)
* [setfacl](https://linux.die.net/man/1/setfacl)
* [How We Optimized ACL Implementation for Minimal Performance Impact](https://juicefs.com/en/blog/engineering/access-control-list)

## Notes

* ACL permission checks require [Linux kernel 4.9](https://lkml.iu.edu/hypermail/linux/kernel/1610.0/01531.html) or later.
* Enabling ACLs may impact performance. However, due to memory cache optimization, most usage scenarios experience minimal performance degradation.
