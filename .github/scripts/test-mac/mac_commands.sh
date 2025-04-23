#!/bin/bash -e

source .github/scripts/common/common.sh
source .github/scripts/test-mac/start_meta_engine.sh


[[ -z "$META" ]] && META=redis
start_meta_engine $META
META_URL=$(get_meta_url $META)
user=$(whoami)
mount_point="/Users/$user/jfs"
HEARTBEAT_INTERVAL=3
HEARTBEAT_SLEEP=3
DIR_QUOTA_FLUSH_INTERVAL=4
VOLUME_QUOTA_FLUSH_INTERVAL=2

wget https://dl.min.io/client/mc/release/darwin-amd64/archive/mc.RELEASE.2021-04-22T17-40-00Z -O mc
chmod +x mc
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=admin123
export MINIO_REFRESH_IAM_INTERVAL=10s

[[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048


skip_test_modify_acl_config()
{
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount -d $META_URL $mount_point
    touch $mount_point/test
    sudo chmod +a "$user allow read,write" $mount_point/test && echo "setfacl should failed" && exit 1
    ./juicefs config $META_URL --enable-acl=true
    ./juicefs umount $mount_point
    sleep 2
    ./juicefs mount -d $META_URL $mount_point
    sudo chmod +a "$user allow read,write" $mount_point/test
    ./juicefs config $META_URL --enable-acl
    umount_jfs $mount_point $META_URL
    ./juicefs mount -d $META_URL $mount_point
    sudo chmod +a "$user allow read,write" $mount_point/test
    ./juicefs config $META_URL --enable-acl=false && echo "should not disable acl" && exit 1 || true 
    ./juicefs config $META_URL | grep EnableACL | grep "true" || (echo "EnableACL should be true" && exit 1) 
}

test_clone_with_jfs_source()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point
    [[ ! -d $mount_point/juicefs ]] && git clone https://github.com/juicedata/juicefs.git $mount_point/juicefs --depth 1
    do_clone true
    do_clone false
}

do_clone()
{
    is_preserve=$1
    rm -rf $mount_point/juicefs1
    rm -rf $mount_point/juicefs2
    [[ "$is_preserve" == "true" ]] && preserve="-p" || preserve=""
    cp -r $preserve $mount_point/juicefs $mount_point/juicefs1
    ./juicefs clone $mount_point/juicefs $mount_point/juicefs2 --preserve
    diff -r $mount_point/juicefs1 $mount_point/juicefs2
    cd $mount_point/juicefs1/ && find . -exec stat -f "%p %u %g %N" {} \; | sort >/tmp/log1 && cd -
    cd $mount_point/juicefs2/ && find . -exec stat -f "%p %u %g %N" {} \; | sort >/tmp/log2 && cd -
    diff /tmp/log1 /tmp/log2
}

check_debug_file(){
   files=("system-info.log" "juicefs.log" "config.txt" "stats.txt" "stats.5s.txt" "pprof")
   debug_dir="debug"
   if [ ! -d "$debug_dir" ]; then
    echo "error:no debug dir"
    exit 1
   fi
   all_files_exist=true
   for file in "${files[@]}"; do
     exist=`find "$debug_dir" -name $file | wc -l`
     if [ "$exist" == 0 ]; then
        echo "no $file"
        all_files_exist=false
     fi
   done
   if [ "$all_files_exist" = true ]; then
    echo "pass"
   else
    exit 1
   fi
}

test_debug_juicefs(){
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL $mount_point
    dd if=/dev/urandom of=$mount_point/bigfile bs=1M count=128
    ./juicefs debug $mount_point/
    check_debug_file
    ./juicefs rmr $mount_point/bigfile
}

test_sync_dir_stat()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point
    ./juicefs mdtest $META_URL /d --depth 15 --dirs 2 --files 100 --threads 10 & 
    pid=$!
    sleep 10
    kill -9 $pid
    pkill -P "$pid" 2>/dev/null || true
    ./juicefs info -r $mount_point/d
    ./juicefs info -r $mount_point/d --strict 
    ./juicefs fsck $META_URL --path /d --sync-dir-stat --repair -r
    ./juicefs info -r $mount_point/d | tee info1.log
    ./juicefs info -r $mount_point/d --strict | tee info2.log
    diff info1.log info2.log
    rm info*.log
    ./juicefs fsck $META_URL --path / --sync-dir-stat --repair -r
    ./juicefs info -r $mount_point | tee info1.log
    ./juicefs info -r $mount_point --strict | tee info2.log
    diff info1.log info2.log
}

