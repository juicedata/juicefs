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
    rm info*.log
    ./juicefs fsck $META_URL --path / --sync-dir-stat --repair -r
    ./juicefs info -r /jfs | tee info1.log
    ./juicefs info -r /jfs --strict | tee info2.log
    diff info1.log info2.log
}

test_fsck_with_random_test()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./random-test runOp -baseDir /jfs/test -files 500000 -ops 5000000 -threads 50 -dirSize 100 -duration 30s -createOp 30,uniform -deleteOp 5,end --linkOp 10,uniform  --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform    
    ./juicefs fsck $META_URL --path /test --sync-dir-stat --repair -r
    ./juicefs info -r /jfs | tee info1.log
    ./juicefs info -r /jfs --strict | tee info2.log
    diff info1.log info2.log || true
}

test_fsck_delete_object()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    echo "test" > /jfs/test.txt
    sleep 1
    object=$(./juicefs info /jfs/test.txt | grep chunks | awk '{print $4}')
    rm /var/jfs/$object
    ./juicefs fsck $META_URL 2>&1 | tee fsck.log
    grep -q "1 objects are lost" fsck.log || exit 1
    rm fsck.log
 #   ./juicefs fsck $META_URL --path / --sync-dir-stat --repair -r 2>&1 | tee fsck.log
 #   grep -q "1 objects are lost" fsck.log || exit 1
 #   rm fsck.log
    ./juicefs rmr /jfs/test.txt --skip-trash
    ./juicefs fsck $META_URL || { echo "files is deleted, fsck should success"; exit 1; }
}

test_sync_dir_df()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /d --depth 15 --dirs 2 --files 100 --threads 10 & 
    pid=$!
    sleep 60s
    kill -9 $pid
    ./juicefs info -r /jfs/d --strict
    #df -h /jfs的Used和
    df -h /jfs
    ./juicefs fsck $META_URL --path /d --sync-dir-stat --repair -r
    ./juicefs info -r /jfs/d | tee info1.log
    ./juicefs info -r /jfs/d --strict | tee info2.log
    diff info1.log info2.log
    rm info*.log
    ./juicefs fsck $META_URL --path / --sync-dir-stat --repair -r
    ./juicefs info -r /jfs | tee info1.log
    ./juicefs info -r /jfs --strict | tee info2.log
    diff info1.log info2.log
}

source .github/scripts/common/run_test.sh && run_test $@
