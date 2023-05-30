#!/bin/bash
set -e

test_total_capacity()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs --capacity 1
    ./juicefs mount -d sqlite3://test.db /jfs --heartbeat 5 --debug
    dd if=/dev/zero of=/jfs/test1 bs=1G count=1
    sleep 10s
    echo a | tee -a /jfs/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config sqlite3://test.db --capacity 2
    sleep 6s
    dd if=/dev/zero of=/jfs/test2 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log

    rm /jfs/test1 -rf
    sleep 3s
    echo a | tee -a /jfs/test3 2>error.log && echo "echo should fail on out of space" && exit 1 || true

    ./juicefs rmr /jfs/.trash
    sleep 15s
    echo a | tee -a /jfs/test3 

    sleep 3s
    ln /jfs/test2 /jfs/test4
    ln /jfs/test2 /jfs/test5
}

skip_test_total_inodes(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs --inodes 1000
    ./juicefs mount -d sqlite3://test.db /jfs --heartbeat 5
    for i in {1..1000}; do
        touch /jfs/test$i
    done
    sleep 10s
    # TODO: fix this
    # wait_until ifree 0
    touch /jfs/test1001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config sqlite3://test.db --inodes 2000
    sleep 6s
    for i in {1001..2000}; do
        touch /jfs/test$i
    done
    sleep 10s
    # wait_until ifree 0
    touch /jfs/test2001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
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

test_dir_capacity(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs --heartbeat 4
    mkdir -p /jfs/d
    ./juicefs quota set sqlite3://test.db --path /d --capacity 1
    sleep 5s
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep 5s
    ./juicefs quota get sqlite3://test.db --path /d
    used=$(./juicefs quota get sqlite3://test.db --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /jfs/d/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log

    ./juicefs quota set sqlite3://test.db /d --capacity 2
    dd if=/dev/zero of=/jfs/d/test2 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs/d/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    rm -rf /jfs/d/test1
    sleep 3s
    used=$(./juicefs quota get sqlite3://test.db --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ used != "50%" ]] && echo "used should be 100%" && exit 1 || true
    dd if=/dev/zero of=/jfs/d/test3 bs=1G count=1
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

test_dir_inodes(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    mkdir -p /jfs/d
    ./juicefs quota set sqlite3://test.db --path /d --inodes 1000
    for i in {1..1000}; do
        touch /jfs/d/test$i
    done
    sleep 3s
    touch /jfs/test1001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    rm -rf error.log
    ./juicefs quota set sqlite3://test.db --path /d --inodes 2000
    for i in {1001..2000}; do
        touch /jfs/test$i
    done
    sleep 3s
    touch /jfs/test2001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
    rm /jfs/test1 -rf 
    touch /jfs/test2001
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

test_sub_dir(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    mkdir -p /jfs/d
    ./juicefs quota set sqlite3://test.db --path /d --inodes 1000 --capacity 1
    ./juicefs mount -d sqlite3://test.db --subdir /d /jfs2
    size=$(df -h /jfs2 | grep "JuiceFS" | awk '{print $2}')
    [[ $size != "1.0G" ]] && echo "size should be 1.0G" && exit 1 || true
    inodes=$(df -ih /jfs2 | grep "JuiceFS" | awk '{print $2}')
    [[ $inodes != "1000" ]] && echo "inodes should be 1000" && exit 1 || true
    dd if=/dev/zero of=/jfs2/test1 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs2/test1 2>error.log && echo "dd should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    for i in {1..1000}; do
        touch /jfs2/test$i
    done
    sleep 3s
    touch /jfs2/test1001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

test_dump_load(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    mkdir -p /jfs/d
    ./juicefs quota set sqlite3://test.db --path /d --inodes 1000 --capacity 1
    ./juicefs dump sqlite3://test.db > dump.json
    rm -rf test2.db
    ./juicefs load sqlite3://test2.db dump.json
    ./juicefs mount sqlite3://test2.db /jfs2 -d
    dd if=/dev/zero of=/jfs2/d/test1 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs2/d/test1 2>error.log && echo "dd should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    for i in {1..1000}; do
        touch /jfs2/test$i
    done
    sleep 3s
    touch /jfs2/test1001 2>error.log && echo "touch should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

test_hard_link(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    mkdir -p /jfs/d
    echo a>/jfs/file
    ./juicefs quota set sqlite3://test.db --path /d --capacity 1
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep 3s
    ln /jfs/file /jfs/d/test2 2>error.log && echo "ln should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

test_check_and_repair_quota(){
    prepare_test
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    mkdir -p /jfs/d
    ./juicefs quota set sqlite3://test.db --path /d --capacity 1
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | awk '{print $2}')
    kill -9 $pid
    sleep 3s
    ./juicefs quota check sqlite3://test.db --path /d --strict && echo "quota check should fail" && exit 1 || true
    ./juicefs quota check sqlite3://test.db --path /d --repair
    ./juicefs quota check sqlite3://test.db --path /d --strict
}

prepare_test()
{
    umount /jfs && sleep 3s || true
    rm -rf test.db
    rm -rf /var/jfs/myjfs
    # mc rm --force --recursive myminio/test
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

