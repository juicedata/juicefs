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
    ./juicefs mount -d $META_URL /jfs --negative-dir-entry-cache 5
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

source .github/scripts/common/run_test.sh && run_test $@
