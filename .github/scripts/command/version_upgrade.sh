#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
echo meta_url is $META_URL

dpkg -s fio >/dev/null 2>&1 || .github/scripts/apt_install.sh fio
lsof -t -i:8081 | xargs -r sudo kill -9
python3 -m http.server 8081 &
server_pid=$!
trap "kill -9 $server_pid" EXIT
.github/scripts/apt_install.sh attr
if [[ ! -x "./juicefs.1.1" ]]; then 
    wget -q https://github.com/juicedata/juicefs/releases/download/v1.1.0/juicefs-1.1.0-linux-amd64.tar.gz
    tar -xzvf juicefs-1.1.0-linux-amd64.tar.gz --transform='s|^juicefs$|juicefs-1.1|' juicefs
    chmod +x juicefs-1.1
    ./juicefs-1.1 version
fi
[[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048

test_kill_mount_process()
{
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    wait_process_started 1
    force_kill_child_process
    sleep 3
    wait_process_started 2
    kill_parent_process
    sleep 2
    stat /tmp/jfs
    ./juicefs umount /tmp/jfs
    wait_process_killed 3
    ./juicefs mount $META_URL /tmp/jfs -d
    kill_child_process
    wait_process_killed 4
    ./juicefs mount $META_URL /tmp/jfs -d
    ./juicefs umount /tmp/jfs
    wait_process_killed 5
}

test_update_non_fuse_option(){
    prepare_test
    ./juicefs mount -d $META_URL /tmp/jfs --cache-dir=/tmp/cache1 --cache-size=800
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=2000
    cat /tmp/jfs/test > /dev/null
    check_cache_size  /tmp/cache1 800
    ./juicefs mount -d $META_URL /tmp/jfs --cache-dir=/tmp/cache1 --cache-size=400
    sleep 2
    check_cache_size  /tmp/cache1 400
    ./juicefs umount /tmp/jfs
}


test_update_fuse_option(){
    umount_jfs /tmp/jfs_xattr $META_URL
    mkdir -p /tmp/jfs_xattr && chmod 777 /tmp/jfs_xattr
    ./juicefs mount -d $META_URL /tmp/jfs_xattr --enable-xattr
    setfattr -n user.test -v "juicedata" /tmp/jfs_xattr
    getfattr -n user.test /tmp/jfs_xattr | grep juicedata
    sleep 1s
    ./juicefs mount -d $META_URL /tmp/jfs_xattr
    getfattr -n user.test /tmp/jfs_xattr && exit 1 || true
    sleep 1s
    ./juicefs mount -d $META_URL /tmp/jfs_xattr --enable-xattr
    setfattr -n user.test -v "juicedata" /tmp/jfs_xattr
    getfattr -n user.test /tmp/jfs_xattr | grep juicedata
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 4 ]] && echo "mount process count should be 4" && exit 1 || true
    umount /tmp/jfs_xattr
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 2 ]] && echo "mount process count should be 2" && exit 1 || true
    umount /tmp/jfs_xattr
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0" && exit 1 || true
}

test_restart_from_1_dot_1(){
    prepare_test
    ./juicefs-1.1 mount  -d $META_URL /tmp/jfs 
    echo hello |tee /tmp/jfs/test
    ./juicefs mount -d $META_URL /tmp/jfs
    wait_process_started
    version=$(./juicefs version | awk '{print $3,$4,$5}')
    grep Version /tmp/jfs/.jfsconfig | grep "$version"
    grep "hello" /tmp/jfs/test 
    ./juicefs umount /tmp/jfs
    wait_process_killed
}

test_restart_from_1_dot_1(){
    prepare_test
    ./juicefs-1.1 mount  -d $META_URL /tmp/jfs 
    echo hello |tee /tmp/jfs/test
    ./juicefs mount -d $META_URL /tmp/jfs
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 3 ]] && echo "mount process count should be 3" && exit 1 || true
    version=$(./juicefs version | awk '{print $3,$4,$5}')
    grep Version /tmp/jfs/.config | grep $version
    echo world | tee /tmp/jfs/test 
    ./juicefs umount /tmp/jfs
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 1 ]] && echo "mount process count should be 1" && exit 1 || true
    ./juicefs umount /tmp/jfs
    count=$(ps -ef | grep juicefs | grep mount | wc -l)
    [[ $count -ne 0 ]] && echo "mount process count should be 0" && exit 1 || true
}


