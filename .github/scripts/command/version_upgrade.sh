#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
echo meta_url is $META_URL

dpkg -s fio >/dev/null 2>&1 || .github/scripts/apt_install.sh fio
dpkg -s attr >/dev/null 2>&1 || .github/scripts/apt_install.sh attr

if [[ ! -x "./juicefs.1.1" ]]; then 
    wget -q https://github.com/juicedata/juicefs/releases/download/v1.1.0/juicefs-1.1.0-linux-amd64.tar.gz
    tar -xzvf juicefs-1.1.0-linux-amd64.tar.gz --transform='s|^juicefs$|juicefs-1.1|' juicefs
    chmod +x juicefs-1.1
    ./juicefs-1.1 version
fi
[[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048
JFS_RSA_PASSPHRASE=12345678 ./juicefs format $META_URL myjfs-vc --encrypt-rsa-key my-priv-key.pem

test_kill_mount_process()
{
    prepare_test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount $META_URL /tmp/jfs -d
    wait_process_started 1
    force_kill_child_process
    sleep 3
    wait_process_started 2
    kill_parent_process
    sleep 2
    stat /tmp/jfs
    ./juicefs umount /tmp/jfs
    wait_process_killed 3
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount $META_URL /tmp/jfs -d
    kill_child_process
    wait_process_killed 4
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount $META_URL /tmp/jfs -d
    ./juicefs umount /tmp/jfs
    wait_process_killed 5
}

test_update_on_fstab(){
    ./juicefs format sqlite3://test2.db myjfs-vc2
    umount_jfs /tmp/jfs sqlite3://test2.db
    rm /sbin/mount.juicefs -rf 
    ./juicefs mount --update-fstab sqlite3://test2.db /tmp/jfs -d \
        -o debug,allow_other,writeback_cache \
        --max-uploads 20  --prefetch 3 --upload-limit 3 \
        --download-limit 100 --get-timeout 60  --put-timeout 60
    grep /tmp/jfs /etc/fstab
    ls /sbin/mount.juicefs -l
    umount /tmp/jfs
    sleep 1s
    exit 1
    mount /tmp/jfs
    ./juicefs mount sqlite3://test2.db /tmp/jfs -d
    ps -ef | grep mount

}

test_update_non_fuse_option(){
    umount_jfs /tmp/jfs $META_URL
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    echo abc | tee /tmp/jfs/test
    sleep 1s
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --read-only
    echo abc | tee /tmp/jfs/test && (echo "should not write read-only file system" && exit 1) || true
    sleep 1s
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs 
    echo abc | tee /tmp/jfs/test
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    sleep 1s
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_on_failure(){
    umount_jfs /tmp/jfs $META_URL
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    echo abc | tee /tmp/jfs/test
    sleep 1s
    JFS_RSA_PASSPHRASE=abc123xx ./juicefs mount -d $META_URL /tmp/jfs || true
    echo abc | tee /tmp/jfs/test
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    sleep 1s
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_non_fuse_option_with_fio(){
    umount_jfs /tmp/jfs $META_URL
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --buffer-size 300
    sleep 1s
    fio -name=fio -filename=/tmp/jfs/testfile -direct=1 -iodepth 64 -ioengine=libaio \
        -rw=randwrite -bs=4k -size=500M -numjobs=16 -runtime=60 -group_reporting &
    fio_pid=$!
    trap "kill -9 $fio_pid || true" EXIT
    for i in {1..10}; do 
        JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --buffer-size $((i+300))
        for t in {1..30}; do
            ps -ef | grep juicefs | grep mount | grep -v grep || true
            count=$(ps -ef | grep juicefs | grep mount | grep "buffer-size $((i+300))" | grep -v grep | wc -l)
            if [[ $count -ne 2 ]]; then
                echo "$t wait update finish, count=$count..." && sleep 1s
            else
                echo "update finish, count=$count" && break
            fi
            if [[ $t -eq 30 ]]; then
                echo "update not finish, count=$count" && exit 1
            fi
            echo abc | tee /tmp/jfs/test
        done
    done
    kill -9 $fio_pid > /dev/null 2>&1 || true
    umount_jfs /tmp/jfs $META_URL
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_fuse_option(){
    umount_jfs /tmp/jfs $META_URL
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --enable-xattr
    setfattr -n user.test -v "juicedata" /tmp/jfs
    getfattr -n user.test /tmp/jfs | grep juicedata
    sleep 1s
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    getfattr -n user.test /tmp/jfs && exit 1 || true
    sleep 1s
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --enable-xattr
    getfattr -n user.test /tmp/jfs | grep juicedata
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 4 ]] && echo "mount process count should be 4, count=$count" && exit 1 || true
    umount /tmp/jfs
    getfattr -n user.test /tmp/jfs && exit 1 || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    sleep 1s
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_from_old_version(){
    prepare_test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs-1.1 mount  -d $META_URL /tmp/jfs
    echo hello |tee /tmp/jfs/test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 3 ]] && echo "mount process count should be 3" && exit 1 || true
    version=$(./juicefs version | awk '{print $3,$4,$5}')
    grep Version /tmp/jfs/.config | grep $version
    grep "hello" /tmp/jfs/test
    echo world | tee /tmp/jfs/test 
    ./juicefs umount /tmp/jfs
    sleep 1s
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 1 ]] && echo "mount process count should be 1" && exit 1 || true
    ./juicefs umount /tmp/jfs
    sleep 1s
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0" && exit 1 || true
}

