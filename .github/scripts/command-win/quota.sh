#!/bin/bash -e

[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1

HEARTBEAT_INTERVAL=3
HEARTBEAT_SLEEP=3
DIR_QUOTA_FLUSH_INTERVAL=4
VOLUME_QUOTA_FLUSH_INTERVAL=2
source .github/scripts/common/common_win.sh

test_total_capacity()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs --capacity 1
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL --debug
    dd if=/dev/zero of=/z/test1 bs=1G count=1
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    dd if=/dev/zero of=/z/test2 bs=1G count=1  && echo "dd should fail on out of space" && exit 1 || true
    rm /z/test1 -rf
    ./juicefs.exe rmr /z/.trash
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    dd if=/dev/zero of=/z/test2 bs=104857600 count=1
}

test_total_inodes(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs --inodes 1000
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee /z/test$i > /dev/null
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee /z/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
 #   grep "No space left on device" error.log
    ./juicefs.exe config $META_URL --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee /z/test$i > /dev/null || (df -i /z && ls /z/ -l | wc -l  && exit 1)
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee /z/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
}

test_remove_and_restore(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path //d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path //d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1 && echo "write should fail on out of space" && exit 1 || true
    echo "remove test1" && rm /z/d/test* -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path //d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "0%" ]] && echo "used should be 0%" && exit 1 || true

    trash_dir=$(ls /z/.trash)
    ./juicefs.exe restore $META_URL $trash_dir --put-back
    ./juicefs.exe quota get $META_URL --path //d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1 && echo "write should fail on out of space" && exit 1 || true
    echo "remove test1" && rm /z/d/test1 -rf
}

test_dir_capacity(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path //d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path //d
    used=$(./juicefs.exe quota get $META_URL --path //d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1 && echo "echo should fail on out of space" && exit 1 || true

    ./juicefs.exe quota set $META_URL --path //d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    dd if=/dev/zero of=/z/d/test3 bs=1G count=1 && echo "echo should fail on out of space" && exit 1 || true
    rm -rf /z/d/test1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    used=$(./juicefs.exe quota get $META_URL --path //d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "50%" ]] && echo "used should be 50%" && exit 1 || true
    dd if=/dev/zero of=/z/d/test3 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota check $META_URL --path //d --strict
}

test_dir_inodes(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path //d --inodes 1000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1..1000}; do
        echo $i > /z/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee /jfs/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    #grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf error.log
    ./juicefs.exe quota set $META_URL --path //d --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee  /z/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /z/d/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    #grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /z/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /z/d/test2001
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota check $META_URL --path //d --strict
}

source .github/scripts/common/run_test.sh && run_test $@
