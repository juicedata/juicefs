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
    run_remove_and_restore_uid_gid_case
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
    run_dir_capacity_uid_gid_case
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
    run_dir_inodes_uid_gid_case
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
   # run_sub_dir_uid_gid_case
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
    run_dump_load_uid_gid_case
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
    run_hard_link_uid_gid_case
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
    run_check_and_repair_uid_gid_quota_case
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

prepare_ug_quota_test()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs config $META_URL --user-group-quota
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
}

resolve_test_users()
{
    if [[ -n "$TEST_USER_1" ]] && [[ -n "$TEST_USER_2" ]]; then
        return 0
    fi

    TEST_USER_1=""
    TEST_USER_2=""

    for candidate in nobody daemon bin; do
        if id "$candidate" >/dev/null 2>&1; then
            candidate_uid=$(id -u "$candidate")
            candidate_gid=$(id -g "$candidate")
            if [[ "$candidate_uid" == "0" ]] || [[ "$candidate_gid" == "0" ]]; then
                continue
            fi
            if [[ -z "$TEST_USER_1" ]]; then
                TEST_USER_1="$candidate"
                TEST_UID_1=$candidate_uid
                TEST_GID_1=$candidate_gid
            elif [[ "$candidate_uid" != "$TEST_UID_1" ]]; then
                TEST_USER_2="$candidate"
                TEST_UID_2=$candidate_uid
                TEST_GID_2=$candidate_gid
                break
            fi
        fi
    done
    create_temp_user()
    {
        idx=$1
        if ! command -v useradd >/dev/null 2>&1; then
            return 1
        fi
        name="jfs-quota-test-${idx}-${RANDOM}"
        if ! useradd -M -s /usr/sbin/nologin "$name" >/dev/null 2>&1; then
            return 1
        fi
        uid=$(id -u "$name" 2>/dev/null || echo 0)
        gid=$(id -g "$name" 2>/dev/null || echo 0)
        if [[ "$uid" == "0" ]] || [[ "$gid" == "0" ]]; then
            userdel -f "$name" >/dev/null 2>&1 || true
            return 1
        fi
        echo "$name:$uid:$gid"
        return 0
    }

    if [[ -z "$TEST_USER_1" ]] || [[ -z "$TEST_USER_2" ]]; then
        if [[ "$(id -u)" != "0" ]]; then
            echo "cannot find two non-root users for user/group quota tests"
            return 1
        fi
        for i in 1 2 3 4; do
            info=$(create_temp_user "$i") || continue
            name=$(echo "$info" | cut -d: -f1)
            uid=$(echo "$info" | cut -d: -f2)
            gid=$(echo "$info" | cut -d: -f3)
            if [[ -z "$TEST_USER_1" ]]; then
                TEST_USER_1="$name"
                TEST_UID_1=$uid
                TEST_GID_1=$gid
            elif [[ -z "$TEST_USER_2" ]] && [[ "$uid" != "$TEST_UID_1" ]]; then
                TEST_USER_2="$name"
                TEST_UID_2=$uid
                TEST_GID_2=$gid
                break
            fi
        done
    fi

    if [[ -z "$TEST_USER_1" ]] || [[ -z "$TEST_USER_2" ]]; then
        echo "cannot find two non-root users for user/group quota tests"
        return 1
    fi

    echo "test users: $TEST_USER_1($TEST_UID_1:$TEST_GID_1), $TEST_USER_2($TEST_UID_2:$TEST_GID_2)"
}

run_as_user_cmd()
{
    user=$1
    shift
    cmd="$*"

    if [[ "$(id -un)" == "$user" ]]; then
        bash -c "$cmd"
        return $?
    fi

    if command -v sudo >/dev/null 2>&1; then
        sudo -n -u "$user" bash -c "$cmd" && return 0 || true
    fi

    if command -v runuser >/dev/null 2>&1; then
        runuser -u "$user" -- bash -c "$cmd" && return 0 || true
    fi

    if command -v su >/dev/null 2>&1; then
        su -s /bin/bash "$user" -c "$cmd" && return 0 || true
    fi

    echo "cannot run command as user $user"
    return 1
}

set_quota_by_username()
{
    username=$1
    capacity=$2
    inodes=$3
    uid=$(id -u "$username")
    ./juicefs quota set $META_URL --uid "$uid" --capacity "$capacity" --inodes "$inodes"
}

