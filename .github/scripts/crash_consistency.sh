#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=redis
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

MANIFEST=/tmp/jfs_crash_manifest.txt
CACHE_DIR=/var/jfsCache-crash
FILE_COUNT=100000
FILE_SIZE=4096
WRITE_SECONDS=15

# Mount and confirm it is actually serving: a remount that failed to come up
# would otherwise read as thousands of lost files instead of a mount error.
mount_jfs(){
    ./juicefs mount -d "$@"
    sleep 2
    if ! stat /jfs/.accesslog >/dev/null 2>&1; then
        echo "<FATAL>: mount is not serving, refusing to call that data loss"
        exit 1
    fi
}

# Kill the mount the way a power loss would: no unmount, no flush, no chance to
# finish an upload. Everything the writer already recorded has to survive.
#
# The mount point has to be part of the match. CI also runs a juicefs mount for
# the coverage directory, and killing that one instead would leave /jfs healthy:
# the test would then pass without ever having crashed anything.
kill_mount_hard(){
    local pid
    pid=$(ps -ef | grep "juicefs mount" | grep -v grep | grep -w "/jfs" | awk '{print $2}' | head -1)
    [[ -z "$pid" ]] && echo "<FATAL>: no mount process for /jfs found to kill" && exit 1
    echo "killing mount process $pid"
    sudo kill -9 "$pid"
    wait_mount_process_killed "$pid" 30
    # the mount point is stale after the kill, detach it before remounting
    sudo umount -l /jfs 2>/dev/null || sudo fusermount -uz /jfs 2>/dev/null || true
}

start_writer(){
    rm -f $MANIFEST
    python3 .github/scripts/crash_writer.py /jfs $MANIFEST $FILE_COUNT $FILE_SIZE &
    WRITER_PID=$!
    # let it get far enough in that the kill lands mid-stream
    sleep $WRITE_SECONDS
}

stop_writer(){
    wait $WRITER_PID 2>/dev/null || true
    local recorded
    recorded=$(wc -l < $MANIFEST)
    echo "writer recorded $recorded files as durable"
    [[ "$recorded" -lt 1 ]] && echo "<FATAL>: nothing was fsynced, the test proves nothing" && exit 1
    return 0
}

# Staged blocks upload in the background after a writeback mount restarts.
# The staged blocks are picked up by a periodic scan, so draining takes about a
# minute even when it works; leave room for a loaded runner and a slow engine.
wait_staging_drained(){
    local waited=0
    while [[ $waited -lt 240 ]]; do
        if [[ -z "$(find $CACHE_DIR -type d -name rawstaging -exec find {} -type f \; 2>/dev/null | head -1)" ]]; then
            echo "staging drained after ${waited}s"
            return 0
        fi
        sleep 5
        waited=$((waited + 5))
    done
    echo "<FATAL>: staged blocks were still not uploaded after ${waited}s"
    find $CACHE_DIR -type d -name rawstaging -exec find {} -type f \; 2>/dev/null | head -5
    exit 1
}

# Data must be in object storage, not just in the local cache that survived the
# kill. Re-read through an empty cache so every block comes from the object store.
verify_from_object_storage(){
    umount_jfs /jfs $META_URL
    rm -rf $CACHE_DIR
    mount_jfs $META_URL /jfs --cache-dir $CACHE_DIR
    python3 .github/scripts/crash_verify.py /jfs $MANIFEST
}

# A crash must not leave metadata pointing at objects that were never uploaded.
check_no_dangling_objects(){
    ./juicefs fsck $META_URL --path / -r
}

test_crash_default(){
    prepare_test
    rm -rf $CACHE_DIR
    ./juicefs format $META_URL myjfs
    mount_jfs $META_URL /jfs --cache-dir $CACHE_DIR
    start_writer
    kill_mount_hard
    stop_writer

    mount_jfs $META_URL /jfs --cache-dir $CACHE_DIR
    python3 .github/scripts/crash_verify.py /jfs $MANIFEST
    check_no_dangling_objects
    verify_from_object_storage
}

count_staged(){
    find $CACHE_DIR -type d -name rawstaging -exec find {} -type f \; 2>/dev/null | wc -l
}

# With --writeback, fsync returns once the block is staged on the local disk and
# the upload happens later. The staging directory survives the kill, so a
# restarted mount has to finish those uploads: this covers the scanStaging
# recovery path, which nothing else exercises.
#
# --upload-delay is what makes the test meaningful: without it the uploads keep
# up with the writer and the kill finds an empty staging dir, so the run passes
# without ever exercising the replay. The check below fails the test rather than
# letting it quietly prove nothing.
test_crash_writeback(){
    prepare_test
    rm -rf $CACHE_DIR
    ./juicefs format $META_URL myjfs
    mount_jfs $META_URL /jfs --cache-dir $CACHE_DIR --writeback --upload-delay 60s
    start_writer
    local staged
    staged=$(count_staged)
    echo "staged (un-uploaded) blocks at kill time: $staged"
    kill_mount_hard
    stop_writer
    if [[ "$staged" -lt 1 ]]; then
        echo "<FATAL>: nothing was left in staging, the replay path was never exercised"
        exit 1
    fi

    # same cache dir, no delay: the staged blocks are still there and the mount
    # has to upload them
    mount_jfs $META_URL /jfs --cache-dir $CACHE_DIR --writeback
    python3 .github/scripts/crash_verify.py /jfs $MANIFEST
    wait_staging_drained
    check_no_dangling_objects
    verify_from_object_storage
}

source .github/scripts/common/run_test.sh && run_test $@
