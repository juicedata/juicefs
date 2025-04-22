#!/bin/bash -e

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_sort_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --sort-dir
    
    for i in {1..1000}; do
        touch "/jfs/file_$i"
    done
        mkdir -p /jfs/subdir
    for i in {1..1000}; do
        touch "/jfs/subdir/file_$i"
    done    
    ls -lh /jfs > /tmp/sorted_no_u
    ls -U -lh /jfs > /tmp/sorted_with_u
    diff /tmp/sorted_no_u /tmp/sorted_with_u
    
    ls -lh /jfs/subdir > /tmp/subdir_sorted_no_u
    ls -U -lh /jfs/subdir > /tmp/subdir_sorted_with_u
    diff /tmp/subdir_sorted_no_u /tmp/subdir_sorted_with_u    
    rm -f /tmp/sorted_*
    rm -f /tmp/subdir_sorted_*
}

measure_lookup_time() {
    local start_time end_time elapsed
    start_time=$(date +%s.%N)
    for file in "${FILE_LIST[@]}"; do
        if [[ -e "$file" ]]; then
            echo "Error: $file exists!" >&2
            exit 1
        fi
    done
    end_time=$(date +%s.%N)
    elapsed=$(echo "$end_time - $start_time" | bc)
    echo "$elapsed"
}

test_negative_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --negative-entry-cache 5
    TEST_DIR="/jfs/test_dir_$$"
    mkdir -p "${TEST_DIR}"

    FILE_LIST=()
    for i in {1..1000}; do
      FILE_LIST+=("${TEST_DIR}/nonexistent_file_$(printf "%04d" $i)")
    done
    echo -e "\n=== First lookup (uncached) ==="
    time1=$(measure_lookup_time)
    echo "Time taken: ${time1} seconds"
    echo -e "\n=== Second lookup (cached) ==="
    time2=$(measure_lookup_time)
    echo "Time taken: ${time2} seconds"
    echo -e "\n=== Waiting for cache to expire... ==="
    sleep 6 
    echo -e "\n=== Third lookup (after cache expiry) ==="
    time3=$(measure_lookup_time)
    echo "Time taken: ${time3} seconds"
    echo -e "\n=== Test Result ==="
    if (( $(echo "$time1 > 2 * $time2" | bc -l) )) && \
       (( $(echo "$time3 > 2 * $time2" | bc -l) )) && \
       (( $(echo "$time1 - $time3 < 0.5" | bc -l) )); then
        echo "PASS: Caching behavior matches expectations:"
    else
        echo "FAIL: Caching behavior does NOT match expectations:"
        echo "Expected: First ≈ Third > 2 x Second"
        exit 1
    fi
    rm -rf "${TEST_DIR}"
    echo -e "\nTest directory removed: ${TEST_DIR}"
}

test_check_storage(){
    start_meta_engine $META minio
    prepare_test
    sleep 2
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test \
        --access-key minioadmin --secret-key minioadmin --compress lz4 --hash-prefix
    docker stop minio
    ./juicefs mount $META_URL /tmp/jfs --check-storage || echo "PASS: Mount failed as expected when storage is not accessible"
    docker start minio
    sleep 2
    ./juicefs mount $META_URL /tmp/jfs -d
    ./juicefs umount /tmp/jfs
    docker stop minio && docker rm minio
}

test_capabilities()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-xattr --enable-cap
    cp /bin/ls /jfs/test_ls
    cp /bin/ping /jfs/test_ping
    chmod +x /jfs/test_ls /jfs/test_ping
    setcap "cap_net_raw+ep" /jfs/test_ping
    setcap "cap_dac_override+ep" /jfs/test_ls
    sleep 1
    getcap /jfs/test_ping | grep -E "cap_net_raw[+=]ep" || {
        echo "FAIL: capability not set correctly on test_ping"
        exit 1
    }
    getcap /jfs/test_ls | grep -E "cap_dac_override[+=]ep" || {
        echo "FAIL: capability not set correctly on test_ls"
        exit 1
    }
    capsh --print | grep "Current:" || {
        echo "FAIL: cannot get current capabilities"
        exit 1
    }
    setcap -r /jfs/test_ping
    setcap -r /jfs/test_ls
    getcap /jfs/test_ping | grep -E "cap_net_raw[+=]ep" && {
        echo "FAIL: capability not removed from test_ping"
        exit 1
    }
    getcap /jfs/test_ls | grep -E "cap_dac_override[+=]ep" && {
        echo "FAIL: capability not removed from test_ls"
        exit 1
    }
    rm -f /jfs/test_ls /jfs/test_ping
    echo "PASS: Capabilities test completed successfully"
}

test_all_squash()
{
    prepare_test
   ./juicefs format $META_URL myjfs
   ./juicefs mount -d $META_URL /jfs --all-squash 1101:1101
    mkdir -p /jfs/test_dir
    touch /jfs/test_dir/test_file
    uid1=$(stat -c %u /jfs/test_dir)
    gid1=$(stat -c %g /jfs/test_dir)
    uid2=$(stat -c %u /jfs/test_dir/test_file)
    gid2=$(stat -c %g /jfs/test_dir/test_file)
    if [[ "$uid1" != "1101" ]] || [[ "$gid1" != "1101" ]] || [[ "$uid2" != "1101" ]] || [[ "$gid2" != "1101" ]]; then
        echo >&2 "<FATAL>: uid/gid does not same as squash: uid1: $uid1, uid2: $uid2, gid1: $gid1, gid2: $gid2"
        exit 1
    fi
}

test_umask()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --umask 0027

    mkdir -p /jfs/test_dir
    dir_perms=$(stat -c %a /jfs/test_dir)
    if [[ "$dir_perms" != "750" ]]; then
        echo >&2 "<FATAL>: Directory permissions incorrect. Expected: 750, Got: $dir_perms"
        exit 1
    fi
    touch /jfs/test_file
    file_perms=$(stat -c %a /jfs/test_file)
    if [[ "$file_perms" != "640" ]]; then
        echo >&2 "<FATAL>: File permissions incorrect. Expected: 640, Got: $file_perms"
        exit 1
    fi
    touch /jfs/test_dir/nested_file
    nested_perms=$(stat -c %a /jfs/test_dir/nested_file)
    if [[ "$nested_perms" != "640" ]]; then
        echo >&2 "<FATAL>: Nested file permissions incorrect. Expected: 640, Got: $nested_perms"
        exit 1
    fi
    echo "PASS: Umask test completed successfully"
}

source .github/scripts/common/run_test.sh && run_test $@
