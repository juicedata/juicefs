#!/bin/bash -e
source "$(dirname "$0")/common.sh"
source .github/scripts/start_meta_engine.sh
THRESHOLD=20
COMPARISON_MODE="lower_is_better"

prepare() {
    prepare0 $@
    ./juicefs mdtest $(get_meta_url $META) /mdtest --depth 0 --dirs 1 --files 10000 --threads 100
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    time ./juicefs rmr /tmp/jfs/mdtest 2>&1
}

parse_result() {
    # Extract real time in seconds from: real    0m1.234s
    grep "^real" | head -1 | awk '{
        time_str = $2
        gsub(/s$/, "", time_str)
        split(time_str, parts, "m")
        printf "%.3f\n", parts[1] * 60 + parts[2]
    }'
}