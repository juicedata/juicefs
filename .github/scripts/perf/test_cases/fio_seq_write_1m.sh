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
    fio --name=big-write \
        --directory=/tmp/jfs/fio \
        --group_reporting \
        --rw=write --direct=1 --bs=1m --end_fsync=1 --runtime=120 \
        --numjobs=8 --nrfiles=8 --size=2G --output-format=normal
}

parse_result() {
    parse_fio_iops
}
