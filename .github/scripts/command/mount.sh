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

test_redis_client_cache()
{
    if [[ "$META" != "redis" ]]; then
        echo "Skip redis client cache test for META=$META"
        return 0
    fi

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs2 || true
    ./juicefs mount -d $META_URL /jfs2

    mkdir -p /jfs/redis_csc
    for i in {1..100}; do
        echo "v$i" > "/jfs/redis_csc/file_$i"
    done

    wait_command_success "ls /jfs2/redis_csc | wc -l" "100" 30
    echo "cache-sync" > /jfs/redis_csc/shared_file
    wait_command_success "cat /jfs2/redis_csc/shared_file" "cache-sync" 30

    ./juicefs umount /jfs2 || umount -l /jfs2 || true
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

test_close_to_open1()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs2 || true
    ./juicefs mount -d $META_URL /jfs2
    file1="/jfs/testfile.tmp"
    file2="/jfs2/testfile.tmp"
    rm $file1 || true
    openssl rand -base64 -out $file1 512000
    sleep 3
    ls -ls $file2
    echo "#########################"
    echo "hello" > $file1
    hex_file2=$(cat $file2 | hexdump -C)
    echo "#########################"
    hex_file2_2=$(cat $file2 | hexdump -C)
    hex_file1=$(cat $file1 | hexdump -C)
    [[ "$hex_file2" != "$hex_file1" ]] && echo "Content of $hex_file2 and $hex_file1 do not match" && exit 1 || true
    [[ "$hex_file2_2" != "$hex_file1" ]] && echo "Content of $hex_file2_2 and $hex_file1 do not match" && exit 1 || true
}

test_colse_to_open2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs2 || true
    ./juicefs mount -d $META_URL /jfs2
    file1="/jfs/testfile.tmp"
    file2="/jfs2/testfile.tmp"
    rm $file1 || true
    python3 -c "
for i in range(1, 101):
    with open('$file1', 'a') as f:
        f.write(f'{i}\\n')
    with open('$file2', 'a') as f:
        f.write(f'{i}\\n')
"
    line_count1=$(cat $file1 | wc -l)
    line_count2=$(cat $file2 | wc -l)
    [[ $line_count1 -ne 200 ]] && cat $file1 && echo "Error: $file1 should have 200 lines but has $line_count1" && exit 1 || true
    [[ $line_count2 -ne 200 ]] && cat $file2 && echo "Error: $file2 should have 200 lines but has $line_count2" && exit 1 || true
}

_skip_if_not_redis() {
    if [[ "$META" != "redis" ]]; then
        echo "Skip: this test requires META=redis (current: $META)"
        return 1
    fi
    return 0
}

_umount_secondary() {
    for mp in "$@"; do
        ./juicefs umount "$mp" 2>/dev/null || umount -l "$mp" 2>/dev/null || true
    done
}

test_redis_attr_consistency_chmod() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2

    local dir="/jfs/chmod_test"
    mkdir -p "$dir"

    touch "$dir/file1"
    chmod 644 "$dir/file1"
    wait_command_success "stat -c %a /jfs2/chmod_test/file1" "644" 30

    chmod 755 "$dir/file1"
    wait_command_success "stat -c %a /jfs2/chmod_test/file1" "755" 30

    chmod 400 "$dir/file1"
    wait_command_success "stat -c %a /jfs2/chmod_test/file1" "400" 30

    chmod 700 "$dir"
    wait_command_success "stat -c %a /jfs2/chmod_test" "700" 30

    chmod 755 "$dir"
    wait_command_success "stat -c %a /jfs2/chmod_test" "755" 30

    local perms=(600 640 644 750 755)
    for i in "${!perms[@]}"; do
        touch "$dir/batch_$i"
        chmod "${perms[$i]}" "$dir/batch_$i"
    done
    for i in "${!perms[@]}"; do
        wait_command_success "stat -c %a /jfs2/chmod_test/batch_$i" "${perms[$i]}" 30
    done

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_chmod"
}

test_redis_attr_consistency_chown() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2

    local dir="/jfs/chown_test"
    mkdir -p "$dir"
    touch "$dir/file1"
    touch "$dir/file2"

    local target_uid target_gid
    target_uid=$(id -u nobody 2>/dev/null || echo "65534")
    target_gid=$(id -g nobody 2>/dev/null || echo "65534")

    chown "${target_uid}:${target_gid}" "$dir/file1"
    wait_command_success "stat -c %u /jfs2/chown_test/file1" "$target_uid" 30
    wait_command_success "stat -c %g /jfs2/chown_test/file1" "$target_gid" 30

    chown "${target_uid}:${target_gid}" "$dir"
    wait_command_success "stat -c %u /jfs2/chown_test" "$target_uid" 30
    wait_command_success "stat -c %g /jfs2/chown_test" "$target_gid" 30

    chown "0:0" "$dir/file1"
    wait_command_success "stat -c %u /jfs2/chown_test/file1" "0" 30
    wait_command_success "stat -c %g /jfs2/chown_test/file1" "0" 30

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_chown"
}

