#!/bin/bash -e
source "$(dirname "$0")/common.sh"
source .github/scripts/start_meta_engine.sh
THRESHOLD=20
COMPARISON_MODE="higher_is_better"

prepare() {
    prepare0 $@
}

run_test() {
    ./juicefs mdtest $(get_meta_url $META) /mdtest --depth 0 --dirs 1 --files 10000 --threads 100 --write 8192 2>&1
}

parse_result() {
    # Extract files/s from output like: processed 50000 files (1234.56 files/s)
    sed -n 's/.*(\([0-9.]*\) files\/s).*/\1/p' | tail -1
}