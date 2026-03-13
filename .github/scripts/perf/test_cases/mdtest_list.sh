#!/bin/bash -e
source "$(dirname "$0")/common.sh"
source .github/scripts/start_meta_engine.sh
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    ./juicefs mdtest $(get_meta_url $META) /mdtest --depth 0 --dirs 1 --files 5000 --threads 100
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    { time ls /tmp/jfs/mdtest/test-dir.0-0/mdtest_tree.0/ > /dev/null 2>&1; } 2>&1
}

parse_result() {
    parse_real_time
}