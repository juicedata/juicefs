#!/bin/bash

set -e

umount_jfs()
{
    mp=$1
    [[ -z "$mp" ]] && mp=/jfs
    [[ ! -f $mp/.config ]] && echo "$mp/.config not found, $mp is not juicefs mount point" && return
    pid=$(./juicefs status sqlite3://test.db | jq --arg mp "$mp" '.Sessions[] | select(.MountPoint == $mp) | .ProcessID')
    echo "umount  $mp, pid $pid"
    umount -l $mp
    wait_mount_process_killed 20
}

wait_mount_process_killed()
{   
    pid=$1
    wait_seconds=$2
    [[ -z "$pid" ]] && echo "pid is empty" && exit 1
    echo "wait mount process $pid exit in $wait_seconds seconds"
    for i in $(seq 1 $wait_seconds); do
        count=$(ps -ef | grep "juicefs mount" | awk '{print $2}'| grep ^$pid$ | wc -l)
        echo $i, mount process count is $count
        if [ $count -eq 0 ]; then
            echo "mount process is killed"
            break
        fi
        if [ $i -eq $wait_seconds ]; then
            ps -ef | grep "juicefs mount" | grep -v "grep"
            echo "<FATAL>: mount process is not killed after $wait_seconds"
            exit 1
        fi
        echo "wait mount process to be killed..." && sleep 1s
    done
}