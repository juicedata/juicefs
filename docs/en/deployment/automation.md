---
title: Automated Deployment
sidebar_position: 7
---

Automated deployment is recommended when JuiceFS Client is to be installed on a large number of hosts.

Below examples only demonstrate the mount process, you should [Create a file system](../getting-started/standalone.md#juicefs-format) before getting started.

## Ansible

Below is the [Ansible](https://ansible.com) example to install and mount JuiceFS in localhost:

```yaml
- hosts: localhost
  tasks:
    - set_fact:
        # Change accordingly
        meta_url: sqlite3:///tmp/myjfs.db
        jfs_path: /jfs
        jfs_pkg: /tmp/juicefs-ce.tar.gz
        jfs_bin_dir: /usr/local/bin

    - get_url:
        # Change download URL accordingly
        url: https://d.juicefs.com/juicefs/releases/download/v1.0.2/juicefs-1.0.2-linux-amd64.tar.gz
        dest: "{{jfs_pkg}}"

    - ansible.builtin.unarchive:
        src: "{{jfs_pkg}}"
        dest: "{{jfs_bin_dir}}"
        include:
          - juicefs

    - name: Create symbolic for fstab
      ansible.builtin.file:
        src: "{{jfs_bin_dir}}/juicefs"
        dest: "/sbin/mount.juicefs"
        state: link

    - name: Mount JuiceFS and create fstab entry
      mount:
        path: "{{jfs_path}}"
        src: "{{meta_url}}"
        fstype: juicefs
        opts: _netdev
        state: mounted
```
