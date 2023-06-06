#!/bin/bash
set -e
python3 -c "import minio" || sudo pip install minio 
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

test_clone()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
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
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