test_user_group_quota_set_get_list_delete(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 20
    ./juicefs quota set $META_URL --gid "$TEST_GID_1" --capacity 1 --inodes 20
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    ./juicefs quota get $META_URL --uid "$TEST_UID_1" 2>&1 | tee uid_quota.log
    grep "UID:$TEST_UID_1" uid_quota.log || (echo "uid quota should exist" && exit 1)

    ./juicefs quota get $META_URL --gid "$TEST_GID_1" 2>&1 | tee gid_quota.log
    grep "GID:$TEST_GID_1" gid_quota.log || (echo "gid quota should exist" && exit 1)

    ./juicefs quota list $META_URL 2>&1 | tee quota_list.log
    grep "UID:$TEST_UID_1" quota_list.log || (echo "uid quota should be listed" && exit 1)
    grep "GID:$TEST_GID_1" quota_list.log || (echo "gid quota should be listed" && exit 1)

    ./juicefs quota delete $META_URL --uid "$TEST_UID_1"
    ./juicefs quota delete $META_URL --gid "$TEST_GID_1"
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    ./juicefs quota list $META_URL 2>&1 | tee quota_list_after_delete.log
    grep "UID:$TEST_UID_1" quota_list_after_delete.log && echo "uid quota should be deleted" && exit 1 || true
    grep "GID:$TEST_GID_1" quota_list_after_delete.log && echo "gid quota should be deleted" && exit 1 || true
}

test_uid_quota_check_on_write_and_truncate(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/uidq
    chmod 777 /jfs/uidq

    ./juicefs quota set $META_URL --uid "$TEST_UID_2" --capacity 1 --inodes 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_2" "touch /jfs/uidq/inode1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "touch /jfs/uidq/inode2" 2>error.log && echo "second inode should fail for uid quota" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid inode quota check failed" && exit 1)

    ./juicefs quota set $META_URL --uid "$TEST_UID_2" --capacity 1 --inodes 10
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    rm -f /jfs/uidq/inode1
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    run_as_user_cmd "$TEST_USER_2" "truncate -s 900M /jfs/uidq/space1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "truncate -s 1100M /jfs/uidq/space1" 2>error.log && echo "truncate should fail for uid capacity quota" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid capacity quota check failed" && exit 1)
}

test_gid_quota_check_on_write(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/gidq
    chmod 777 /jfs/gidq

    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --inodes 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_2" "touch /jfs/gidq/file1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "touch /jfs/gidq/file2" 2>error.log && echo "second inode should fail for gid quota" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid inode quota check failed" && exit 1)
}

test_chown_transfer_user_group_quota(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/chownq
    chmod 777 /jfs/chownq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --inodes 1
    ./juicefs quota set $META_URL --uid "$TEST_UID_2" --inodes 1
    ./juicefs quota set $META_URL --gid "$TEST_GID_1" --inodes 1
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --inodes 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "touch /jfs/chownq/src_file"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "touch /jfs/chownq/src_file2" 2>error.log && echo "user1 should exceed inode quota before chown" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "user1 pre-chown quota check failed" && exit 1)

    chown "$TEST_UID_2:$TEST_GID_2" /jfs/chownq/src_file
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "touch /jfs/chownq/src_file2"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "touch /jfs/chownq/dst_file" 2>error.log && echo "user2 should exceed inode quota after chown transfer" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "user2 post-chown quota check failed" && exit 1)
}

test_set_quota_by_username(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    set_quota_by_username "$TEST_USER_2" 1 10
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    uid=$(id -u "$TEST_USER_2")
    ./juicefs quota get $META_URL --uid "$uid" 2>&1 | tee username_quota.log
    grep "UID:$uid" username_quota.log || (echo "quota set by username should be visible in uid quota" && exit 1)

    ./juicefs quota list $META_URL 2>&1 | tee username_quota_list.log
    grep "UID:$uid" username_quota_list.log || (echo "quota set by username should be listed" && exit 1)
}