do_upgrade_restart_from_python(){
    prepare_test
    old_version=$1
    echo old_version is $old_version
    wget -q https://s.juicefs.com/static/juicefs-$old_version.py -O juicefs-$old_version.py && chmod +x juicefs-$old_version.py
    mkdir -p /root/.juicefs
    cp -f cmd/mount/mount.$old_version /root/.juicefs/jfsmount
    
    if [[ $old_version == "4.8" || $old_version == "4.9" ]]; then
        ./juicefs-$old_version.py mount test-volume /tmp/jfs \
            --no-update --conf-dir=conf -o debug
    else
        JFS_RSA_PASSPHRASE=12345678 ./juicefs-$old_version.py mount test-volume /tmp/jfs \
            --no-update --conf-dir=conf  --rsa-key my-priv-key.pem -o debug
    fi

    fio -name=fio -filename=/tmp/jfs/testfile -direct=1 -iodepth 64 -ioengine=libaio -rw=randwrite -bs=4k -size=500M -numjobs=16 -runtime=30 -group_reporting &
    fio_pid=$!
    sleep 5s

    ps -ef | grep juicefs-$old_version.py
    old_python_pid=$(ps -ef | grep juicefs-$old_version.py | grep -v grep | awk '{print $2}')
    ps -ef | awk -v var=$old_python_pid '$3 == var'
    old_mount_pid=$(ps -ef | awk -v var=$old_python_pid '$3 == var' | awk '{print $2}')
    version=$(/root/.juicefs/jfsmount -V | awk '{print $3,$4,$5}')
    echo old_python_pid is $old_python_pid, old_mount_pid is $old_mount_pid, version is $version

    grep Version /tmp/jfs/.jfsconfig | grep "$version"
    echo hello > /tmp/jfs/test
    # cp -f cmd/mount/mount /root/.juicefs/jfsmount
    cp -f ./juicefs.py juicefs-$old_version.py
    echo "sleep 1s to wait fuse ready" && sleep 5
    abspath=$(pwd)
    mount_url=http://localhost:8081/cmd/mount/mount
    # mount_url=https://juicefs-com-static.oss-cn-shanghai.aliyuncs.com/jfs_release/main/mount
    JFS_FORCE_UPGRADE=true MOUNT_URL=$mount_url ./juicefs-$old_version.py version --upgrade --restart
    ps -ef | grep mount
    wget -q $mount_url -O mount.main && chmod +x mount.main
    version=$(./mount.main version | awk '{print $3}')
    echo version is $version
    grep Version /tmp/jfs/.jfsconfig 
    grep Version /tmp/jfs/.jfsconfig | grep "$version" || (echo "version not match" && exit 1)
    count=$(ps -ef | awk -v var=$old_python_pid '$2 == var ' | wc -l)
    [ $count != 0 ] && echo "old juicefs.py process is not killed" && exit 1 || true
    count=$(ps -ef | awk -v var=$old_mount_pid '$2 == var ' | wc -l)
    [ $count != 0 ] && echo "old mount process is not killed" && exit 1 || true
    rm -rf /var/jfsCache/test-volume/raw || true
    cat /tmp/jfs/test | grep hello
    # umount /tmp/jfs and check the mount process exited
    kill -9 $fio_pid || true
    umount_jfs /tmp/jfs 
    ps -ef | grep juicefs-$old_version.py
    old_python_pid=$(ps -ef | grep juicefs-$old_version.py | grep -v grep | awk '{print $2}')
    ps -ef | awk -v var=$old_python_pid '$3 == var'
    old_mount_pid=$(ps -ef | awk -v var=$old_python_pid '$3 == var' | awk '{print $2}')
    count=$(ps -ef | awk -v var=$old_python_pid '$2 == var ' | wc -l)
    [ $count != 0 ] && echo "old juicefs.py process is not killed" && exit 1 || true
    count=$(ps -ef | awk -v var=$old_mount_pid '$2 == var ' | wc -l)
    [ $count != 0 ] && echo "old mount process is not killed" && exit 1 || true
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