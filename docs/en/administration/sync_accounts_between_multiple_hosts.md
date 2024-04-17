---
title: Sync Accounts between Multiple Hosts
sidebar_position: 7
slug: /sync_accounts_between_multiple_hosts
---

JuiceFS supports Unix file permission, you can manage permissions by directory or file granularity, just like a local file system.

To provide users with an intuitive and consistent permission management experience (e.g. the files accessible by user A on host X should be accessible by the same user on host Y), the same user who wants to access JuiceFS should have the same UID and GID on all hosts.

Here we provide a simple [Ansible](https://www.ansible.com/community) playbook to demonstrate how to ensure an account with same UID and GID on multiple hosts.

:::note
If you are using JuiceFS in Hadoop environment, besides sync accounts between multiple hosts, you can also specify a global user list and user group file. Please refer to [here](../deployment/hadoop_java_sdk.md#other-configurations) for more information.
:::

## Install Ansible

Select a host as a [control node](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#managed-node-requirements) which can access all hosts using `ssh` with the same privileged account like `root` or other sudo account. Then, install Ansible on this host. Refer to [Installing Ansible](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#installing-ansible) for details.

## Ensure the same account on all hosts

Create `account-sync/play.yaml` as follows:

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

Create the Ansible inventory `hosts`, which contains IP addresses of all hosts that need to create account.

Here we ensure an account `alice` with UID 1200 and group `staff` with GID 500 on 2 hosts:

```shell
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

Now the new account `alice:staff` has been created on these 2 hosts.

If the specified UID or GID has been allocated to another user or group on some hosts, the creation would fail.

```shell
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

In the above example, the group ID 1000 has been allocated to another group on host `172.16.255.180`. So we should **change the GID**  or **delete the group with GID 1000** on host `172.16.255.180`, and then run the playbook again.

:::caution
If the UID / GID of an existing user is changed, the user may lose permissions to previously accessible files. For example:

```shell
$ ls -l /tmp/hello.txt
-rw-r--r-- 1 alice staff 6 Apr 26 21:43 /tmp/hello.txt
$ id alice
uid=1200(alice) gid=500(staff) groups=500(staff)
```

We change the UID of alice from 1200 to 1201

```shell
~/account-sync$ ansible-playbook -i hosts -u root --ssh-extra-args "-o StrictHostKeyChecking=no" \
--extra-vars "group=staff gid=500 user=alice uid=1201" play.yaml
```

Now we have no permission to remove this file as its owner is not alice:

```shell
$ ls -l /tmp/hello.txt
-rw-r--r-- 1 1200 staff 6 Apr 26 21:43 /tmp/hello.txt
$ rm /tmp/hello.txt
rm: remove write-protected regular file '/tmp/hello.txt'? y
rm: cannot remove '/tmp/hello.txt': Operation not permitted
```

:::