test_redis_attr_consistency_mtime() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2

    local dir="/jfs/mtime_test"
    mkdir -p "$dir"
    touch "$dir/file1"

    touch -m -t 202001010000.00 "$dir/file1"
    local expected_mtime
    expected_mtime=$(stat -c %Y "$dir/file1")
    wait_command_success "stat -c %Y /jfs2/mtime_test/file1" "$expected_mtime" 30

    touch -m -t 202312311200.00 "$dir/file1"
    expected_mtime=$(stat -c %Y "$dir/file1")
    wait_command_success "stat -c %Y /jfs2/mtime_test/file1" "$expected_mtime" 30

    echo "update content" > "$dir/file1"
    expected_mtime=$(stat -c %Y "$dir/file1")
    wait_command_success "stat -c %Y /jfs2/mtime_test/file1" "$expected_mtime" 30

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_mtime"
}

test_redis_attr_consistency_xattr() {
    _skip_if_not_redis || return 0

    if ! command -v setfattr &>/dev/null || ! command -v getfattr &>/dev/null; then
        echo "Skip: setfattr/getfattr not available"
        return 0
    fi

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-xattr
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2 --enable-xattr

    local dir="/jfs/xattr_test"
    mkdir -p "$dir"
    touch "$dir/file1"

    setfattr -n user.author -v "juicefs" "$dir/file1"
    wait_command_success "getfattr -n user.author --only-values /jfs2/xattr_test/file1" "juicefs" 30

    setfattr -n user.author -v "juicefs-v2" "$dir/file1"
    wait_command_success "getfattr -n user.author --only-values /jfs2/xattr_test/file1" "juicefs-v2" 30

    setfattr -n user.version -v "1.0" "$dir/file1"
    setfattr -n user.env -v "production" "$dir/file1"
    wait_command_success "getfattr -n user.version --only-values /jfs2/xattr_test/file1" "1.0" 30
    wait_command_success "getfattr -n user.env --only-values /jfs2/xattr_test/file1" "production" 30

    setfattr -x user.env "$dir/file1"
    wait_command_success \
        "getfattr -n user.env /jfs2/xattr_test/file1 2>&1 | grep -c 'No such attribute' || echo 1" \
        "1" 30

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_xattr"
}

test_redis_attr_consistency_truncate() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2

    local dir="/jfs/truncate_test"
    mkdir -p "$dir"

    dd if=/dev/urandom of="$dir/file1" bs=1024 count=64 2>/dev/null
    local size1
    size1=$(stat -c %s "$dir/file1")
    wait_command_success "stat -c %s /jfs2/truncate_test/file1" "$size1" 30

    truncate -s 1024 "$dir/file1"
    wait_command_success "stat -c %s /jfs2/truncate_test/file1" "1024" 30

    truncate -s 102400 "$dir/file1"
    wait_command_success "stat -c %s /jfs2/truncate_test/file1" "102400" 30

    truncate -s 0 "$dir/file1"
    wait_command_success "stat -c %s /jfs2/truncate_test/file1" "0" 30

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_truncate"
}

test_redis_attr_consistency_multipoint() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2
    mkdir -p /jfs3
    ./juicefs mount -d $META_URL /jfs3

    local dir="/jfs/multi_test"
    mkdir -p "$dir"
    touch "$dir/shared"

    chmod 640 "$dir/shared"
    wait_command_success "stat -c %a /jfs2/multi_test/shared" "640" 30
    wait_command_success "stat -c %a /jfs3/multi_test/shared" "640" 30

    chmod 700 /jfs2/multi_test/shared
    wait_command_success "stat -c %a /jfs/multi_test/shared" "700" 30
    wait_command_success "stat -c %a /jfs3/multi_test/shared" "700" 30

    chmod 755 /jfs3/multi_test/shared
    wait_command_success "stat -c %a /jfs/multi_test/shared" "755" 30
    wait_command_success "stat -c %a /jfs2/multi_test/shared" "755" 30

    local final_perm="600"
    for perm in 700 710 720 750 640 "$final_perm"; do
        chmod "$perm" "$dir/shared"
    done
    wait_command_success "stat -c %a /jfs2/multi_test/shared" "$final_perm" 30
    wait_command_success "stat -c %a /jfs3/multi_test/shared" "$final_perm" 30

    touch "$dir/newfile"
    chmod 444 "$dir/newfile"
    wait_command_success "stat -c %a /jfs2/multi_test/newfile" "444" 30
    wait_command_success "stat -c %a /jfs3/multi_test/newfile" "444" 30

    rm "$dir/newfile"
    wait_command_success "ls /jfs2/multi_test/newfile 2>&1 | grep -c 'No such file' || echo 1" "1" 30
    wait_command_success "ls /jfs3/multi_test/newfile 2>&1 | grep -c 'No such file' || echo 1" "1" 30

    mkdir -p "$dir/subdir"
    chmod 750 "$dir/subdir"
    wait_command_success "stat -c %a /jfs2/multi_test/subdir" "750" 30
    wait_command_success "stat -c %a /jfs3/multi_test/subdir" "750" 30

    chmod 755 /jfs2/multi_test/subdir
    wait_command_success "stat -c %a /jfs/multi_test/subdir" "755" 30
    wait_command_success "stat -c %a /jfs3/multi_test/subdir" "755" 30

    _umount_secondary /jfs2 /jfs3
    echo "PASS: test_redis_attr_consistency_multipoint"
}

