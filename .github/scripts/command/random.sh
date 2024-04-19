#!/bin/bash -e
source .github/scripts/common/common.sh
[[ -z "$MAX_EXAMPLE" ]] && MAX_EXAMPLE=100
[[ -z "$STEP_COUNT" ]] && STEP_COUNT=50

[[ -z "$META1" ]] && META1=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META1
META_URL1=$(get_meta_url $META1)

[[ -z "$META2" ]] && META2=redis
source .github/scripts/start_meta_engine.sh
start_meta_engine $META2
META_URL2=$(get_meta_url $META2)

prepare_test()
{
    meta_url=$1
    mp=$2
    volume=$3
    shift 3
    options=$@
    umount_jfs $mp $meta_url
    python3 .github/scripts/flush_meta.py $meta_url
    rm -rf /var/jfs/$volume || true
    rm -rf /var/jfsCache/$volume || true
    ./juicefs format $meta_url $volume $options
    ./juicefs mount -d $meta_url $mp
}

test_run_examples()
{
    prepare_test $META_URL1 /tmp/jfs1 myjfs1 --enable-acl --trash-days 0
    prepare_test $META_URL2 /tmp/jfs2 myjfs2 --enable-acl --trash-days 0
    python3 .github/scripts/hypo/command_test.py
}

test_run_all()
{
    prepare_test $META_URL1 /tmp/jfs1 myjfs1
    prepare_test $META_URL2 /tmp/jfs2 myjfs2
    CHECK_NLINK=false MAX_EXAMPLE=$MAX_EXAMPLE STEP_COUNT=$STEP_COUNT python3 .github/scripts/hypo/command.py
}

source .github/scripts/common/run_test.sh && run_test $@

