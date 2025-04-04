#!/bin/bash -e
source .github/scripts/common/common_win.sh


[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1


test_sync_dir_stat()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    ./juicefs.exe mdtest $META_URL //d --depth 15 --dirs 2 --files 100 --threads 10 & 
    pid=$!
    sleep 15s
    kill -9 $pid
    ./juicefs.exe info -r /z/d
    ./juicefs.exe info -r /z/d --strict 
    ./juicefs.exe fsck $META_URL --path //d --sync-dir-stat --repair -r
    ./juicefs.exe info -r /z/d | tee info1.log
    ./juicefs.exe info -r /z/d --strict | tee info2.log
    diff info1.log info2.log
}

source .github/scripts/common/run_test.sh && run_test $@
