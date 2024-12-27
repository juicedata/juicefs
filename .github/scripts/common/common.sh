#!/bin/bash -e
dpkg -s jq >/dev/null 2>&1 || .github/scripts/apt_install.sh jq
prepare_test()
{
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
}

umount_jfs()
{
    mp=$1
    meta_url=$2
    [[ -z "$mp" ]] && echo "mount point is empty" && exit 1
    [[ -z "$meta_url" ]] && echo "meta url is empty" && exit 1
    echo "umount_jfs $mp $meta_url"
    [[ ! -f $mp/.config ]] && return
    ls -l $mp/.config
    pids=$(./juicefs status --log-level error $meta_url 2>/dev/null |tee status.log| jq --arg mp "$mp" '.Sessions[] | select(.MountPoint == $mp) | .ProcessID')
    [[ -z "$pids" ]] && cat status.log && echo "pid is empty" && return
    echo "umount is $mp, pids is $pids"
    for pid in $pids; do
        umount -l $mp
    done
    for pid in $pids; do
        wait_mount_process_killed $pid 60
    done    
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

compare_md5sum(){
    file1=$1
    file2=$2
    md51=$(md5sum $file1 | awk '{print $1}')
    md52=$(md5sum $file2 | awk '{print $1}')
    # echo md51 is $md51, md52 is $md52
    if [ "$md51" != "$md52" ] ; then
        echo "md5 are different: md51 is $md51, md52 is $md52"
        exit 1
    fi
}


wait_command_success()
{
    command=$1
    expected=$2
    timeout=$3
    [[ -z "$timeout" ]] && timeout=30
    echo "wait_command_success command=$command, expected=$expected, timeout=$timeout"
    for i in $(seq 1 $timeout); do
        result=$(eval "$command")
        echo result is $result
        if [[ "$result" == "$expected" ]]; then
            echo "command success"
            break
        fi
        if [ $i -eq $timeout ]; then
            eval "$command"
            echo "command failed after $timeout: $command"
            exit 1
        fi
        echo "wait command to success in $i sec..." && sleep 1
    done
}