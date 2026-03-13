#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    cmd/mount/mount mdtest /tmp/jfs/mdtest --depth 3 --dirs 10  --files 10 --threads 100 --write 8192
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    time cmd/mount/mount destroy --conf-dir=deploy/docker test-volume test-volume-abc123 --force 2>&1
}

parse_result() {
    parse_real_time
}