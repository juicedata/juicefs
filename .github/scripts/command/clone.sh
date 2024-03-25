#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_clone_preserve_with_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch /jfs/test
    for mode in 777 755 644; do
        sudo -u juicefs chmod $mode /jfs/test
        check_guid_after_clone true
        check_guid_after_clone false
    done
}

test_clone_preserve_with_dir()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs mkdir /jfs/test
    for mode in 777 755 644; do
        sudo -u juicefs chmod $mode /jfs/test
        check_guid_after_clone true
        check_guid_after_clone false
    done
}

test_clone_with_jfs_source()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    [[ ! -d /jfs/juicefs ]] && git clone https://github.com/juicedata/juicefs.git /jfs/juicefs --depth 1
    do_clone true
    do_clone false
}

skip_test_clone_with_fsrand()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    seed=$(date +%s)
    python3 .github/scripts/fsrand.py -a -c 2000 -s $seed  /jfs/juicefs
    do_clone true
    do_clone false 
}

do_clone()
{
    is_preserve=$1
    rm -rf /jfs/juicefs1
    rm -rf /jfs/juicefs2
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    cp -r /jfs/juicefs /jfs/juicefs1 $preserve
    ./juicefs clone /jfs/juicefs /jfs/juicefs2 $preserve
    diff -ur /jfs/juicefs1 /jfs/juicefs2 --no-dereference
    cd /jfs/juicefs1/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 >/tmp/log1 && cd -
    cd /jfs/juicefs2/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 >/tmp/log2 && cd -
    diff -u /tmp/log1 /tmp/log2
}

test_clone_with_big_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/test bs=1M count=1000
    cp /tmp/test /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}
test_clone_with_big_file2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/test bs=1M count=1000
    echo "a" | tee -a /tmp/test
    cp /tmp/test /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}

test_clone_with_random_write(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    PATH1=/tmp/test PATH2=/jfs/test python3 .github/scripts/random_read_write.py 
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}

test_clone_with_sparse_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    fallocate -l 1.0001g /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    diff /jfs/test /jfs/test1
}

test_clone_with_sparse_file2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    fallocate -l 1.1T /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
}

test_clone_with_small_files(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/test
    for i in $(seq 1 2000); do
        echo $i > /jfs/test/$i
    done
    ./juicefs clone /jfs/test /jfs/test1
    diff -ur /jfs/test1 /jfs/test1
}

skip_test_clone_with_mdtest1()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /test --depth 2 --dirs 10 --files 10 --threads 100 --write 8192
    ./juicefs clone /jfs/test /jfs/test1
    ./juicefs rmr /jfs/test
    ./juicefs rmr /jfs/test1
}

skip_test_clone_with_mdtest2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /test --depth 1 --dirs 1 --files 1000 --threads 100 --write 8192
    ./juicefs clone /jfs/test /jfs/test1
    ./juicefs rmr /jfs/test
    ./juicefs rmr /jfs/test1
}

check_guid_after_clone(){
    is_preserve=$1
    echo "check_guid_after_clone, is_preserve: $is_preserve"
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    rm /jfs/test1 -rf
    sleep 3
    ls /jfs/test1 && echo "test1 should not exist" && exit 1 || echo "/jfs/test1 not exist" 
    rm /jfs/test2 -rf
    ./juicefs clone /jfs/test /jfs/test1 $preserve
    cp /jfs/test /jfs/test2 -rf $preserve
    uid1=$(stat -c %u /jfs/test1)
    gid1=$(stat -c %g /jfs/test1)
    mode1=$(stat -c %a /jfs/test1)
    uid2=$(stat -c %u /jfs/test2)
    gid2=$(stat -c %g /jfs/test2)
    mode2=$(stat -c %a /jfs/test2)

    if [[ "$uid1" != "$uid2" ]] || [[ "$gid1" != "$gid2" ]] || [[ "$mode1" != "$mode2" ]]; then
        echo >&2 "<FATAL>: clone does not same as cp: uid1: $uid1, uid2: $uid2, gid1: $gid1, gid2: $gid2, mode1: $mode1, mode2: $mode2"
        exit 1
    fi
}

source .github/scripts/common/run_test.sh && run_test $@