test_gc_trash_slices(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point
    PATH1=/tmp/test PATH2=$mount_point/test python3 .github/scripts/random_read_write.py 
    ./juicefs status --more $META_URL
    ./juicefs config $META_URL --trash-days 0 --yes
    ./juicefs gc $META_URL 
    ./juicefs gc $META_URL --delete
    ./juicefs status --more $META_URL
}

test_update_non_fuse_option(){
    prepare_test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs format $META_URL myjfs --encrypt-rsa-key my-priv-key.pem
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL $mount_point
    echo abc | tee $mount_point/test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL $mount_point --read-only
    echo abc | tee $mount_point/test && (echo "should not write read-only file system" && exit 1) || true
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL $mount_point 
    echo abc | tee $mount_point/test
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l | tr -d ' ')
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount $mount_point
    sleep 2
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l | tr -d ' ')
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_info_big_file(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point
    dd if=/dev/zero of=$mount_point/bigfile bs=1M count=4096
    ./juicefs info $mount_point/bigfile
    ./juicefs rmr $mount_point/bigfile
    df -h $mount_point
}

test_list_large_dir()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point
    local files_count=100000
    if [[ "$META_URL" == redis://* ]]; then
        files_count=130000
    fi
    ./juicefs mdtest $META_URL /test --depth 0 --dirs 1 --files $files_count --threads 1
    du $mount_point/test & du_pid=$!
    sleep 2
    kill -INT $du_pid || true
    wait $du_pid || true
    if ! [ -d "$mount_point/test" ]; then
        echo >&2 "<FATAL>: directory $mount_point/test is not accessible after ls interruption"
        exit 1
    fi
}

test_total_inodes(){
    prepare_test
    ./juicefs format $META_URL myjfs --inodes 1000
    ./juicefs mount -d $META_URL $mount_point --heartbeat $HEARTBEAT_INTERVAL
    set +x
    for i in {1..1000}; do
        echo $i | tee $mount_point/test$i > /dev/null
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee $mount_point/test1001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
    grep "No space left on device" error.log
    ./juicefs config $META_URL --inodes 2000
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    set +x
    for i in {1001..2000}; do
        echo $i | tee $mount_point/test$i > /dev/null || (df -i $mount_point && ls $mount_point/ -l | wc -l  && exit 1)
    done
    set -x
    sleep $VOLUME_QUOTA_FLUSH_INTERVAL
    echo a | tee $mount_point/test2001 2>error.log && echo "write should fail on out of inodes" && exit 1 || true
}

test_remove_and_restore(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p $mount_point/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=$mount_point/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a $mount_point/d/test2 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disc quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm -rf $mount_point/d/test1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "0%" ]] && echo "used should be 0%" && exit 1 || true

    trash_dir=$(ls $mount_point/.trash)
    sudo ./juicefs restore $META_URL $trash_dir --put-back
    ./juicefs quota get $META_URL --path /d 2>&1 | tee quota.log
    used=$(cat quota.log | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    echo a | tee -a $mount_point/d/test2 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep -i "Disc quota exceeded" error.log || (echo "grep failed" && exit 1)

    echo "remove test1" && rm -rf $mount_point/d/test1
    dd if=/dev/zero of=$mount_point/d/test2 bs=1M count=1
    trash_dir=$(ls $mount_point/.trash)
    sudo ./juicefs restore $META_URL $trash_dir --put-back 2>&1 | tee restore.log
    grep "disc quota exceeded" restore.log || (echo "check restore log failed" && exit 1)
}

