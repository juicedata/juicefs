#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    cmd/mount/mount mdtest /tmp/jfs/mdtest --depth 0 --dirs 1 --files 10000 --threads 100
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    { time ls "/tmp/jfs/mdtest/mdtest_tree.0/" > /dev/null 2>&1; } 2>&1
}

parse_result() {
    parse_real_time
}