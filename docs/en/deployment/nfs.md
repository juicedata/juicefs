---
title: Create NFS Shares
sidebar_position: 9
description: Learn how to use the NFS protocol to share directories within the JuiceFS file system.
---

NFS (Network File System) is a network file-sharing protocol that allows different computers to share files and directories over a network. It was originally developed by Sun Microsystems and is a standard way of file sharing between Unix and Unix-like systems. The NFS protocol enables clients to access remote file systems as if they were local, achieving transparent remote file access.

When you need to share directories from the JuiceFS file system through NFS, you can simply use the `juicefs mount` command to mount the file system. Then, you can create NFS shares with the JuiceFS mount point or subdirectories.

:::note
`juicefs mount` mounts the file system as a local user-space file system through the FUSE interface, making it identical to the local file system in terms of appearance and usage. Hence, it can be directly used to create NFS shares.
:::

## Step 1. Install NFS

To configure NFS shares, you need to install the relevant software packages on both the server and client sides. Let's take Ubuntu/Debian systems as an example:

### 1. Server-side installation

Create a host for NFS sharing (with the JuiceFS file system also mounted on this server).

```shell
sudo apt install nfs-kernel-server
```

### 2. Client-side installation

All Linux hosts that need to access NFS shares should install the client software.

```shell
sudo apt install nfs-common
```

## Step 2. Create shares

Assuming the JuiceFS is mounted on the server system at the path `/mnt/myjfs`, if you want to set the `media` subdirectory as an NFS share, you can add the following configuration to the `/etc/exports` file on the server system:

```
"/mnt/myjfs/media" *(rw,sync,no_subtree_check,fsid=1)
```

The syntax for NFS share configuration is as follows:

```
<Share Path> <Allowed IPs>(options)
```

For example, if you want to restrict the mounting of this share to hosts in the `192.168.1.0/24` IP range and avoid squashing root privileges, you can modify it as follows:

```
"/mnt/myjfs/media" 192.168.1.0/24(rw,async,no_subtree_check,no_root_squash,fsid=1)
```

### Share option description

**Explanation of the share options:**

- `rw`: Represents read and write permissions. If read-only access is desired, use `ro`.
- `sync` and `async`: `sync` enables synchronous writes, meaning that when writing to the NFS share, the client waits for the server's confirmation of successful data write before proceeding with subsequent operations. `async`, on the other hand, allows asynchronous writes. In this mode, the client does not wait for the server's confirmation of successful write before proceeding with subsequent operations.
- `no_subtree_check`: Disables subtree checking, allowing clients to mount both the parent and child directories of the NFS share. This can reduce some security but improve NFS compatibility. Setting it to `subtree_check` enables subtree checking, allowing clients to only mount the NFS share and its subdirectories.
- `no_root_squash`: Controls the mapping behavior of the client's root user when accessing the NFS share. By default, when the client mounts the NFS share as root, the server maps it to a non-privileged user (usually nobody or nfsnobody), which is known as root squashing. Enabling this option cancels the root squashing, giving the client the same root user privileges as the server. This option comes with certain security risks and should be used with caution.
- `fsid`: A file system identifier used to identify different file systems on NFS. In NFSv4, the root directory of NFS is defined as fsid=0, and other file systems need to be numbered uniquely under it. Here, JuiceFS is an externally mounted FUSE file system, so it needs to be assigned a unique identifier.

### Choosing between async and sync modes

For NFS shares, the sync (synchronous writes) mode can improve data reliability but always requires waiting for the server's confirmation before proceeding with the next operation. This may result in lower write performance. For JuiceFS, which is a cloud-based distributed file system, network latency also needs to be considered. Using the sync mode can often lead to lower write performance due to network latency.

In most cases, when creating NFS shares with JuiceFS, it is recommended to set the write mode to async (asynchronous writes) to avoid sacrificing write performance. If data reliability must be prioritized and sync mode is necessary, it is recommended to configure JuiceFS with a high-performance SSD as a local cache with sufficient capacity and enable the writeback cache mode.
