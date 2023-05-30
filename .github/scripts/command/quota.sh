#!/bin/bash
set -ex
[[ -z "$META" ]] && META=tikv

python3 -c "import minio" || sudo pip install minio 
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)


source .github/scripts/common/common.sh

test_total_capacity()
{
    prepare_test
    ./juicefs format $META_URL myjfs --capacity 1
    ./juicefs mount -d $META_URL /jfs --heartbeat 5 --debug
    dd if=/dev/zero of=/jfs/test1 bs=1G count=1
    sleep 10s
    echo a | tee -a /jfs/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config $META_URL --capacity 2
    sleep 10s
    dd if=/dev/zero of=/jfs/test2 bs=1G count=1
    sleep 10s
    echo a | tee -a /jfs/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep "No space left on device" error.log

    rm /jfs/test1 -rf
    sleep 10s
    echo a | tee -a /jfs/test3 2>error.log && echo "echo should fail on out of space" && exit 1 || true

    ./juicefs rmr /jfs/.trash
    sleep 10s
    echo a | tee -a /jfs/test3 

    sleep 10s
    ln /jfs/test2 /jfs/test4
    ln /jfs/test2 /jfs/test5
}

test_total_inodes(){
    prepare_test
    ./juicefs format $META_URL myjfs --inodes 1000
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    for i in {1..1000}; do
        echo $i | tee /jfs/test$i
    done
    sleep 12s
    echo a | tee /jfs/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config $META_URL --inodes 2000
    sleep 6s
    for i in {1001..2000}; do
        echo $i | tee /jfs/test$i
    done
    sleep 6s
    echo a | tee /jfs/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
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
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep 6s
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep 3s
    ./juicefs quota get $META_URL --path /d
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a /jfs/d/test1 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs quota set $META_URL --path /d --capacity 2
    sleep 6s
    dd if=/dev/zero of=/jfs/d/test2 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs/d/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf /jfs/d/test1
    sleep 3s
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "50%" ]] && echo "used should be 50%" && exit 1 || true
    dd if=/dev/zero of=/jfs/d/test3 bs=1G count=1
    sleep 3s
    ./juicefs quota check $META_URL --path /d --strict
}

test_dir_inodes(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000
    sleep 6s
    for i in {1..1000}; do
        echo $i > /jfs/d/test$i
    done
    sleep 3s
    echo a | tee /jfs/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf error.log
    ./juicefs quota set $META_URL --path /d --inodes 2000
    sleep 6s
    for i in {1001..2000}; do
        echo $i | tee  /jfs/d/test$i
    done
    sleep 3s
    echo a | tee  /jfs/d/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/d/test1 -rf
    sleep 3s
    echo a | tee  /jfs/d/test2001
    sleep 3s
    ./juicefs quota check $META_URL --path /d --strict
}

test_sub_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000 --capacity 1
    sleep 6s
    umount_jfs /jfs $META_URL
    ./juicefs mount -d $META_URL --subdir /d /jfs --heartbeat 2
    size=$(df -h /jfs | grep "JuiceFS" | awk '{print $2}')
    [[ $size != "1.0G" ]] && echo "size should be 1.0G" && exit 1 || true
    inodes=$(df -ih /jfs | grep "JuiceFS" | awk '{print $2}')
    [[ $inodes != "1000" ]] && echo "inodes should be 1000" && exit 1 || true
    dd if=/dev/zero of=/jfs/test1 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/test1 -rf
    sleep 3s
    for i in {1..1000}; do
        echo $i | tee /jfs/test$i
    done
    sleep 3s
    echo $i | tee /jfs/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    ./juicefs quota check $META_URL --path /d --strict
}

test_dump_load(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000 --capacity 1
    sleep 6s
    ./juicefs dump $META_URL > dump.json
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount $META_URL /jfs -d --heartbeat 5
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep 3s
    echo a | tee -a /jfs/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm /jfs/d/test1 -rf
    sleep 3s
    for i in {1..1000}; do
        echo $i | tee /jfs/d/test$i
    done
    sleep 3s
    echo a | tee /jfs/d/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    ./juicefs quota check $META_URL --path /d --strict
}

test_hard_link(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    dd if=/dev/zero of=/jfs/file bs=1G count=1
    ./juicefs quota set $META_URL --path /d --capacity 2
    sleep 6s
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep 3s
    ln /jfs/file /jfs/d/test2
    sleep 3s
    ln /jfs/file /jfs/d/test3 2>error.log && echo "hard link should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
    sleep 3s
    ./juicefs quota check $META_URL --path /d --strict
}

test_check_and_repair_quota(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat 5
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep 6s
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | awk '{print $2}')
    kill -9 $pid
    sleep 3s
    ./juicefs quota check $META_URL --path /d --strict && echo "quota check should fail" && exit 1 || true
    ./juicefs quota check $META_URL --path /d --repair
    ./juicefs quota check $META_URL --path /d --strict
}

prepare_test()
{
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
    # mc rm --force --recursive myminio/test
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

