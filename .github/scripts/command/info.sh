#!/bin/bash
set -e

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_info_big_file(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/jfs/bigfile bs=16M count=1024
    ./juicefs info /jfs/bigfile
}

prepare_test()
{
    umount_jfs /jfs $META_URL
    ls -l /jfs/.config && exit 1 || true
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}

source .github/scripts/common/run_test.sh && run_test $@