test_quota_list_uid_filter_regression(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 3
    ./juicefs quota set $META_URL --uid "$TEST_UID_2" --capacity 1 --inodes 7
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    ./juicefs quota list $META_URL --uid "$TEST_UID_1" 2>&1 | tee uid_filter_1.log
    grep "UID:$TEST_UID_1" uid_filter_1.log || (echo "uid filter should show requested uid quota" && exit 1)
    grep "UID:$TEST_UID_2" uid_filter_1.log && echo "uid filter should not include other uid quota" && exit 1 || true
    uid_rows=$(grep -c "UID:" uid_filter_1.log || true)
    [[ "$uid_rows" -ne 1 ]] && echo "uid filter should only return one UID row" && exit 1 || true
    inodes_value=$(grep "UID:$TEST_UID_1" uid_filter_1.log | head -n1 | awk -F'|' '{gsub(/[[:space:]]/,"",$6); print $6}')
    [[ "$inodes_value" != "3" ]] && echo "uid filter should return uid1 inodes=3" && exit 1 || true

    ./juicefs quota list $META_URL --uid "$TEST_UID_2" 2>&1 | tee uid_filter_2.log
    grep "UID:$TEST_UID_2" uid_filter_2.log || (echo "uid filter should show requested uid quota" && exit 1)
    grep "UID:$TEST_UID_1" uid_filter_2.log && echo "uid filter should not include other uid quota" && exit 1 || true
    uid_rows=$(grep -c "UID:" uid_filter_2.log || true)
    [[ "$uid_rows" -ne 1 ]] && echo "uid filter should only return one UID row" && exit 1 || true
    inodes_value=$(grep "UID:$TEST_UID_2" uid_filter_2.log | head -n1 | awk -F'|' '{gsub(/[[:space:]]/,"",$6); print $6}')
    [[ "$inodes_value" != "7" ]] && echo "uid filter should return uid2 inodes=7" && exit 1 || true
}

run_sub_dir_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/d
    chmod 777 /jfs/d

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 100
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1 --inodes 100
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    umount_jfs /jfs $META_URL
    ./juicefs mount -d $META_URL --subdir /d /jfs --heartbeat $HEARTBEAT_INTERVAL
    chmod 777 /jfs

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/uid_cap bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "echo a >> /jfs/uid_cap" 2>error.log \
        && echo "uid capacity quota should block write via subdir mount" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid subdir capacity quota not enforced" && exit 1)

    rm -f /jfs/uid_cap
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    run_as_user_cmd "$TEST_USER_1" "for i in \$(seq 1 100); do touch /jfs/uid_inode_\$i; done"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "touch /jfs/uid_inode_overflow" 2>error.log \
        && echo "uid inode quota should block create via subdir mount" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid subdir inode quota not enforced" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/gid_cap bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "echo a >> /jfs/gid_cap" 2>error.log \
        && echo "gid capacity quota should block write via subdir mount" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid subdir capacity quota not enforced" && exit 1)

    rm -f /jfs/gid_cap
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    run_as_user_cmd "$TEST_USER_2" "for i in \$(seq 1 100); do touch /jfs/gid_inode_\$i; done"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "touch /jfs/gid_inode_overflow" 2>error.log \
        && echo "gid inode quota should block create via subdir mount" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid subdir inode quota not enforced" && exit 1)
}

run_hard_link_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/ughl
    chmod 777 /jfs/ughl

    dd if=/dev/zero of=/jfs/root_file bs=1G count=1

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 2
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/ughl/uid_test1 bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    ln /jfs/root_file /jfs/ughl/root_link
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/ughl/uid_test2 bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    run_as_user_cmd "$TEST_USER_1" "echo a >> /jfs/ughl/uid_test2" 2>error.log \
        && echo "uid capacity quota should block write after 2G used" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid hardlink capacity quota not enforced" && exit 1)

    ln /jfs/ughl/uid_test1 /jfs/ughl/uid_test1_link
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/ughl/gid_test1 bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    ln /jfs/root_file /jfs/ughl/gid_root_link 2>error.log \
        && echo "hard link into gid-quota-full area should fail" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid hardlink capacity quota not enforced" && exit 1)

    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict
}

run_dump_load_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/dumpq
    chmod 777 /jfs/dumpq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 100
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1 --inodes 100
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/dumpq/uid_pre bs=512M count=1"
    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/dumpq/gid_pre bs=512M count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    ./juicefs dump --log-level error $META_URL --fast > dump.json
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount $META_URL /jfs -d --heartbeat $HEARTBEAT_INTERVAL
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    chmod 777 /jfs/dumpq

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/dumpq/uid_post bs=1G count=1" 2>error.log \
        && echo "uid capacity quota should be enforced after dump/load" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid quota not preserved by dump/load" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/dumpq/gid_post bs=1G count=1" 2>error.log \
        && echo "gid capacity quota should be enforced after dump/load" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid quota not preserved by dump/load" && exit 1)

    run_as_user_cmd "$TEST_USER_1" "for i in \$(seq 1 100); do touch /jfs/dumpq/uid_inode_post_\$i; done"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "touch /jfs/dumpq/uid_inode_overflow" 2>error.log \
        && echo "uid inode quota should be enforced after dump/load" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid inode quota not preserved by dump/load" && exit 1)

    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict
    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict
}

