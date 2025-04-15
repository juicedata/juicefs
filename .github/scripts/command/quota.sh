#!/bin/bash -e

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

HEARTBEAT_INTERVAL=3
HEARTBEAT_SLEEP=3
DIR_QUOTA_FLUSH_INTERVAL=4
VOLUME_QUOTA_FLUSH_INTERVAL=2
source .github/scripts/common/common.sh

test_total_capacity()
{
    prepare_test
    ./juicefs format $META_URL myjfs --capacity 1
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL --debug
    dd if=/dev/zero of=/jfs/test1 bs=1G count=1
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config $META_URL --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/test2 bs=1G count=1
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log

    rm /jfs/test1 -rf
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/test3 2>error.log && echo "echo should fail on out of space" && exit 1 || true

    ./juicefs rmr /jfs/.trash
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/test3 

    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    ln /jfs/test2 /jfs/test4
    ln /jfs/test2 /jfs/test5
}

test_total_inodes(){
    prepare_test
    ./juicefs format $META_URL myjfs --inodes 1000
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee /jfs/test$i > /dev/null
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee /jfs/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config $META_URL --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee /jfs/test$i > /dev/null || (df -i /jfs && ls /jfs/ -l | wc -l  && exit 1)
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee /jfs/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
}

test_nested_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    file_count=1000
    mkdir -p /jfs/d1/{d1,d2,d3,d4,d5,d6}/{d1,d2,d3,d4,d5,d6}/{d1,d2,d3,d4,d5,d6}
    dir_count=$(find /jfs/d1 -type d | wc -l)
    echo "dir_count: $dir_count"
    ./juicefs quota set $META_URL --path /d1 --inodes $((file_count+dir_count-1))
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    for i in $(seq 1 $file_count); do
        subdir=$(find /jfs/d1/ -type d | shuf -n 1)
        echo "touch $subdir/test$i" && touch $subdir/test$i
    done
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    subdir=$(find /jfs/d1/ -type d | shuf -n 1)
    touch $subdir/test 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs quota set $META_URL --path /d1 --inodes $((file_count+dir_count))
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    subdir=$(find /jfs/d1/ -type d | shuf -n 1)
    touch $subdir/test
}

test_remove_and_restore(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /jfs/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm /jfs/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "0%" ]] && echo "used should be 0%" && exit 1 || true

    trash_dir=$(ls /jfs/.trash)
    ./juicefs restore $META_URL $trash_dir --put-back
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    echo a | tee -a /jfs/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm /jfs/d/test1 -rf
    dd if=/dev/zero of=/jfs/d/test2 bs=1M count=1
    trash_dir=$(ls /jfs/.trash)
    ./juicefs restore $META_URL $trash_dir --put-back 2>&1 | tee restore.log
    grep "disk quota exceeded" restore.log || (echo "check restore log failed" && exit 1)
}

test_dir_capacity(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /jfs/d/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs quota set $META_URL --path /d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/d/test2 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/d/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf /jfs/d/test1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "50%" ]] && echo "used should be 50%" && exit 1 || true
    dd if=/dev/zero of=/jfs/d/test3 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --path /d --strict
}

test_dir_inodes(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1..1000}; do
        echo $i > /jfs/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee /jfs/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf error.log
    ./juicefs quota set $META_URL --path /d --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee  /jfs/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /jfs/d/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /jfs/d/test2001
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --path /d --strict
}

test_sub_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000 --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    umount_jfs /jfs $META_URL
    ./juicefs mount -d $META_URL --subdir /d /jfs --heartbeat 2
    size=$(df -h /jfs | grep "JuiceFS" | awk '{print $2}')
    [[ $size != "1.0G" ]] && echo "size should be 1.0G" && exit 1 || true
    inodes=$(df -ih /jfs | grep "JuiceFS" | awk '{print $2}')
    [[ $inodes != "1000" ]] && echo "inodes should be 1000" && exit 1 || true
    dd if=/dev/zero of=/jfs/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee /jfs/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo $i | tee /jfs/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    ./juicefs quota check $META_URL --path /d --strict
}

test_dump_load(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000 --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    ./juicefs dump --log-level error $META_URL --fast > dump.json
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount $META_URL /jfs -d --heartbeat 5
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee /jfs/d/test$i > /dev/null
    done
    set -x
    sleep 3s
    echo a | tee /jfs/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    ./juicefs quota check $META_URL --path /d --strict
}

test_hard_link(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    dd if=/dev/zero of=/jfs/file bs=1G count=1
    ./juicefs quota set $META_URL --path /d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ln /jfs/file /jfs/d/test2
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ln /jfs/file /jfs/d/test3 2>error.log && echo "hard link should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --path /d --strict
}

test_check_and_repair_quota(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | awk '{print $2}')
    kill -9 $pid
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    # ./juicefs quota check $META_URL --path /d --strict && echo "quota check should fail" && exit 1 || true
    ./juicefs quota check $META_URL --path /d --strict --repair
    ./juicefs quota check $META_URL --path /d --strict
}

wait_until()
{   
    key=$1
    value=$2
    echo "wait until $key becomes $value"
    wait_seconds=15
    for i in $(seq 1 $wait_seconds); do
        if [ "$key" == "ifree" ]; then
            expect_value=$(df -ih /jfs | grep JuiceFS | awk '{print $4}')
        elif [ "$key" == "avail_size" ]; then
            expect_value=$(df h /jfs | grep JuiceFS | awk '{print $4}')
        fi
        if [ "$expect_value" == "$value" ]; then
            echo "$key becomes $value" && return 0
        fi
        echo "wait until $key becomes $value" && sleep 1s
    done
    echo "wait until $key becomes $value failed after $wait_seconds seconds" && exit 1
}

source .github/scripts/common/run_test.sh && run_test $@
