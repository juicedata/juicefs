#!/bin/bash
set -e


test_clone_preserve_guid_with_file()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch /jfs/test
    sudo -u juicefs chmod 654 /jfs/test
    check_guid_after_clone 1101 1101 654
}

test_clone_preserve_guid_with_dir()
{
    prepare_test
    ./juicefs format sqlite3://test.db myjfs
    ./juicefs mount -d sqlite3://test.db /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs mkdir /jfs/test
    sudo -u juicefs chmod 654 /jfs/test
    check_guid_after_clone 1101 1101 654
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
    expected_uid=$1
    expected_gid=$2
    expected_mode=$3
    ./juicefs clone /jfs/test /jfs/test1
    uid=$(stat -c %u /jfs/test1)
    gid=$(stat -c %g /jfs/test1)
    mode=$(stat -c %a /jfs/test1)
    if [[ $uid != 0 ]] || [[ $gid != 0 ]] || [[ "$mode" != "755" ]]; then
        echo "uid or gid or mode should not be preserved, uid: $uid, gid: $gid, mode: $mode"
        echo "expected_uid: 0, expected_gid: 0, expected_mode: 755"
        exit 1
    fi

    rm /jfs/test1 -rf 
    ./juicefs clone /jfs/test /jfs/test1 --preserve
    uid=$(stat -c %u /jfs/test1)
    gid=$(stat -c %g /jfs/test1)
    mode=$(stat -c %a /jfs/test1)
    if [[ $uid != "$expected_gid" ]] || [[ $gid != "$expected_gid" ]] || [[ "$mode" != "$expected_mode" ]]; then
        echo "uid or gid should be preserved: uid: $uid, gid: $gid, mode: $mode"
        echo "expected_uid: $expected_uid, expected_gid: $expected_gid, expected_mode: $expected_mode"
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

