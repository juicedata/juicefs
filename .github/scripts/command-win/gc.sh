#!/bin/bash -e

source .github/scripts/common/common_win.sh
[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1


test_delay_delete_slice_after_compaction(){
    if [[ "$META_URL" != redis* ]]; then
        echo "this test only runs for redis meta engine"
        return
    fi
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs --trash-days 1
    ./juicefs.exe mount -d $META_URL z: --no-usage-report
    redis-cli save
    # don't skip files when gc compact
    export JFS_SKIPPED_TIME=1
    ./juicefs.exe gc --compact --delete $META_URL
    ./juicefs.exe fsck $META_URL
}

test_gc_trash_slices(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --no-usage-report
    PATH1=test PATH2=z:\\test python3 .github/scripts/random_read_write.py 
    ./juicefs.exe status --more $META_URL
    ./juicefs.exe config $META_URL --trash-days 0 --yes
    ./juicefs.exe gc $META_URL 
    ./juicefs.exe gc $META_URL --delete
    ./juicefs.exe status --more $META_URL
}

source .github/scripts/common/run_test.sh && run_test $@
