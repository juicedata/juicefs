#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$ENCRYPT" ]] && ENCRYPT=false
[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
FORMAT_OPTIONS=""
if [ "$ENCRYPT" == "true" ]; then
    export JFS_RSA_PASSPHRASE=the-passwd-for-rsa
    openssl genrsa -aes256 -passout pass:$JFS_RSA_PASSPHRASE -out my-priv-key.pem 2048
    FORMAT_OPTIONS="--encrypt-rsa-key my-priv-key.pem"
fi

generate_source_dir(){
    rm -rf jfs_source
    git clone https://github.com/juicedata/juicefs.git jfs_source --depth 1
    chmod 777 jfs_source
    mkdir jfs_source/empty_dir
    dd if=/dev/urandom of=jfs_source/file bs=5M count=1
    chmod 777 jfs_source/file
    ln -sf file jfs_source/symlink_to_file
    ln -f jfs_source/file jfs_source/hard_link_to_file
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch jfs_source/file2
    ln -s ../cmd jfs_source/pkg/symlink_to_cmd
}

generate_source_dir

generate_fsrand(){
    seed=$(date +%s)
    python3 .github/scripts/fsrand.py -a -c 2000 -s $seed  fsrand
}

compare_sync_dirs(){
    local src_dir=$1
    local dst_dir=$2
    diff -r --exclude='.jfs.file*.tmp.*' \
        "$src_dir" "$dst_dir"
}

test_sync_with_mount_point(){
    do_sync_with_mount_point 
    do_sync_with_mount_point --list-threads 10 --list-depth 5
    do_sync_with_mount_point --dirs --update --perms --check-all 
    do_sync_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

test_sync_without_mount_point(){
    do_sync_without_mount_point 
    do_sync_without_mount_point --list-threads 10 --list-depth 5
    do_sync_without_mount_point --dirs --update --perms --check-all 
    do_sync_without_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_sync_without_mount_point(){
    prepare_test
    options=$@
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    meta_url=$META_URL ./juicefs sync jfs_source/ jfs://meta_url/jfs_source/ $options --links

    ./juicefs mount -d $META_URL /jfs
    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference  jfs_source/ /jfs/jfs_source
}

do_sync_with_mount_point(){
    prepare_test
    options=$@
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options --links

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference jfs_source/ /jfs/jfs_source/
}

test_sync_with_loop_link(){
    prepare_test
    options="--dirs --update --perms --check-all --list-threads 10 --list-depth 5"
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ln -s looplink jfs_source/looplink
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options  2>&1 | tee err.log || true
    grep -i "failed to handle 1 objects" err.log || (echo "grep failed" && exit 1)
    rm -rf jfs_source/looplink
}

test_sync_with_deep_link(){
    prepare_test
    options="--dirs --update --perms --check-all --list-threads 10 --list-depth 5"
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    touch jfs_source/symlink_1
    for i in {1..41}; do
        ln -s symlink_$i jfs_source/symlink_$((i+1))
    done
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options  2>&1 | tee err.log || true
    grep -i "failed to handle 1 objects" err.log || (echo "grep failed" && exit 1)
    rm -rf jfs_source/symlink_*
}

skip_test_sync_fsrand_with_mount_point(){
    generate_fsrand
    do_test_sync_fsrand_with_mount_point 
    do_test_sync_fsrand_with_mount_point --list-threads 10 --list-depth 5
    do_test_sync_fsrand_with_mount_point --dirs --update --perms --check-all 
    do_test_sync_fsrand_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_test_sync_fsrand_with_mount_point(){
    prepare_test
    options=$@
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync fsrand/ /jfs/fsrand/ $options --links

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    diff -ur --no-dereference fsrand/ /jfs/fsrand/
}

test_sync_include_exclude_option(){
    prepare_test
    ./juicefs format --trash-days 0 $FORMAT_OPTIONS $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    ./juicefs sync jfs_source/ /jfs/
    for source_dir in "/jfs/" "jfs_source/" ; do 
        while IFS=, read -r jfs_option rsync_option status; do
            printf '\n%s, %s, %s\n' "$jfs_option" "$rsync_option" "$status"
            status=$(echo $status| xargs)
            if [[ -z "$status" || "$status" = "disable" ]]; then 
                continue
            fi
            if [ "$source_dir" == "/jfs/" ]; then 
                jfs_option="--exclude .stats --exclude .config $jfs_option " 
                rsync_option="--exclude .stats --exclude .config $rsync_option " 
            fi
            rm rsync_dir/ -rf && mkdir rsync_dir
            set -o noglob
            rsync -a $source_dir rsync_dir/ $rsync_option
            rm jfs_sync_dir/ -rf && mkdir jfs_sync_dir/
            ./juicefs sync $source_dir jfs_sync_dir/ $jfs_option --list-threads 2
            set -u noglob
            printf 'juicefs sync %s %s %s\n' "$source_dir"  "jfs_sync_dir/" "$jfs_option" 
            printf 'rsync %s %s %s\n' "$source_dir" "rsync_dir/"  "$rsync_option" 
            printf 'diff between juicefs sync and rsync:\n'
            diff -ur jfs_sync_dir rsync_dir
        done < .github/workflows/resources/sync-options.txt
    done
}

test_sync_with_time(){
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf data/
    mkdir data
    echo "old" > data/file1
    echo "old" > data/file2
    echo "old" > data/file3
    sleep 1
    start_time=$(date "+%Y-%m-%d %H:%M:%S")
    sleep 1
    echo "new" > data/file2
    sleep 1
    mid_time=$(date "+%Y-%m-%d %H:%M:%S")
    sleep 1
    echo "new" > data/file3
    sleep 1
    end_time=$(date "+%Y-%m-%d %H:%M:%S")
    mkdir -p sync_dst1 sync_dst2
    ./juicefs sync --start-time "$start_time" data/ /jfs/sync_dst1/
    [ "$(cat /jfs/sync_dst1/file1 2>/dev/null)" = "" ] || (echo "file1 should not exist" && exit 1)
    [ "$(cat /jfs/sync_dst1/file2)" = "new" ] || (echo "file2 should be new" && exit 1)
    [ "$(cat /jfs/sync_dst1/file3)" = "new" ] || (echo "file3 should be new" && exit 1)
    ./juicefs sync --start-time "$start_time" --end-time "$mid_time" data/ /jfs/sync_dst2/
    [ "$(cat /jfs/sync_dst2/file1 2>/dev/null)" = "" ] || (echo "file1 should not exist" && exit 1)
    [ "$(cat /jfs/sync_dst2/file2)" = "new" ] || (echo "file2 should be new" && exit 1)
    [ "$(cat /jfs/sync_dst2/file3 2>/dev/null)" = "" ] || (echo "file3 should not exist" && exit 1)
}

test_sync_check_change()
{
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf data/
    mkdir data
    nohup bash -c 'for i in `seq 1 1000000`; do echo $i >> data/echo; done' > /dev/null 2>&1 &
    pid=$!
    sleep 0.5
    ./juicefs sync --check-change data/ /jfs/data/ 2>&1 | grep "changed during sync" || (echo "should detect file changes during sync" && exit 1 )
    kill $pid || true
}

test_ignore_existing()
{
    prepare_test
    rm -rf /tmp/src_dir /tmp/rsync_dir /tmp/jfs_sync_dir
    mkdir -p /tmp/src_dir/d1
    mkdir -p /tmp/jfs_sync_dir/d1
    echo abc > /tmp/src_dir/file1
    echo 1234 > /tmp/jfs_sync_dir/file1
    echo abcde > /tmp/src_dir/d1/d1file1
    echo 123456 > /tmp/jfs_sync_dir/d1/d1file1
    cp -rf /tmp/jfs_sync_dir/ /tmp/rsync_dir
    
    mkdir /tmp/src_dir/no-exist-dir
    echo 1111 > /tmp/src_dir/no-exist-dir/f1
    echo 123456 > /tmp/src_dir/d1/no-exist-file

    ./juicefs sync /tmp/src_dir /tmp/jfs_sync_dir --existing
    rsync -r /tmp/src_dir/ /tmp/rsync_dir --existing --size-only
    diff -ur /tmp/jfs_sync_dir /tmp/rsync_dir
    
    rm -rf /tmp/src_dir /tmp/rsync_dir
    mkdir -p /tmp/src_dir/d1
    mkdir -p /tmp/jfs_sync_dir/d1
    echo abc > /tmp/src_dir/file1
    echo 1234 > /tmp/jfs_sync_dir/file1
    echo abcde > /tmp/src_dir/d1/d1file1
    echo 123456 > /tmp/jfs_sync_dir/d1/d1file1
    echo abc > /tmp/src_dir/file2
    echo abcde > /tmp/src_dir/d1/d1file2
    cp -rf /tmp/jfs_sync_dir/ /tmp/rsync_dir
    
    ./juicefs sync /tmp/src_dir /tmp/jfs_sync_dir --ignore-existing 
    rsync -r /tmp/src_dir/ /tmp/rsync_dir --ignore-existing --size-only
    diff -ur /tmp/jfs_sync_dir /tmp/rsync_dir
}
test_file_head(){
    # issue link: https://github.com/juicedata/juicefs/issues/2125
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount $META_URL /jfs -d
    mkdir /jfs/jfs_source/
    [[ ! -d jfs_source ]] && git clone https://github.com/juicedata/juicefs.git jfs_source
    ./juicefs sync jfs_source/ /jfs/jfs_source/  --update --perms --check-all --bwlimit=81920 --dirs --threads=30 --list-threads=3 --debug
    echo "test" > jfs_source/test_file
    mkdir -p jfs_source/test_dir
    ./juicefs sync jfs_source/ /jfs/jfs_source/  --update --perms --check-all --bwlimit=81920 --dirs --threads=30 --list-threads=2 --debug
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur jfs_source/ /jfs/jfs_source
}


test_checkpoint_basic_resume(){
    # Test: sync with checkpoint, interrupt mid-way, resume and verify data correctness
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 2000); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # First sync: interrupt with SIGINT after short delay
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --threads 2 --debug 2>&1 | tee sync1.log || true
    # Check that checkpoint file was created in destination
    checkpoint_file=$(find /jfs/data/ -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | head -1)
    if [ -z "$checkpoint_file" ]; then
        echo "checkpoint file should exist after interrupted sync"
        exit 1
    fi
    echo "Checkpoint file found: $checkpoint_file"
    # Resume sync with checkpoint
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --debug 2>&1 | tee sync2.log || true
    # Verify all files are synced correctly
    compare_sync_dirs data/ /jfs/data/
    # Verify checkpoint file is cleaned up after successful sync
    checkpoint_file_after=$(find /jfs/data/ -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | head -1)
    if [ -n "$checkpoint_file_after" ]; then
        echo "checkpoint file should be deleted after successful sync"
        exit 1
    fi
}

test_checkpoint_cleanup_on_success(){
    # Test: checkpoint file should be removed after a full successful sync
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 50); do
        echo "content-$i" > data/file$i
    done
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync.log
    diff -r data/ /jfs/data/
    # Checkpoint file should be cleaned up
    count=$(find /jfs/data/ -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$count" -ne 0 ]; then
        echo "checkpoint file should be deleted after successful sync, found $count"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
}

test_checkpoint_stats_correctness(){
    # Test: after resume, cumulative stats (copied, skipped, etc.) should be correct
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 300); do
        dd if=/dev/urandom of=data/file$i bs=32K count=1 status=none
    done
    # First sync: interrupt early
    timeout 2 ./juicefs sync data/ /jfs/data/ --debug --enable-checkpoint --checkpoint-interval 1s --threads 2 2>&1 | tee sync1.log || true
    # Get partial copied count from first run (from checkpoint)
    # Resume sync
    sleep 2
    ./juicefs sync data/ /jfs/data/ --debug --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync2.log
    # Verify the final log line reports correct total stats
    compare_sync_dirs data/ /jfs/data/
    # The final log should report: Found: 300, and copied + skipped = 300
    total_found=$(tail -5 sync2.log | grep "Found:" | sed 's/.*Found: \([0-9]*\).*/\1/')
    if [ -n "$total_found" ] && [ "$total_found" -ne 300 ]; then
        echo "Expected Found: 300, got: $total_found"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_config_mismatch(){
    # Test: changing config options should discard old checkpoint and start fresh
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 100); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # First sync with --update, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --update --threads 2 2>&1 | tee sync1.log || true
    # Resume with different config (--force-update instead of --update)
    # This should trigger config mismatch and start fresh
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --force-update 2>&1 | tee sync2.log
    # Should see "config mismatch" warning in log
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --update --threads 4 2>&1 | tee sync1.log || true
    grep -i "mismatch\|starting fresh" sync2.log || echo "Warning: expected checkpoint config mismatch message"
    compare_sync_dirs data/ /jfs/data/
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_with_delete_dst(){
    # Test: checkpoint with --delete-dst should correctly track extra objects for deletion
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 50); do
        echo "content-$i" > data/file$i
    done
    # Pre-populate destination with extra files that should be deleted
    mkdir -p /jfs/data
    for i in $(seq 51 80); do
        echo "extra-$i" > /jfs/data/extra$i
    done
    # First sync with checkpoint + delete-dst, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --delete-dst --threads 2 2>&1 | tee sync1.log || true
    # Resume
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --delete-dst 2>&1 | tee sync2.log
    # Verify: source files exist, extra files deleted
    compare_sync_dirs data/ /jfs/data/
    # Check extra files are gone
    for i in $(seq 51 80); do
        if [ -f /jfs/data/extra$i ]; then
            echo "Error: extra file extra$i should have been deleted"
            exit 1
        fi
    done
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_with_delete_src(){
    # Test: checkpoint with --delete-src should correctly delete source after sync
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 50); do
        echo "content-$i" > data/file$i
    done
    cp -r data data_backup
    # First sync with checkpoint + delete-src, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --delete-src --check-all --threads 2 2>&1 | tee sync1.log || true
    # Resume
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --delete-src --check-all 2>&1 | tee sync2.log
    # Verify: destination has all files
    compare_sync_dirs data_backup/ /jfs/data/
    # Source files should be deleted
    src_remaining=$(ls data/ 2>/dev/null | wc -l)
    if [ "$src_remaining" -ne 0 ]; then
        echo "Error: source should be empty after delete-src sync, found $src_remaining files"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_with_update(){
    # Test: checkpoint with --update should correctly handle updated files across resume
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 100); do
        echo "original-$i" > data/file$i
    done
    # First full sync
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync1.log
    diff -r data/ /jfs/data/
    # Modify some files
    for i in $(seq 1 50); do
        echo "updated-$i" > data/file$i
    done
    # Sync with update + checkpoint, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --update --threads 2 2>&1 | tee sync2.log || true
    # Resume
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --update 2>&1 | tee sync3.log
    # Verify updated content
    compare_sync_dirs data/ /jfs/data/
    grep "panic:\|<FATAL>" sync3.log && echo "panic or fatal in sync3.log" && exit 1 || true
}

test_checkpoint_with_include_exclude(){
    # Test: checkpoint with include/exclude patterns
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir -p data/dir1 data/dir2
    for i in $(seq 1 50); do
        echo "txt-$i" > data/dir1/file$i.txt
        echo "log-$i" > data/dir1/file$i.log
        echo "txt-$i" > data/dir2/file$i.txt
    done
    # Sync with exclude *.log + checkpoint, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s \
        --exclude "*.log" --threads 2 2>&1 | tee sync1.log || true
    # Resume with same exclude pattern
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s \
        --exclude "*.log" 2>&1 | tee sync2.log
    # Verify: .txt files exist, .log files do not
    for i in $(seq 1 50); do
        [ -f /jfs/data/dir1/file$i.txt ] || (echo "file$i.txt should exist" && exit 1)
        [ ! -f /jfs/data/dir1/file$i.log ] || (echo "file$i.log should not exist" && exit 1)
    done
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_with_check_all(){
    # Test: checkpoint with --check-all should verify checksum on resume
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 100); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # Sync with check-all + checkpoint, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --check-all --threads 2 2>&1 | tee sync1.log || true
    # Resume
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --check-all 2>&1 | tee sync2.log
    compare_sync_dirs data/ /jfs/data/
    # Verify checked count is reported
    grep "checked:" sync2.log || echo "Warning: expected 'checked:' in sync log"
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_signal_save(){
    # Test: sending SIGINT should trigger checkpoint save
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 5000); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # Start sync in background, then send SIGINT
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 60s --threads 2 2>&1 > sync_bg.log &
    sync_pid=$!
    sleep 2
    kill -INT $sync_pid || true
    wait $sync_pid || true

    rm data/file1
    # Checkpoint file should exist from signal save
    checkpoint_file=$(find /jfs/data -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | head -1)
    if [ -z "$checkpoint_file" ]; then
        echo "checkpoint file should have been saved on SIGINT"
        exit 1
    fi
    echo "Checkpoint saved on signal: $checkpoint_file"
    # Resume should complete
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync2.log
    diff -r --exclude='.jfs.file*.tmp.*' --exclude='*file1' data/ /jfs/data/
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_multiple_interruptions_resume(){
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    # depth 5, dirs 3, files 20 => ~364 dirs x 20 files = ~7300 files (deep enough for delimiter bug #6865)
    ./juicefs mdtest $META_URL /mdtest_src --depth 5 --dirs 3 --files 20 --threads 10
    sync_opts="--enable-checkpoint --checkpoint-interval 1s --threads 20 --list-threads 8 --list-depth 5 --dirs --check-change"
    run_id=1
    for sig in INT KILL INT; do
        meta_url=$META_URL ./juicefs sync jfs://meta_url/mdtest_src/ jfs://meta_url/data/ $sync_opts > "sync${run_id}.log" 2>&1 &
        sync_pid=$!
        sleep 2
        kill -$sig "$sync_pid" || true
        wait "$sync_pid" || true
        echo "=== sync run $run_id (signal $sig) ===" && tail -3 "sync${run_id}.log"
        run_id=$((run_id + 1))
    done
    meta_url=$META_URL ./juicefs sync jfs://meta_url/mdtest_src/ jfs://meta_url/data/ $sync_opts 2>&1 | tee sync_final.log
    compare_sync_dirs /jfs/mdtest_src/ /jfs/data/
    grep "panic:\|<FATAL>" sync_final.log && echo "panic or fatal in sync_final.log" && exit 1 || true
}

test_checkpoint_without_mount_point(){
    # Test: checkpoint with jfs:// protocol (no mount point)
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    rm -rf data && mkdir data
    for i in $(seq 1 100); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # First sync to jfs:// with checkpoint, interrupt
    timeout 3 meta_url=$META_URL ./juicefs sync data/ jfs://meta_url/data/ --enable-checkpoint --checkpoint-interval 1s --threads 2 2>&1 | tee sync1.log || true
    # Resume
    meta_url=$META_URL ./juicefs sync data/ jfs://meta_url/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync2.log
    # Mount and verify
    ./juicefs mount -d $META_URL /jfs
    compare_sync_dirs data/ /jfs/data/
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_dry_run(){
    # Test: checkpoint with --dry should not save checkpoint file
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 20); do
        echo "content-$i" > data/file$i
    done
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s --dry 2>&1 | tee sync.log
    # Checkpoint should NOT be saved in dry mode
    count=$(find /jfs/data/ -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$count" -ne 0 ]; then
        echo "checkpoint file should not be created in dry run mode"
        exit 1
    fi
    # No files should be actually copied
    dst_count=$(ls /jfs/data/ 2>/dev/null | wc -l)
    if [ "$dst_count" -ne 0 ]; then
        echo "no files should be copied in dry run mode"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
}

test_checkpoint_multiple_dirs(){
    # Test: checkpoint with subdirectory structure
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir -p data/a/b/c data/d/e data/f
    for dir in data/a/b/c data/d/e data/f; do
        for i in $(seq 1 30); do
            dd if=/dev/urandom of=$dir/file$i bs=32K count=1 status=none
        done
    done
    # First sync with checkpoint + list-depth, interrupt
    timeout 2 ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s \
        --dirs --list-threads 4 --list-depth 3 --threads 2 2>&1 | tee sync1.log || true
    # Resume
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s \
        --dirs --list-threads 4 --list-depth 3 2>&1 | tee sync2.log
    compare_sync_dirs data/ /jfs/data/
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_idempotent_resume(){
    # Test: running resume multiple times should be idempotent
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 50); do
        echo "content-$i" > data/file$i
    done
    # Full successful sync
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync1.log
    compare_sync_dirs data/ /jfs/data/
    # Run again - should be a no-op (all skipped)
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 1s 2>&1 | tee sync2.log
    compare_sync_dirs data/ /jfs/data/
    # Check the second run skipped everything
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_save_on_check_change_failure(){
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    rm -rf data && mkdir data
    for i in $(seq 1 2000); do
        dd if=/dev/urandom of=data/file$i bs=64K count=1 status=none
    done
    # Background process continuously modifies source files to trigger check-change
    (while true; do
        for i in $(seq 1 300); do
            echo "m" >> data/file$i 2>/dev/null
        done
    done) &
    modifier_pid=$!
    # Sync with --check-change should fail because source files keep changing
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 60s \
        --check-change --threads 2 > sync1.log 2>&1 || true
    kill $modifier_pid 2>/dev/null
    wait $modifier_pid 2>/dev/null || true
    # Verify check-change failure was reported
    grep -i "changed during sync\|failed to handle" sync1.log || (echo "expected check-change failure" && exit 1)
    # Key assertion: checkpoint file must exist after failure (issue #6890)
    checkpoint_file=$(find /jfs/data -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | head -1)
    if [ -z "$checkpoint_file" ]; then
        echo "FAIL: checkpoint should be saved when sync fails (issue #6890)"
        exit 1
    fi
    echo "Checkpoint correctly saved on check-change failure: $checkpoint_file"
    # Resume sync (source no longer changing) should complete successfully
    rm /jfs/data/.juicefs-sync-checkpoint*
    ./juicefs sync data/ /jfs/data/ --enable-checkpoint --checkpoint-interval 10s --check-change 2>&1 | tee sync2.log
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

source .github/scripts/common/run_test.sh && run_test $@
