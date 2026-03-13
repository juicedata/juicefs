#!/bin/bash -e
source "$(dirname "$0")/common.sh"
source .github/scripts/start_meta_engine.sh
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    # cmd/mount/mount mdtest /tmp/jfs/mdtest --depth 0 --dirs 1 --files 10000 --threads 100
    ./juicefs mdtest $(get_meta_url $META) /mdtest --depth 2 --dirs 10  --files 10 --threads 100 --write 8192
    ./juicefs rmr /tmp/jfs/mdtest
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    trash_dir=$(ls /tmp/jfs/.trash)
    time ./juicefs restore $(get_meta_url $META) $trash_dir --put-back 2>&1
}

parse_result() {
    parse_real_time
}