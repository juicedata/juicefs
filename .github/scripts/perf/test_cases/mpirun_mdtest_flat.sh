#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=20
COMPARISON_MODE="higher_is_better"

prepare() {
    prepare0 $@
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    mpirun --use-hwthread-cpus --allow-run-as-root -np 4 mdtest -F -w 102400 -I 10000 -z 0 -d /tmp/jfs/mdtest
}

parse_result() {
    parse_mpirun_ops
}