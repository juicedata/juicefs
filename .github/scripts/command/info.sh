#!/bin/bash -e

sudo dpkg -s redis-tools || sudo .github/scripts/apt_install.sh redis-tools
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_info_big_file(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/zero of=/jfs/bigfile bs=1M count=4096
    ./juicefs info /jfs/bigfile
    ./juicefs rmr /jfs/bigfile
    df -h /jfs
}

source .github/scripts/common/run_test.sh && run_test $@
