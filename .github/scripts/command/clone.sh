#!/bin/bash
set -e


test_clone_preserve_with_file()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch /jfs/test
    sudo -u juicefs chmod 777 /jfs/test
    check_guid_after_clone true
    check_guid_after_clone false
}

test_clone_preserve_with_dir()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs mkdir /jfs/test
    sudo -u juicefs chmod 777 /jfs/test
    check_guid_after_clone true
    check_guid_after_clone false
}

test_clone()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs
    [[ ! -d /jfs/juicefs ]] && git clone https://github.com/juicedata/juicefs.git /jfs/juicefs
    rm -rf /jfs/juicefs1
    ./juicefs clone /jfs/juicefs /jfs/juicefs1
    diff -ur /jfs/juicefs /jfs/juicefs1
    rm /jfs/juicefs1 -rf
}

check_guid_after_clone(){
    is_preserve=$1
    echo "check_guid_after_clone, is_preserve: $is_preserve"
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    rm /jfs/test1 -rf
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

prepare_test()
{
    umount /jfs && sleep 3s || true
    umount /jfs2 && sleep 3s || true
    rm -rf test.db test2.db || true
    rm -rf /var/jfs/myjfs || true
    # mc rm --force --recursive myminio/test
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

