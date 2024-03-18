---
sidebar_position: 1
---

# POSIX ACL

Version 1.2 supports POSIX ACL. For detailed rules, please refer to:

- [POSIX Access Control Lists on Linux](https://www.usenix.org/legacy/publications/library/proceedings/usenix03/tech/freenix03/full_papers/gruenbacher/gruenbacher_html/main.html#:~:text=Access%20Check%20Algorithm&text=The%20ACL%20entries%20are%20looked,matching%20entry%20contains%20sufficient%20permissions.)
- [setfacl](https://linux.die.net/man/1/setfacl)

## Usage

<!-- markdownlint-disable MD044 enhanced-proper-names -->

Currently, once ACL is enabled, it cannot be disabled.  
Therefore, the --enable-acl flag is associated with the volume.

- Create a new volume

```shell
juicefs format sqlite3://myjfs.db myjfs --enable-acl
```

- Modify the configuration of an existing volume

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

- Enabling ACL increases the minimum client version requirement to v1.2.
- Enabling ACL may have additional performance implications.
For scenarios with infrequent ACL changes,
the impact is minimal with memory cache optimization.
- Enabling ACL will activate extended attributes (xattr) functionality.
- Enabling ACL is recommended for using ["Sync Accounts between Multiple Hosts"](../administration/sync_accounts_between_multiple_hosts.md)
