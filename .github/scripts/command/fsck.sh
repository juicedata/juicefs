#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_fix_nlink(){
    if [[ "$META" == "sqlite3" ]]; then
        do_fix_nlink_sqlite3
    fi
}
do_fix_nlink_sqlite3(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/a
    mkdir /jfs/a/b
    touch /jfs/a/c
    sleep 4s # to wait dir stat update
    ./juicefs fsck $META_URL --path / -r
    sqlite3 test.db "update jfs_node set nlink=100 where inode=2"
    sqlite3 test.db "select nlink from jfs_node where inode=2"
    ./juicefs fsck $META_URL --path / -r && exit 1 || true
    ./juicefs fsck $META_URL --path / -r --repair
    ./juicefs fsck $META_URL --path / -r
}

test_sync_dir_stat()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /d --depth 15 --dirs 2 --files 100 --threads 10 & 
    pid=$!
    sleep 15s
    kill -9 $pid
    ./juicefs info -r /jfs/d
    ./juicefs info -r /jfs/d --strict 
    ./juicefs fsck $META_URL --path /d --sync-dir-stat --repair -r
    ./juicefs info -r /jfs/d | tee info1.log
    ./juicefs info -r /jfs/d --strict | tee info2.log
    diff info1.log info2.log
}

source .github/scripts/common/run_test.sh && run_test $@
