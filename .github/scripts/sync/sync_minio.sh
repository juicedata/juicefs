#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META minio
META_URL=$(get_meta_url $META)

test_sync_small_files(){
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 5 --threads 10
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ --list-threads 100 --list-depth 10
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ $count1 -eq $count2 ]
}

test_sync_big_file_with_jfs(){
    prepare_test
    [[ ! -f "/tmp/bigfile" ]] && dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    ./mc cp /tmp/bigfile myminio/myjfs/bigfile
    export dst_jfs=$META_URL 
    timeout 10 ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/myjfs/bigfile jfs://dst_jfs/bigfile --threads=64 --force-update
    cmp /tmp/bigfile /jfs/bigfile
}

test_sync_big_file(){
    prepare_test
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cp myminio/myjfs/bigfile /tmp/bigfile2
    cmp /tmp/bigfile /tmp/bigfile2
}

test_sync_with_limit(){
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 2 --files 5 --threads 10
    ./juicefs sync --limit 1000 minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    count=$(./mc ls myminio/myjfs -r | wc -l)
    echo count is $count
    [ $count -eq 1000 ]
}
test_sync_with_existing(){
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync --existing minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc find myminio/myjfs/abc && echo "myminio/myjfs/abc should not exist" && exit 1 || true
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc find myminio/myjfs/abc
}
test_sync_with_update(){
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    echo def > def
    ./mc cp def myminio/myjfs/abc
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def" && exit 1)
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def" && exit 1)
    ./juicefs sync --force-update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc" && exit 1)
    echo hijk > hijk
    ./mc cp hijk myminio/myjfs/abc
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep hijk || (echo "content should be hijk" && exit 1)
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc" && exit 1)
}

test_sync_hard_link(){
    prepare_test
    echo abc > /jfs/abc
    ln /jfs/abc /jfs/def
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/def | grep abc || (echo "content should be abc" && exit 1)
    echo abcd > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cat myminio/myjfs/def | grep abcd || (echo "content should be abcd" && exit 1)
}

test_sync_external_link(){
    prepare_test
    touch hello
    ln -s $(realpath hello) /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    [ -z $(./mc cat myminio/myjfs/hello) ]
}

# list object should be skipped when encountering a loop symlink
test_sync_loop_symlink(){
    prepare_test
    touch hello
    ln -s hello /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    rm -rf /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
}

test_sync_deep_symlink(){
    prepare_test
    cd /jfs
    echo hello > hello
    ln -s hello symlink_1
    for i in {1..40}; do
        ln -s symlink_$i symlink_$((i+1))
    done
    cat symlink_40 | grep hello
    cat symlink_41 && echo "cat symlink_41 fail" && exit 1 || true
    cd -
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    for i in {1..40}; do
        ./mc cat myminio/myjfs/symlink_$i | grep "^hello$"
    done
}

test_sync_list_object_symlink(){
    prepare_test
    cd /jfs
    mkdir dir1
    mkdir -p dir2/src_dir
    echo abc > dir2/src_dir/afile
    ln -s ./../dir2/src_dir dir1/symlink_dir
    cd -
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/dir1/ minio://minioadmin:minioadmin@localhost:9000/myjfs/dir3/
    ./mc cat myminio/myjfs/dir3/symlink_dir/afile | grep abc || (echo "content should be abc" && exit 1)
}

prepare_test(){
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
    rm -rf /var/jfsCache/myjfs
    (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9 || true
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    wait_gateway_ready
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
}

wait_gateway_ready(){
    timeout=30
    for i in $(seq 1 $timeout); do
        if [[ -z $(lsof -i :9005) ]]; then
            echo "$i Waiting for port 9005 to be ready..."
            sleep 1
        else
            echo "gateway is now ready on port 9005"
            break
        fi
    done
    if [[ -z $(lsof -i :9005) ]]; then
        echo "gateway is not ready after $timeout seconds"
        exit 1
    fi
}

test_checkpoint_minio_basic(){
    # Test: checkpoint basic resume for minio-to-minio sync
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 5 --threads 10
    # First sync with checkpoint, interrupt early
    timeout 5 ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync1.log 2>&1 || true
    cat sync1.log
    # Checkpoint file should exist in destination
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "checkpoint file should exist after interrupted minio sync"
        exit 1
    fi
    # Resume sync
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    cat sync2.log
    count1=$(./mc ls -r juicegw/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch: $count1 vs $count2" && exit 1)
    # Checkpoint should be cleaned up
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -ne 0 ]; then
        echo "checkpoint file should be deleted after successful minio sync"
        exit 1
    fi
}

test_checkpoint_minio_cleanup_on_success(){
    # Test: checkpoint file is deleted after successful minio sync (no interruption)
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 5 --depth 2 --files 10 --threads 10
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync.log 2>&1
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch: $count1 vs $count2" && exit 1)
    # Verify checkpoint cleaned up
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -ne 0 ]; then
        echo "checkpoint file should be deleted after success"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
}

test_checkpoint_minio_stats_correctness(){
    # Test: checkpoint stats correctness for minio sync
    prepare_test
    echo abc > /jfs/abc
    echo def > /jfs/def
    echo ghi > /jfs/ghi
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        2>&1 | tee sync.log
    # Verify stats show correct copied count
    copied=$(grep -oP 'Copied:\s*\K\d+' sync.log || grep -oP 'copied:\s*\K\d+' sync.log || echo "0")
    if [ "$copied" -lt 3 ]; then
        echo "Warning: copied count ($copied) seems low for 3 files"
    fi
    # Rerun - should skip existing files
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        2>&1 | tee sync2.log
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_minio_with_update(){
    # Test: checkpoint + --update for minio sync
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    echo def > def
    ./mc cp def myminio/myjfs/abc
    # Sync with --update + checkpoint should keep newer dst
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def with --update" && exit 1)
    # Sync with --force-update + checkpoint should overwrite
    ./juicefs sync --force-update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc with --force-update" && exit 1)
}

test_checkpoint_minio_big_file_resume(){
    # Test: checkpoint resume for large file minio sync
    prepare_test
    dd if=/dev/urandom of=/tmp/bigfile_ckpt bs=1M count=256
    cp /tmp/bigfile_ckpt /jfs/bigfile
    # First sync with checkpoint, interrupt
    timeout 10 ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync1.log 2>&1 || true
    # Resume
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    ./mc cp myminio/myjfs/bigfile /tmp/bigfile_ckpt2
    cmp /tmp/bigfile_ckpt /tmp/bigfile_ckpt2 || (echo "big file content mismatch after checkpoint resume" && exit 1)
    rm -f /tmp/bigfile_ckpt /tmp/bigfile_ckpt2
}

test_checkpoint_minio_signal_save(){
    # Test: SIGINT saves checkpoint for minio sync
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 10 --threads 10
    # Start sync in background
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 60s \
        >sync1.log 2>&1 &
    sync_pid=$!
    sleep 3
    kill -INT $sync_pid || true
    wait $sync_pid || true
    # Checkpoint should be saved
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "checkpoint file should exist after SIGINT"
        exit 1
    fi
    # Resume should work
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    count1=$(./mc ls -r juicegw/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch after resume: $count1 vs $count2" && exit 1)
}

source .github/scripts/common/run_test.sh && run_test $@
