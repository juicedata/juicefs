#!/bin/bash -e

python3 -c "import xattr" || pip install xattr 
dpkg -s redis-tools || .github/scripts/apt_install.sh redis-tools
dpkg -s fio || .github/scripts/apt_install.sh fio
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_delay_delete_slice_after_compaction(){
    if [[ "$META" != redis* ]]; then
        echo "this test only runs for redis meta engine"
        return
    fi
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 1
    ./juicefs mount -d $META_URL /jfs --no-usage-report
    fio --name=abc --rw=randwrite --refill_buffers --size=500M --bs=256k --directory=/jfs
    redis-cli save
    # don't skip files when gc compact
    export JFS_SKIPPED_TIME=1
    ./juicefs gc --compact --delete $META_URL
    killall -9 redis-server
    sleep 3
    ./juicefs fsck $META_URL
}

test_gc_trash_slices(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    PATH1=/tmp/test PATH2=/jfs/test python3 .github/scripts/random_read_write.py 
    ./juicefs status --more $META_URL
    ./juicefs config $META_URL --trash-days 0 --yes
    ./juicefs gc $META_URL 
    ./juicefs gc $META_URL --delete
    ./juicefs status --more $META_URL
}

test_gc_trash_files(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand
    rm -rf /jfs/fsrand
    ./juicefs status --more $META_URL
    ./juicefs config $META_URL --trash-days 0 --yes
    ./juicefs gc $META_URL 
    ./juicefs gc $META_URL --delete
    ./juicefs status --more $META_URL
}

source .github/scripts/common/run_test.sh && run_test $@
