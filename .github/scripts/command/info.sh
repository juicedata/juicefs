#!/bin/bash
set -ex
python3 -c "import minio" || sudo pip install minio 
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

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
    ./juicefs fsck $META_URL --path /d --sync-dir-stat --repair -r -v
    ./juicefs info -r /jfs/d | tee info1.log
    ./juicefs info -r /jfs/d --strict | tee info2.log
    diff info1.log info2.log
}

prepare_test()
{
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
}


function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done