run_check_and_repair_uid_gid_quota_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/checkq
    chmod 777 /jfs/checkq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 100
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1 --inodes 100
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/checkq/uid_file bs=512M count=1"
    run_as_user_cmd "$TEST_USER_1" "for i in \$(seq 1 50); do touch /jfs/checkq/uid_inode_\$i; done"
    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/checkq/gid_file bs=512M count=1"
    run_as_user_cmd "$TEST_USER_2" "for i in \$(seq 1 50); do touch /jfs/checkq/gid_inode_\$i; done"

    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | awk '{print $2}')
    kill -9 $pid
    sleep $DIR_QUOTA_FLUSH_INTERVAL

    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict --repair
    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict

    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict --repair
    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict
}

run_remove_and_restore_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/rrq
    chmod 777 /jfs/rrq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/rrq/uid_file bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "echo a >> /jfs/rrq/uid_file" 2>error.log && echo "uid remove/restore should hit capacity quota" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid remove/restore pre-delete quota check failed" && exit 1)

    rm -f /jfs/rrq/uid_file
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    trash_dir=$(ls /jfs/.trash | tail -n1)
    ./juicefs restore $META_URL $trash_dir --put-back
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    run_as_user_cmd "$TEST_USER_1" "echo a >> /jfs/rrq/uid_file" 2>error.log && echo "uid restore should recover used space and keep quota enforcement" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid remove/restore post-restore quota check failed" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/rrq/gid_file bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "echo a >> /jfs/rrq/gid_file" 2>error.log && echo "gid remove/restore should hit capacity quota" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid remove/restore pre-delete quota check failed" && exit 1)

    rm -f /jfs/rrq/gid_file
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    trash_dir=$(ls /jfs/.trash | tail -n1)
    ./juicefs restore $META_URL $trash_dir --put-back
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    run_as_user_cmd "$TEST_USER_2" "echo a >> /jfs/rrq/gid_file" 2>error.log && echo "gid restore should recover used space and keep quota enforcement" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid remove/restore post-restore quota check failed" && exit 1)
}

run_dir_capacity_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/capq
    chmod 777 /jfs/capq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/capq/uid_cap_file bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "echo a >> /jfs/capq/uid_cap_file" 2>error.log && echo "uid capacity quota should block append" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid capacity quota check failed" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/capq/gid_cap_file bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "echo a >> /jfs/capq/gid_cap_file" 2>error.log && echo "gid capacity quota should block append" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid capacity quota check failed" && exit 1)

    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict
    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict
}

run_dir_inodes_uid_gid_case(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    mkdir -p /jfs/inodeq
    chmod 777 /jfs/inodeq

    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --inodes 100
    ./juicefs quota set $META_URL --gid "$TEST_GID_2" --inodes 100
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "for i in \$(seq 1 100); do touch /jfs/inodeq/uid_file_\$i; done"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "touch /jfs/inodeq/uid_file_overflow" 2>error.log && echo "uid inode quota should block file create" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid inode quota check failed" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "for i in \$(seq 1 100); do touch /jfs/inodeq/gid_file_\$i; done"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "touch /jfs/inodeq/gid_file_overflow" 2>error.log && echo "gid inode quota should block file create" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "gid inode quota check failed" && exit 1)

    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict
    ./juicefs quota check $META_URL --gid "$TEST_GID_2" --strict
}

test_quota_uid_path_global_combo(){
    prepare_ug_quota_test
    resolve_test_users || return 0

    ./juicefs config $META_URL --capacity 3 --inodes 300
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    mkdir -p /jfs/combo
    chmod 777 /jfs/combo

    ./juicefs quota set $META_URL --path /combo --capacity 2 --inodes 200
    ./juicefs quota set $META_URL --uid "$TEST_UID_1" --capacity 1 --inodes 120
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))

    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/combo/u1_file1 bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_1" "dd if=/dev/zero of=/jfs/combo/u1_file2 bs=1G count=1" 2>error.log && echo "uid quota should take effect in uid+path+global combo" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "uid quota not enforced in uid+path+global combo" && exit 1)

    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/combo/u2_file1 bs=1G count=1"
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    run_as_user_cmd "$TEST_USER_2" "dd if=/dev/zero of=/jfs/combo/u2_file2 bs=1G count=1" 2>error.log && echo "path quota should cap total usage in uid+path+global combo" && exit 1 || true
    grep -i "Disk quota exceeded" error.log || (echo "path quota not enforced in uid+path+global combo" && exit 1)

    ./juicefs quota check $META_URL --uid "$TEST_UID_1" --strict
    ./juicefs quota check $META_URL --path /combo --strict
}

source .github/scripts/common/run_test.sh && run_test $@
