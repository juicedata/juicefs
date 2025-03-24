#!/bin/bash -e
source .github/scripts/common/common_win.sh


[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1

test_clone_preserve_with_dir()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    powershell -Command "Start-Process -Verb RunAs -FilePath 'net' -ArgumentList 'user juicefs Password123! /add' -Wait"
    mkdir z:\\test
    cmd.exe /c "icacls z:\\test /grant juicefs:(F)"
    for mode in 777 755 644; do
        cmd.exe /c "runas /user:juicefs \"icacls z:\\test /grant Everyone:(F)\""
        check_guid_after_clone true
        check_guid_after_clone false
    done
    net user juicefs /delete
}

test_clone_with_jfs_source()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    [[ ! -d /z/juicefs ]] && git clone https://github.com/juicedata/juicefs.git /z/juicefs --depth 1
    do_clone true
    do_clone false
}

do_clone()
{
    is_preserve=$1
    rm -rf /z/juicefs1
    rm -rf /z/juicefs2
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    cp -r /z/juicefs /z/juicefs1 $preserve
    ./juicefs.exe clone /z/juicefs /z/juicefs2 $preserve
    diff -ur /z/juicefs1 /z/juicefs2 --no-dereference
    cd /z/juicefs1/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 > log1 && cd -
    cd /z/juicefs2/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 > log2 && cd -
    diff -u log1 log2
}

test_clone_with_big_file()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    dd if=/dev/urandom of=./test bs=1M count=1000
    cp test z/test
    ./juicefs.exe clone /z/test /z/test1
    rm /z/test -rf
    diff test /z/test1
}

test_clone_with_random_write(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    PATH1=test PATH2=z:\\test python3 .github/scripts/random_read_write.py 
    ./juicefs.exe clone test /z/test1
    rm /z/test -rf
    diff /tmp/test /z/test1
}

test_clone_with_small_files(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    mkdir /z/test
    for i in $(seq 1 2000); do
        echo $i > /z/test/$i
    done
    ./juicefs.exe clone /z/test /z/test1
    diff -ur /z/test1 /z/test1
}

skip_test_clone_with_mdtest1()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    ./juicefs.exe mdtest $META_URL /test --depth 2 --dirs 10 --files 10 --threads 100 --write 8192
    ./juicefs.exe clone /z/test /z/test1
    ./juicefs.exe rmr /z/test
    ./juicefs.exe rmr /z/test1
}

skip_test_clone_with_mdtest2()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    ./juicefs.exe mdtest $META_URL /test --depth 1 --dirs 1 --files 1000 --threads 100 --write 8192
    ./juicefs.exe clone /z/test /z/test1
    ./juicefs.exe rmr /z/test
    ./juicefs.exe rmr /z/test1
}

check_guid_after_clone(){
    is_preserve=$1
    echo "check_guid_after_clone, is_preserve: $is_preserve"
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    rm /z/test1 -rf
    sleep 3
    ls /z/test1 && echo "test1 should not exist" && exit 1 || echo "/z/test1 not exist" 
    rm /z/test2 -rf
    ./juicefs.exe clone /z/test /z/test1 $preserve
    cp /z/test /z/test2 -rf $preserve
    uid1=$(stat -c %u /z/test1)
    gid1=$(stat -c %g /z/test1)
    mode1=$(stat -c %a /z/test1)
    uid2=$(stat -c %u /z/test2)
    gid2=$(stat -c %g /z/test2)
    mode2=$(stat -c %a /z/test2)

    if [[ "$uid1" != "$uid2" ]] || [[ "$gid1" != "$gid2" ]] || [[ "$mode1" != "$mode2" ]]; then
        echo >&2 "<FATAL>: clone does not same as cp: uid1: $uid1, uid2: $uid2, gid1: $gid1, gid2: $gid2, mode1: $mode1, mode2: $mode2"
        exit 1
    fi
}

source .github/scripts/common/run_test.sh && run_test $@

