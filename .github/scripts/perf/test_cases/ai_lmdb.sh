#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=30
COMPARISON_MODE="higher_is_better"

prepare() {
    prepare0 $@
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    source venv/bin/activate
    python3 "$(dirname "$0")/ai_format_benchmark.py" /tmp/jfs --benchmark lmdb --quick
}

parse_result() {
    grep "AVERAGE_THROUGHPUT:" | awk '{print $2}'
}
