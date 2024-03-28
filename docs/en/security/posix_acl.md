---
sidebar_position: 1
---

# POSIX ACL

Version 1.2 supports POSIX ACL. For detailed rules, please refer to:

- [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html)
- [setfacl](https://linux.die.net/man/1/setfacl)

## Usage

<!-- markdownlint-disable MD044 enhanced-proper-names -->

Currently, once ACL is enabled, it cannot be disabled.  
Therefore, the --enable-acl flag is associated with the volume.

### Enable ACL for new volumes

```shell
juicefs format sqlite3://myjfs.db myjfs --enable-acl
```

### Enable ACl for existing volumes

- Upgrade all old client to v1.2 and remount it.
- Use the following command with v1.2 client to change the volume configuration.

```shell
juicefs config sqlite3://myjfs.db --enable-acl
```

<!-- markdownlint-enable MD044 enhanced-proper-names -->

## Compatibility

- New client versions are compatible with old volume versions.
- Old client versions are compatible with new volume versions (without ACL enabled).

:::caution Note
If ACL is enabled, it is recommended that all clients to be upgraded.
If an old client mounts a new volume (without ACL enabled),
and ACL is subsequently enabled on the volume,
operations by the old client may impact the correctness of ACL.
:::

## Others

- ACL permission checks are supported only in Linux kernel 4.9 and higher versions. You can refer to this [documentation](https://lkml.iu.edu/hypermail/linux/kernel/1610.0/01531.html) for more details.
- Enabling ACL increases the minimum client version requirement to v1.2.
- Enabling ACL may have additional performance implications.
For scenarios with infrequent ACL changes,
the impact is minimal with memory cache optimization.
