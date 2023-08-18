---
title: Create Samba Shares
sidebar_position: 8
description: Learn how to share directories in the JuiceFS file system through Samba.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Samba is an open-source software suite that implements the SMB/CIFS (Server Message Block / Common Internet File System) protocol, which is a commonly used file-sharing protocol in Windows systems. With Samba, you can create shared directories on Linux/Unix servers, allowing Windows computers to access and use these shared resources over the network.

To create a shared folder on a Linux system with Samba installed, you can edit the `smb.conf` configuration file. Once configured, Windows and macOS systems can access and read/write the shared folder using their file managers. Linux needs to install the Samba client for access.

When you need to share directories from the JuiceFS file system through Samba, you can simply use the `juicefs mount` command to mount the file system. Then, you can create Samba shares with the JuiceFS mount point or subdirectories.

:::note
`juicefs mount` mounts the file system as a local user-space file system through the FUSE interface, making it identical to the local file system in terms of appearance and usage. Hence, it can be directly used to create Samba shares.
:::

## Step 1: Install Samba

Most Linux distributions provide Samba through their package managers.

<Tabs>
<TabItem value="debian" label="Debian and derivatives">

```shell
sudo apt install samba
```

</TabItem>
    <TabItem value="redhat" label="RHEL and derivatives">

```shell
sudo dnf install samba
```

</TabItem>
</Tabs>

If you need to configure AD/DC (Active Directory / Domain Controller), additional software packages need to be installed. For more details, refer to the [Samba Official Installation Guide](https://wiki.samba.org/index.php/Distribution-specific_Package_Installation).

## Step 2: Enable JuiceFS extended attribute (xattr) support

According to the [Samba official documentation](https://wiki.samba.org/index.php/File_System_Support#File_systems_without_xattr_support), it is recommended to use file systems that support extended attributes (xattr). To enable extended attribute support for JuiceFS during the mount process, use the `--enable-xattr` option. For example:

```shell
sudo juicefs mount -d --enable-xattr sqlite3://myjfs.db /mnt/myjfs
```

For cases where you configure automatic mounting through `/etc/fstab`, you can add the `enable-xattr` option to the mount options section. For example:

```ini
# <metadata engine URL> <mount point> <file system type> <mount options>
redis://127.0.0.1:6379/0 /mnt/myjfs juicefs _netdev,max-uploads=50,writeback,cache-size=1024000,enable-xattr 0 0
```

### Knowledge extension: why Samba requires file system support for extended attributes

Samba is software designed for Linux/Unix systems, serving file sharing to Windows systems. In Windows systems, many files and directories have additional metadata, for example, file authors, keywords, and icon positions. This information is typically stored outside the POSIX file system and requires xattr format for storage in Windows. To ensure that these files can be correctly stored in Linux systems, Samba recommends using file systems that support extended attributes when creating shares.

## Step 3: Create a Samba share

Assuming the JuiceFS mount point is `/mnt/myjfs`, if you want to create a Samba share for the `media` directory within it, you can configure it as follows:

```ini
[Media]
    path = /mnt/myjfs/media
    guest ok = no
    read only = no
    browseable = yes
```

## Share for macOS

Apple macOS systems support direct access to Samba shares. Similar to Windows, macOS also has additional metadata (e.g., icon positions, Spotlight search) that needs to be saved using xattr. Samba version 4.9 and above have the support for macOS extended attributes enabled by default.

If your Samba version is lower than 4.9, you need to add the `ea support = yes` option to the [global] section of the Samba configuration to enable extended attribute support for macOS. Edit the configuration file `/etc/samba/smb.conf`, for example:

```ini
[global]
    workgroup = SAMBA
    security = user
    passdb backend = tdbsam
    ea support = yes
```

## User management in Samba

Samba has its own user database, independent of the operating system users. However, since Samba shares directories from the system, appropriate user permissions are required to read and write files.

### Create Samba users

When creating users for Samba, it is required that the user already exists in the system, as Samba will automatically map the Samba user to the same-named system user with corresponding permissions.

- If the user already exists in the system, assuming the system account is "herald," you can create a Samba account for it as follows:

    ```shell
    sudo smbpasswd -a herald
    ```

    Follow the on-screen prompts to set the password. The Samba account can have a different password than the system user.

- If you need to create a new user, taking the example of creating a user named "abc":

    1. Create a user:

        ```shell
        sudo adduser abc
        ```

    2. Create a corresponding Samba user with the same name:

        ```shell
        sudo smbpasswd -a abc
        ```

### View created Samba users

`pdbedit` is a built-in tool in Samba used to manage the Samba user database. You can use this tool to list all the created Samba users:

```shell
sudo pdbedit -L
```

It will display a list of all created Samba users, including their usernames, security identifiers (SIDs), group membership, and other related information.
