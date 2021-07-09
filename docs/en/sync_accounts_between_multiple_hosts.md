# Sync Accounts between Multiple Hosts

JuiceFS supports POSIX compatible ACL to manage permissions in the granularity of directory or file. The behavior is the same as a local file system.

In order to make the permission experience intuitive to user (e.g. the files accessible by user A in host X should be accessible in host Y with the same user), the same user who want to access JuiceFS should have the same UID and GID on all hosts.

Here we provide a simple [Ansible](https://www.ansible.com/community) playbook to demonstrate how to ensure an account with same UID and GID on multiple hosts.

> **Note**: Besides sync accounts between multiple hosts, you can also specify a global user list and user group file, please refer to [here](hadoop_java_sdk.md#other-configurations) for more information.

## Install ansible

Select a host as a [control node](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#managed-node-requirements) which can access all hosts using `ssh` with the same privileged account like `root` or other sudo account. Install ansible on this host. Read [Installing Ansible](https://docs.ansible.com/ansible/latest/installation_guide/intro_installation.html#installing-ansible) for more installation details.



## Ensure the same account on all hosts

Create an empty directory `account-sync` , save below content in `play.yaml` under this directory.

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



Create a file named `hosts` in this directory, place IP addresses of all hosts need to create account in this file, each line with a host's IP.

Here we ensure an account `alice` with UID 1200 and  group `staff` with GID 500 on 2 hosts:

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

Now the new account `alice:staff` has been created on these 2 hosts.

If the UID or GID specified has been allocated to another user or group on some hosts, the creation would failed.

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

In above example,  the group ID 1000 has been allocated to another group on host `172.16.255.180` , we should **change the GID**  or **delete the group with GID 1000** on host `172.16.255.180` , then run the playbook again.



> **CAUTION**
>
> If the user account has already existed on the host and we change it to another UID or GID value, the user may loss permissions to the files and directories which they previously have. For example:
>
> ```
> $ ls -l /tmp/hello.txt
> -rw-r--r-- 1 alice staff 6 Apr 26 21:43 /tmp/hello.txt
> $ id alice
> uid=1200(alice) gid=500(staff) groups=500(staff)
> ```
>
> We change the UID of alice from 1200 to 1201
>
> ```
> ~/account-sync$ ansible-playbook -i hosts -u root --ssh-extra-args "-o StrictHostKeyChecking=no" \
> --extra-vars "group=staff gid=500 user=alice uid=1201" play.yaml
> ```
>
> Now we have no permission to remove this file as its owner is not alice:
>
> ```
> $ ls -l /tmp/hello.txt
> -rw-r--r-- 1 1200 staff 6 Apr 26 21:43 /tmp/hello.txt
> $ rm /tmp/hello.txt
> rm: remove write-protected regular file '/tmp/hello.txt'? y
> rm: cannot remove '/tmp/hello.txt': Operation not permitted
> ```
