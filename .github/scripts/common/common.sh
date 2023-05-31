#!/bin/bash

set -e

umount_jfs()
{
    mp=$1
    meta_url=$2
    [[ -z "$mp" ]] && echo "mount point is empty" && exit 1
    [[ -z "$meta_url" ]] && echo "meta url is empty" && exit 1
    [[ ! -f $mp/.config ]] && echo "$mp/.config not found, $mp is not juicefs mount point" && return
    pid=$(./juicefs status $meta_url 2>/dev/null | jq --arg mp "$mp" '.Sessions[] | select(.MountPoint == $mp) | .ProcessID')
    echo "umount  $mp, pid $pid"
    umount -l $mp
    wait_mount_process_killed $pid 20
}

wait_mount_process_killed()
{   
    pid=$1
    wait_seconds=$2
    [[ -z "$pid" ]] && echo "pid is empty" && exit 1
    [[ -z "$wait_seconds" ]] && echo "wait_seconds is empty" && exit 1
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