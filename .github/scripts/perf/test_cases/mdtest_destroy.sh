#!/bin/bash -e
source "$(dirname "$0")/common.sh"
source .github/scripts/start_meta_engine.sh

THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    ./juicefs mdtest $(get_meta_url $META) /mdtest --depth 3 --dirs 10  --files 10 --threads 100 --write 8192
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    time ./juicefs destroy --force $(get_meta_url $META) 2>&1
}

parse_result() {
    parse_real_time
}