#!/bin/bash -ex
source .github/scripts/common/common_win.sh

[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1

[[ -z "$SEED" ]] && SEED=$(date +%s)
HEARTBEAT_INTERVAL=2
DIR_QUOTA_FLUSH_INTERVAL=4
[[ -z "$BINARY" ]] && BINARY=false
[[ -z "$FAST" ]] && FAST=false

trap "echo random seed is $SEED" EXIT

test_dump_load_sustained_file(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs --trash-days 0
    ./juicefs.exe mount -d $META_URL z:
    file_count=100
    for i in $(seq 1 $file_count); do
        touch /z/file$i
        exec {fd}<>/z/file$i
        echo fd is $fd
        fds[$i]=$fd
        rm /z/file$i
    done
    ./juicefs.exe dump $META_URL dump.json $(get_dump_option)
    for i in $(seq 1 $file_count); do
        fd=${fds[$i]}
        exec {fd}>&-
    done
    if [[ "$BINARY" == "true" ]]; then
        sustained=$(./juicefs.exe load dump.json --binary --stat | grep sustained | awk -F"|" '{print $2}')
    else
        sustained=$(jq '.Sustained[].inodes | length' dump.json)
    fi
    echo "sustained file count: $sustained"
    ./juicefs.exe umount z:
    prepare_win_test
    ./juicefs.exe load $META_URL dump.json $(get_load_option)
    ./juicefs.exe mount -d $META_URL z:
}

test_dump_load_with_copy_file_range(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    rm -rf /tmp/test
    dd if=/dev/zero of=/tmp/test bs=1M count=1024
    cp /tmp/test /z/test
    node .github/scripts/copyFile.js /z/test /z/test1
    ./juicefs.exe dump $META_URL dump.json $(get_dump_option)
    ./juicefs.exe umount z:
    redis-cli -h 127.0.0.1 -p 6379 -n 1 FLUSHDB
    ./juicefs.exe load $META_URL dump.json $(get_load_option)
    ./juicefs.exe mount -d $META_URL z:
    compare_md5sum /tmp/test /z/test1
}

test_dump_load_with_quota(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path //d --inodes 1000 --capacity 1
    ./juicefs.exe dump --log-level error $META_URL $(get_dump_option) > dump.json
    ./juicefs.exe umount z:
    redis-cli -h 127.0.0.1 -p 6379 -n 1 FLUSHDB
    ./juicefs.exe load $META_URL dump.json $(get_load_option)
    ./juicefs.exe mount $META_URL z: -d --heartbeat $HEARTBEAT_INTERVAL
    ./juicefs.exe quota get $META_URL --path //d
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1 2>error.log && echo "write should fail on out of space" && exit 1 || true
}

get_dump_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    elif [[ "$FAST" == "true" ]]; then
        option="--fast"
    else
        option=""
    fi
    echo $option
}

get_load_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    else
        option=""
    fi
    echo $option
}

prepare_test(){
    umount_jfs /jfs $META_URL
    umount_jfs /jfs2 sqlite3://test2.db
    python3 .github/scripts/flush_meta.py $META_URL
    rm test2.db -rf 
    rm -rf /var/jfs/myjfs || true
    mc rm --force --recursive myminio/test || true
}

source .github/scripts/common/run_test.sh && run_test $@
