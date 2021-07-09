# JuiceFS 多主机间同步账户

JuiceFS 支持 POSIX 兼容的 ACL，以目录或文件的粒度管理权限。该行为与本地文件系统相同。

为了让用户获得直观的权限管理体验（例如，用户 A 在主机 X 中访问的文件，在主机 Y 中也应该可以用相同的用户身份访问），想要访问 JuiceFS 存储的同一个用户，应该在所有主机上具有相同的 UID 和 GID。

在这里，我们提供了一个简单的 [Ansible](https://www.ansible.com/community) playbook 来演示如何确保一个帐户在多个主机上具有相同的 UID 和 GID。

> **注意**：除了在多主机间同步账户以外，也可以指定一个全局的用户列表和所属用户组文件，具体请参见[这里](hadoop_java_sdk.md#其他配置)。

## 安装 Ansible

选择一个主机作为 [控制节点](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#managed-node-requirements)，它可以使用 `ssh` 以 `root` 或其他在 sudo 用户组的身份，访问所有。在此主机上安装 Ansible。阅读 [安装 Ansible](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#installing-ansible) 了解更多安装细节。

## 确保所有主机上的帐户相同

创建一个空目录 `account-sync` ，将下面的内容保存在该目录下的 `play.yaml` 中。

```yaml
---
- hosts: all
  tasks:
    - name: "Ensure group {{ group }} with gid {{ gid }} exists"
      group:
        name: "{{ group }}"
        gid: "{{ gid }}"
        state: present

    - name: "Ensure user {{ user }} with uid {{ uid }} exists"
      user:
        name: "{{ user }}"
        uid: "{{ uid }}"
        group: "{{ gid }}"
        state: present
```

在该目录下创建一个名为 `hosts` 的文件，将所有需要创建账号的主机的 IP 地址放置在该文件中，每行一个 IP。

在这里，我们确保在 2 台主机上使用 UID 1200 的帐户 `alice` 和 GID 500 的 `staff` 组：

```
~/account-sync$ cat hosts
172.16.255.163
172.16.255.180
~/account-sync$ ansible-playbook -i hosts -u root --ssh-extra-args "-o StrictHostKeyChecking=no" \
--extra-vars "group=staff gid=500 user=alice uid=1200" play.yaml

PLAY [all] ************************************************************************************************

TASK [Gathering Facts] ************************************************************************************
ok: [172.16.255.180]
ok: [172.16.255.163]

TASK [Ensure group staff with gid 500 exists] *************************************************************
ok: [172.16.255.163]
ok: [172.16.255.180]

TASK [Ensure user alice with uid 1200 exists] *************************************************************
changed: [172.16.255.180]
changed: [172.16.255.163]

PLAY RECAP ************************************************************************************************
172.16.255.163             : ok=3    changed=1    unreachable=0    failed=0
172.16.255.180             : ok=3    changed=1    unreachable=0    failed=0
```

现在已经在这 2 台主机上创建了新帐户 `alice:staff`。

如果指定的 UID 或 GID 已分配给某些主机上的另一个用户或组，则创建将失败。

```
~/account-sync$ ansible-playbook -i hosts -u root --ssh-extra-args "-o StrictHostKeyChecking=no" \
--extra-vars "group=ubuntu gid=1000 user=ubuntu uid=1000" play.yaml

PLAY [all] ************************************************************************************************

TASK [Gathering Facts] ************************************************************************************
ok: [172.16.255.180]
ok: [172.16.255.163]

TASK [Ensure group ubuntu with gid 1000 exists] ***********************************************************
ok: [172.16.255.163]
fatal: [172.16.255.180]: FAILED! => {"changed": false, "msg": "groupmod: GID '1000' already exists\n", "name": "ubuntu"}

TASK [Ensure user ubuntu with uid 1000 exists] ************************************************************
ok: [172.16.255.163]
	to retry, use: --limit @/home/ubuntu/account-sync/play.retry

PLAY RECAP ************************************************************************************************
172.16.255.163             : ok=3    changed=0    unreachable=0    failed=0
172.16.255.180             : ok=1    changed=0    unreachable=0    failed=1
```

在上面的示例中，组 ID 1000 已分配给主机 `172.16.255.180` 上的另一个组，我们应该 **更改 GID** 或 **删除主机 `172.16.255.180` 上 GID 为 1000** 的组，然后再次运行 playbook。



> **小心**
>
> 如果用户帐户已经存在于主机上，并且我们将其更改为另一个 UID 或 GID 值，则用户可能会失去对他们以前拥有的文件和目录的权限。例如：
>
> ```
> $ ls -l /tmp/hello.txt
> -rw-r--r-- 1 alice staff 6 Apr 26 21:43 /tmp/hello.txt
> $ id alice
> uid=1200(alice) gid=500(staff) groups=500(staff)
> ```
>
> 我们将 alice 的 UID 从 1200 改为 1201
>
> ```
> ~/account-sync$ ansible-playbook -i hosts -u root --ssh-extra-args "-o StrictHostKeyChecking=no" \
> --extra-vars "group=staff gid=500 user=alice uid=1201" play.yaml
> ```
>
> 现在我们没有权限删除这个文件，因为它的所有者不是 alice：
>
> ```
> $ ls -l /tmp/hello.txt
> -rw-r--r-- 1 1200 staff 6 Apr 26 21:43 /tmp/hello.txt
> $ rm /tmp/hello.txt
> rm: remove write-protected regular file '/tmp/hello.txt'? y
> rm: cannot remove '/tmp/hello.txt': Operation not permitted
> ```