test_redis_attr_consistency_heavy() {
    _skip_if_not_redis || return 0

    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --attr-cache 0
    mkdir -p /jfs2
    ./juicefs mount -d $META_URL /jfs2 --attr-cache 0

    local dir1="/jfs/heavy_test"
    local dir2="/jfs2/heavy_test"
    local n=50
    local rounds=10
    local perms=(600 640 644 700 750 755 400 444 664 770)
    local timestamps=(202001010000.00 202006150830.00 202101010000.00 202201010000.00 202301010000.00
                      202312311200.00 202401010000.00 202406151530.00 202501010000.00 202512311200.00)

    local target_uid target_gid
    target_uid=$(id -u nobody 2>/dev/null || echo "65534")
    target_gid=$(id -g nobody 2>/dev/null || echo "65534")

    mkdir -p "$dir1"

    for i in $(seq 1 "$n"); do
        touch "$dir1/file_$i"
    done

    for round in $(seq 1 "$rounds"); do
        local perm="${perms[$(( (round - 1) % ${#perms[@]} ))]}"
        local ts="${timestamps[$(( (round - 1) % ${#timestamps[@]} ))]}"

        if (( round % 2 == 1 )); then
            for i in $(seq 1 "$n"); do
                chmod "$perm" "$dir1/file_$i"
                chown "0:0" "$dir1/file_$i"
                truncate -s "$(( round * 1024 ))" "$dir1/file_$i"
                touch -m -t "$ts" "$dir1/file_$i"
            done
        else
            for i in $(seq 1 "$n"); do
                chmod "$perm" "$dir2/file_$i"
                chown "${target_uid}:${target_gid}" "$dir2/file_$i"
                truncate -s "$(( round * 1024 ))" "$dir2/file_$i"
                touch -m -t "$ts" "$dir2/file_$i"
            done
        fi
    done

    local last_round="$rounds"
    local expected_perm="${perms[$(( (last_round - 1) % ${#perms[@]} ))]}"
    local expected_ts="${timestamps[$(( (last_round - 1) % ${#timestamps[@]} ))]}"
    local expected_size="$(( last_round * 1024 ))"

    if (( last_round % 2 == 1 )); then
        local expected_uid="0"
        local expected_gid="0"
        local check_dir="$dir2"
        local write_dir="$dir1"
    else
        local expected_uid="$target_uid"
        local expected_gid="$target_gid"
        local check_dir="$dir1"
        local write_dir="$dir2"
    fi

    local touch_epoch
    touch_epoch=$(stat -c %Y "${write_dir}/file_1")

    local failed=0
    for i in $(seq 1 "$n"); do
        local f="${check_dir}/file_$i"
        local actual_perm actual_uid actual_gid actual_mtime actual_size
        actual_perm=$(stat -c %a "$f")
        actual_uid=$(stat -c %u "$f")
        actual_gid=$(stat -c %g "$f")
        actual_mtime=$(stat -c %Y "$f")
        actual_size=$(stat -c %s "$f")

        if [[ "$actual_perm" != "$expected_perm" ]]; then
            echo "FAIL: file_$i perm: got=$actual_perm expected=$expected_perm"
            failed=1
        fi
        if [[ "$actual_uid" != "$expected_uid" ]]; then
            echo "FAIL: file_$i uid: got=$actual_uid expected=$expected_uid"
            failed=1
        fi
        if [[ "$actual_gid" != "$expected_gid" ]]; then
            echo "FAIL: file_$i gid: got=$actual_gid expected=$expected_gid"
            failed=1
        fi
        if [[ -n "$touch_epoch" && "$actual_mtime" != "$touch_epoch" ]]; then
            echo "FAIL: file_$i mtime: got=$actual_mtime expected=$touch_epoch"
            failed=1
        fi
        if [[ "$actual_size" != "$expected_size" ]]; then
            echo "FAIL: file_$i size: got=$actual_size expected=$expected_size"
            failed=1
        fi
    done

    [[ "$failed" -eq 1 ]] && exit 1

    for i in $(seq 1 "$n"); do
        local final_perm="755"
        chmod "$final_perm" "${write_dir}/file_$i"
    done
    for i in $(seq 1 "$n"); do
        wait_command_success "stat -c %a ${check_dir}/file_$i" "755" 30
    done

    _umount_secondary /jfs2
    echo "PASS: test_redis_attr_consistency_heavy"
}

source .github/scripts/common/run_test.sh && run_test $@

