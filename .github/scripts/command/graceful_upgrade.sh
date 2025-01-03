#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
echo meta_url is $META_URL

dpkg -s fio >/dev/null 2>&1 || .github/scripts/apt_install.sh fio
dpkg -s attr >/dev/null 2>&1 || .github/scripts/apt_install.sh attr

if [[ ! -x "./juicefs-1.1" ]]; then 
    wget -q https://github.com/juicedata/juicefs/releases/download/v1.1.0/juicefs-1.1.0-linux-amd64.tar.gz
    rm /tmp/juicefs -rf && mkdir -p /tmp/juicefs
    tar -xzvf juicefs-1.1.0-linux-amd64.tar.gz -C /tmp/juicefs
    mv /tmp/juicefs/juicefs juicefs-1.1 && chmod +x juicefs-1.1 
    rm /tmp/juicefs -rf && rm juicefs-1.1.0-linux-amd64.tar.gz
    ./juicefs-1.1 version | grep "version 1.1"
fi
[[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048


test_kill_mount_process()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /tmp/jfs -d
    wait_process_started 1
    force_kill_child_process
    sleep 3
    wait_process_started 2
    kill_parent_process
    wait_command_success "ps -ef | grep "mount" | grep "/tmp/jfs" | grep -v grep | wc -l" 0
    ./juicefs mount $META_URL /tmp/jfs -d
    kill_child_process
    wait_command_success "ps -ef | grep "mount" | grep "/tmp/jfs" | grep -v grep | wc -l" 0
    ./juicefs mount $META_URL /tmp/jfs -d
    ./juicefs umount /tmp/jfs
    wait_command_success "ps -ef | grep "mount" | grep "/tmp/jfs" | grep -v grep | wc -l" 0
}

skip_test_update_with_flock(){
    prepare_test 
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /tmp/jfs
    ps -ef | grep mount
    cat /tmp/jfs/.config | grep -i sid
    echo abc | tee /tmp/jfs/test
    sleep 1s
    flock -x /tmp/jfs/test -c cat & 
    sleep 1s
    flock -s /tmp/jfs/test -c "echo abc" > flock.log 2>&1 &
    sleep 1s
    exit 1
    ./juicefs mount -d $META_URL /tmp/jfs
    ps -ef | grep mount
    cat /tmp/jfs/.config | grep -i sid
    cat flock.log
    count=$(ps -ef | grep flock | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "flock process should be 2, count=$count" && exit 1 || true    
}

test_update_non_fuse_option(){
    prepare_test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs format $META_URL myjfs --encrypt-rsa-key my-priv-key.pem
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    echo abc | tee /tmp/jfs/test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs --read-only
    echo abc | tee /tmp/jfs/test && (echo "should not write read-only file system" && exit 1) || true
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs 
    echo abc | tee /tmp/jfs/test
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_on_failure(){
    prepare_test
    JFS_RSA_PASSPHRASE=12345678 ./juicefs format $META_URL myjfs --encrypt-rsa-key my-priv-key.pem
    JFS_RSA_PASSPHRASE=12345678 ./juicefs mount -d $META_URL /tmp/jfs
    echo abc | tee /tmp/jfs/test
    JFS_RSA_PASSPHRASE=abc123xx ./juicefs mount -d $META_URL /tmp/jfs || true
    echo abc | tee /tmp/jfs/test
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}
#TODO: fio test failed on database locked.
test_update_on_fio(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /tmp/jfs --buffer-size 300
    fio -name=fio -filename=/tmp/jfs/testfile -direct=1 -iodepth 16 -ioengine=libaio \
        -rw=randwrite -bs=4k -size=100M -numjobs=4 -runtime=30 -group_reporting >fio.log 2>&1 &
    fio_pid=$!
    trap "kill -9 $fio_pid > /dev/null || true" EXIT
    for i in {1..5}; do
        echo "update buffer-size to $((i+300))"
        ./juicefs mount -d $META_URL /tmp/jfs --buffer-size $((i+300))
        wait_command_success "ps -ef | grep juicefs | grep mount | grep \"buffer-size $((i+300))\" | wc -l" 2
        echo abc | tee /tmp/jfs/test
    done
    kill -9 $fio_pid > /dev/null 2>&1 || true
    # umount_jfs /tmp/jfs $META_URL
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
}

test_update_fuse_option(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /tmp/jfs --enable-xattr
    setfattr -n user.test -v "juicedata" /tmp/jfs
    getfattr -n user.test /tmp/jfs | grep juicedata
    ./juicefs mount -d $META_URL /tmp/jfs
    getfattr -n user.test /tmp/jfs && exit 1 || true
    ./juicefs mount -d $META_URL /tmp/jfs --enable-xattr
    getfattr -n user.test /tmp/jfs | grep juicedata
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 4 ]] && echo "mount process count should be 4, count=$count" && exit 1 || true
    umount /tmp/jfs
    getfattr -n user.test /tmp/jfs && exit 1 || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2, count=$count" && exit 1 || true
    umount /tmp/jfs
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0, count=$count" && exit 1 || true
}

test_update_from_old_version(){
    prepare_test
    ./juicefs-1.1 format $META_URL myjfs
    ./juicefs-1.1 mount  -d $META_URL /tmp/jfs
    echo hello |tee /tmp/jfs/test
    ./juicefs mount -d $META_URL /tmp/jfs
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 3 ]] && echo "mount process count should be 3" && exit 1 || true
    version=$(./juicefs version | awk '{print $3,$4,$5}')
    grep Version /tmp/jfs/.config | grep $version
    grep "hello" /tmp/jfs/test
    echo world | tee /tmp/jfs/test 
    ./juicefs umount /tmp/jfs
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 1 ]] && echo "mount process count should be 1" && exit 1 || true
    ./juicefs umount /tmp/jfs
    ps -ef | grep juicefs | grep mount | grep -v grep || true
    count=$(ps -ef | grep juicefs | grep mount | grep -v grep | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0" && exit 1 || true
}

test_update_on_fstab(){
    prepare_test
    ./juicefs format $META_URL myjfs
    umount_jfs /tmp/jfs $META_URL
    rm /sbin/mount.juicefs -rf 
    ./juicefs mount --update-fstab $META_URL /tmp/jfs -d \
        -o debug,allow_other,writeback_cache \
        --max-uploads 20  --prefetch 3 --upload-limit 3 \
        --download-limit 100 --get-timeout 60  --put-timeout 60
    grep /tmp/jfs /etc/fstab
    ls /sbin/mount.juicefs -l
    umount /tmp/jfs
    for i in {1..5}; do
        mount /tmp/jfs
        wait_command_success "ps -ef | grep juicefs | grep /tmp/jfs | grep -v grep | wc -l" 2
        # cat /tmp/jfs/.config
    done
}

prepare_test(){
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
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