prepare_test(){
    umount_jfs /tmp/jfs $META_URL
}

kill_child_process()
{
    echo "kill_child_process"
    child_pid=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 != 1 {print $2}')
    kill $child_pid
}

force_kill_child_process()
{
    echo "force_kill_child_process"
    child_pid=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 != 1 {print $2}')
    kill -9 $child_pid
}


kill_parent_process()
{
    echo "kill_parent_process"
    parent_pid=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 == 1 {print $2}')
    kill $parent_pid
}

wait_process_killed()
{   
    echo "wait_process_killed $1"
    wait_seconds=15
    for i in $(seq 1 $wait_seconds); do
        count=$(ps -ef | grep "cmd/mount/mount mount" | grep -v grep | wc -l)
        echo i is $i, count is $count
        if [ $count -eq 0 ]; then
            echo "mount process is killed"
            break
        fi
        if [ $i -eq $wait_seconds ]; then
            ps -ef | grep "cmd/mount/mount | grep -v grep "
            echo "mount process is not killed after $wait_seconds"
            exit 1
        fi
        echo "wait process to kill" && sleep 1
    done
}

wait_process_started()
{   
    echo "wait_process_to_start $1"
    wait_seconds=15
    for i in $(seq 1 $wait_seconds); do
        if check_process_is_alive ; then
            echo "mount process is started"
            break
        fi
        if [ $i -eq $wait_seconds ]; then
            ps -ef | grep "juicefs" | grep "mount" | grep -v grep 
            echo "mount process is not started after $wait_seconds"
            exit 1
        fi
        echo "wait process to start" && sleep 1
    done
}

check_process_is_alive()
{   
    echo >&2 "check_process_is_alive $1"
    count=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | wc -l)
    if [ $count -ne 2 ]; then
        ps -ef | grep "juicefs" | grep -v "grep"
        echo >&2 "mount process is not equal 2"
        return 1
    fi
    child_count=$(ps -ef | grep "juicefs" | grep  "mount" | grep -v grep | awk '$3 != 1 {print $2}' | wc -l)
    if [[ $child_count -ne 1 ]]; then
        ps -ef | grep "juicefs" | grep -v "grep"
        echo >&2 "mount child process is not equal 1"
        return 1
    fi
    parent_count=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 == 1 {print $2}' | wc -l)
    if [ $parent_count -ne 1 ]; then
        ps -ef | grep "juicefs" | grep -v "grep"
        echo >&2 "mount parent process is not equal 1"
        return 1
    fi
    ppid1=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 == 1 {print $2}')
    ppid2=$(ps -ef | grep "juicefs" | grep "mount" | grep -v grep | awk '$3 != 1 {print $3}')
    if [ $ppid1 -ne $ppid2 ]; then
        ps -ef | grep "juicefs" | grep "mount" | grep -v "grep"
        echo >&2 "mount parent process is not equal child process's ppid"
        return 1
    fi
}


source .github/scripts/common/run_test.sh && run_test $@