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
    fio --name=big-read-multiple-concurrent \
        --ramp_time=10s \
        --directory=/tmp/jfs/fio \
        --group_reporting \
        --rw=read --direct=1 --bs=1m \
        --numjobs=8 --nrfiles=8 --openfiles=1 --size=2G \
        --output-format=normal --runtime=120
}

parse_result() {
    parse_fio_iops
}