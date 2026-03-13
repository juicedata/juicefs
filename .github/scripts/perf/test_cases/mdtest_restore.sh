#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    # cmd/mount/mount mdtest /tmp/jfs/mdtest --depth 0 --dirs 1 --files 10000 --threads 100
    cmd/mount/mount mdtest /tmp/jfs/mdtest --depth 2 --dirs 10  --files 10 --threads 100 --write 8192
    cmd/mount/mount rmr /tmp/jfs/mdtest
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    time cmd/mount/mount restore --conf-dir=deploy/docker test-volume -k mdtest 2>&1
}

parse_result() {
    parse_real_time
}