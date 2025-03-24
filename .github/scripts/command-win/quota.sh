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
    echo a | tee -a /z/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs.exe config $META_URL --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/test2 bs=1G count=1
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /z/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log

    rm /z/test1 -rf
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /z/test3 2>error.log && echo "echo should fail on out of space" && exit 1 || true

    ./juicefs.exe rmr /z/.trash
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /z/test3 

    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    ln /z/test2 /z/test4
    ln /z/test2 /z/test5
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
    grep "No space left on device" error.log
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

test_nested_dir(){
    prepare_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    file_count=1000
    mkdir -p /z/d1/{d1,d2,d3,d4,d5,d6}/{d1,d2,d3,d4,d5,d6}/{d1,d2,d3,d4,d5,d6}
    dir_count=$(find /z/d1 -type d | wc -l)
    echo "dir_count: $dir_count"
    ./juicefs.exe quota set $META_URL --path /d1 --inodes $((file_count+dir_count-1))
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    for i in $(seq 1 $file_count); do
        subdir=$(find /z/d1/ -type d | shuf -n 1)
        echo "touch $subdir/test$i" && touch $subdir/test$i
    done
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    subdir=$(find /z/d1/ -type d | shuf -n 1)
    touch $subdir/test 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs.exe quota set $META_URL --path /d1 --inodes $((file_count+dir_count))
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    subdir=$(find /z/d1/ -type d | shuf -n 1)
    touch $subdir/test
}

test_remove_and_restore(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /z/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm /z/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "0%" ]] && echo "used should be 0%" && exit 1 || true

    trash_dir=$(ls /z/.trash)
    ./juicefs.exe restore $META_URL $trash_dir --put-back
    ./juicefs.exe quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    echo a | tee -a /z/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm /z/d/test1 -rf
    dd if=/dev/zero of=/z/d/test2 bs=1M count=1
    trash_dir=$(ls /z/.trash)
    ./juicefs.exe restore $META_URL $trash_dir --put-back 2>&1 | tee restore.log
    grep "disk quota exceeded" restore.log || (echo "check restore log failed" && exit 1)
}

test_dir_capacity(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL /z --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota get $META_URL --path /d
    used=$(./juicefs.exe quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /z/d/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs.exe quota set $META_URL --path /d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test2 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /z/d/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf /z/d/test1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    used=$(./juicefs.exe quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "50%" ]] && echo "used should be 50%" && exit 1 || true
    dd if=/dev/zero of=/z/d/test3 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota check $META_URL --path /d --strict
}

test_dir_inodes(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL /z --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path /d --inodes 1000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1..1000}; do
        echo $i > /z/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee /z/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf error.log
    ./juicefs.exe quota set $META_URL --path /d --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee  /z/d/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /z/d/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /z/d/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee  /z/d/test2001
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota check $META_URL --path /d --strict
}

test_sub_dir(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path /d --inodes 1000 --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    ./juicefs.exe umount z:
    ./juicefs.exe mount -d $META_URL --subdir /d z: --heartbeat 2
    size=$(df -h /z | grep "JuiceFS" | awk '{print $2}')
    [[ $size != "1.0G" ]] && echo "size should be 1.0G" && exit 1 || true
    inodes=$(df -ih /z | grep "JuiceFS" | awk '{print $2}')
    [[ $inodes != "1000" ]] && echo "inodes should be 1000" && exit 1 || true
    dd if=/dev/zero of=/z/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /z/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /z/test1 -rf
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee /z/test$i > /dev/null
    done
    set -x
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo $i | tee /z/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    ./juicefs.exe quota check $META_URL --path /d --strict
}

test_hard_link(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    dd if=/dev/zero of=/z/file bs=1G count=1
    ./juicefs.exe quota set $META_URL --path /d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ln /z/file /z/d/test2
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ln /z/file /z/d/test3 2>error.log && echo "hard link should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs.exe quota check $META_URL --path /d --strict
}

test_check_and_repair_quota(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z: --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /z/d
    ./juicefs.exe quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=/z/d/test1 bs=1G count=1
    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | awk '{print $2}')
    kill -9 $pid
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    # ./juicefs.exe quota check $META_URL --path /d --strict && echo "quota check should fail" && exit 1 || true
    ./juicefs.exe quota check $META_URL --path /d --strict --repair
    ./juicefs.exe quota check $META_URL --path /d --strict
}

wait_until()
{   
    key=$1
    value=$2
    echo "wait until $key becomes $value"
    wait_seconds=15
    for i in $(seq 1 $wait_seconds); do
        if [ "$key" == "ifree" ]; then
            expect_value=$(df -ih /z | grep JuiceFS | awk '{print $4}')
        elif [ "$key" == "avail_size" ]; then
            expect_value=$(df h /z | grep JuiceFS | awk '{print $4}')
        fi
        if [ "$expect_value" == "$value" ]; then
            echo "$key becomes $value" && return 0
        fi
        echo "wait until $key becomes $value" && sleep 1s
    done
    echo "wait until $key becomes $value failed after $wait_seconds seconds" && exit 1
}

source .github/scripts/common/run_test.sh && run_test $@