test_dir_capacity(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL $mount_point --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p $mount_point/d
    ./juicefs quota set $META_URL --path /d --capacity 1
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=$mount_point/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota get $META_URL --path /d
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "100%" ]] && echo "used should be 100%" && exit 1 || true
    echo a | tee -a $mount_point/d/test2 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disc quota exceeded" error.log || (echo "grep failed" && exit 1)

    ./juicefs quota set $META_URL --path /d --capacity 2
    sleep $((HEARTBEAT_INTERVAL+HEARTBEAT_SLEEP))
    dd if=/dev/zero of=$mount_point/d/test2 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a $mount_point/d/test3 2>error.log && echo "echo should fail on out of space" && exit 1 || true
    grep -i "Disc quota exceeded" error.log || (echo "grep failed" && exit 1)
    rm -rf $mount_point/d/test1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    used=$(./juicefs quota get $META_URL --path /d 2>&1 | grep "/d" | awk -F'|' '{print $5}'  | tr -d '[:space:]')
    [[ $used != "50%" ]] && echo "used should be 50%" && exit 1 || true
    dd if=/dev/zero of=$mount_point/d/test3 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    ./juicefs quota check $META_URL --path /d --strict
}

kill_gateway() {
    port=$1
    lsof -i:$port || true
    lsof -t -i :$port | xargs -r kill -9 || true
}

trap 'kill_gateway 9001; kill_gateway 9002' EXIT

start_two_gateway()
{  
    kill_gateway 9001
    kill_gateway 9002
    prepare_test
    ./juicefs format $META_URL myjfs  --trash-days 0
    ./juicefs mount -d $META_URL $mount_point
    export MINIO_ROOT_USER=admin
    export MINIO_ROOT_PASSWORD=admin123
    ./juicefs gateway $META_URL 127.0.0.1:9001 --multi-buckets --keep-etag --object-tag -background
    sleep 1
    ./juicefs gateway $META_URL 127.0.0.1:9002 --multi-buckets --keep-etag --object-tag -background
    sleep 2
    ./mc alias set gateway1 http://127.0.0.1:9001 admin admin123
    ./mc alias set gateway2 http://127.0.0.1:9002 admin admin123
}

test_user_management()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    sleep 12
    user=$(./mc admin user list gateway2 | grep user1) || true
    if [ -z "$user" ]
    then
      echo "user synchronization error"
      exit 1
    fi
    ./mc mb gateway1/test1
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "By default, the user has no read and write permission"
      exit 1
    fi
    ./mc admin policy set gateway1 readwrite user=user1
    if ./mc cp mc gateway1_user1/test1/file1
    then 
      echo "readwrite policy can read and write objects" 
    else
      echo "set readwrite policy fail"
      exit 1
    fi
    ./mc cp gateway2/test1/file1 .
    compare_md5sum file1 mc  
    ./mc admin user disable gateway1 user1
    ./mc admin user remove gateway2 user1
    sleep 12
    user=$(./mc admin user list gateway1 | grep user1) || true
    if [ ! -z "$user" ]
    then
      echo "remove user user1 fail"
      echo $user
      exit 1
    fi
}

test_group_management()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin user add gateway1 user2 admin123
    ./mc admin user add gateway1 user3 admin123
    ./mc admin group add gateway1 testcents user1 user2 user3
    result=$(./mc admin group info gateway1 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
    ./mc admin policy set gateway1 readwrite group=testcents
    sleep 5
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    ./mc mb gateway1/test1
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "readwrite policy can read write"
    else
      echo "the readwrite group has no read and write permission"
      exit 1
    fi
    ./mc admin policy set gateway1 readonly group=testcents
    sleep 5
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "readonly group policy can not write"
      exit 1
    else
      echo "the readonly group has no write permission"
    fi

    ./mc admin group remove gateway1 testcents user1 user2 user3 
    ./mc admin group remove gateway1 testcents
}

source .github/scripts/common/run_test.sh && run_test $@
