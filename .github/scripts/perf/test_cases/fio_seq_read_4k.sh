#!/bin/bash -e
source "$(dirname "$0")/common.sh"
THRESHOLD=20
COMPARISON_MODE="higher_is_better"

prepare() {
    prepare0 $@
    echo 3 > /proc/sys/vm/drop_caches
}

run_test() {
    mkdir -p /tmp/jfs/fio
    fio --name=big-read-multiple \
        --filename="/tmp/jfs/fio/fio_test_$(date +%Y%m%d_%H%M%S).dat" \
        --group_reporting --runtime=300 \
        --rw=read --direct=1 --bs=4k --ramp_time=10s \
        --numjobs=8 --nrfiles=1 --size=2G --output-format=normal
}

parse_result() {
    parse_fio_iops
}
