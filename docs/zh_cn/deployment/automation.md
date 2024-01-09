---
title: 自动化部署
sidebar_position: 7
---

面对大量节点需要安装并挂载 JuiceFS 时，可以用本章介绍的方法进行自动化部署。

下方示范仅用于挂载，因此你需要提前[创建好 JuiceFS 文件系统](../getting-started/standalone.md#juicefs-format)。

## Ansible

使用 [Ansible](https://ansible.com) 在本机挂载 JuiceFS 文件系统的 playbook 样例如下：

```yaml
- hosts: localhost
  tasks:
    - set_fact:
        # 根据实际情况修改
        meta_url: sqlite3:///tmp/myjfs.db
        jfs_path: /jfs
        jfs_pkg: /tmp/juicefs-ce.tar.gz
        jfs_bin_dir: /usr/local/bin

    - get_url:
        # 根据实际情况替换成需要的下载链接
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
