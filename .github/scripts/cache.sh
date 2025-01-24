#!/bin/bash -e
dpkg -s redis-server || .github/scripts/apt_install.sh  redis-tools redis-server
dpkg -s fio || .github/scripts/apt_install.sh fio
source .github/scripts/common/common.sh
source .github/scripts/start_meta_engine.sh
[[ -z "$META" ]] && META=sqlite3
start_meta_engine $META minio
META_URL=$(get_meta_url $META)

test_warmup_in_background(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=1024
    ./juicefs warmup /tmp/jfs/test --evict
    ./juicefs warmup /tmp/jfs/test --background
    wait_warmup_finish /tmp/jfs/test 100
    ./juicefs warmup /tmp/jfs/test --background --evict 
    wait_warmup_finish /tmp/jfs/test 0
}

test_batch_warmup(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d
    rm -f file.list
    file_count=11000
    time seq 1 $file_count | xargs -P 8 -I {} sh -c 'echo {} > /tmp/jfs/test_{}; echo /tmp/jfs/test_{} >> file.list'
    # time for i in $(seq 1 $file_count); do echo $i > /tmp/jfs/test_$i; echo /tmp/jfs/test_$i >> file.list; done
    ./juicefs warmup -f file.list 2>&1 | tee warmup.log
    files=$(get_cache_file_count)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    ./juicefs warmup -f file.list --check 2>&1 | tee warmup.log
    files=$(get_cache_file_count)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    grep "(100.0%)" warmup.log || (echo "warmup failed, expect 100.0% warmup" && exit 1)
    ./juicefs warmup -f file.list --evict 2>&1 | tee warmup.log 
    files=$(get_cache_file_count)
    [[ $files -ne $file_count ]] && echo "warmup evict failed, expect $file_count files, actual $files" && exit 1 || true
    ./juicefs warmup -f file.list --check 2>&1 | tee warmup.log
    files=$(get_cache_file_count)
    [[ $files -ne $file_count ]] && echo "warmup evict failed, expect $file_count files, actual $files" && exit 1 || true
    grep "(0.0%)" warmup.log || (echo "warmup failed, expect 0.0% warmup" && exit 1)

    ./juicefs warmup /tmp/jfs/test* 2>&1 | tee warmup.log
    files=$(get_cache_file_count)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
}

test_kernel_writeback_cache(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d -o writeback_cache
    mkdir /tmp/jfs/fio
    runtime=15
    cat /tmp/jfs/.stats | grep fuse | grep 'juicefs_fuse_written_size_bytes_sum\|juicefs_fuse_ops_total_write'
    fio --name=seq_write_test --rw=write --bs=10 --size=4M --numjobs=8 --nrfiles=1 --runtime=$runtime --time_based --group_reporting --directory=/tmp/jfs/fio | tee fio.log
    cat /tmp/jfs/.stats | grep fuse | grep 'juicefs_fuse_written_size_bytes_sum\|juicefs_fuse_ops_total_write'
    bytes=$(cat /tmp/jfs/.stats | grep juicefs_fuse_written_size_bytes_sum | awk '{print $2}')
    ops=$(cat /tmp/jfs/.stats | grep juicefs_fuse_ops_total_write | awk '{print $2}')
    [[ $((bytes/ops)) -lt 10240 ]] && echo "writeback_cache may not enabled" && exit 1 || true
}

test_cache_items(){
    prepare_test
    ./juicefs format $META_URL myjfs
    cache_items=500
    ./juicefs mount $META_URL /tmp/jfs -d --cache-items $cache_items
    seq 1 $((cache_items*2)) | xargs -P 8 -I {} sh -c 'echo {} > /tmp/jfs/test_{};'
    ./juicefs warmup /tmp/jfs/
    ./juicefs warmup /tmp/jfs/ --check 2>&1 | tee warmup.log
    ratio=$(get_warmup_ratio)
    [[ $ratio -lt 55 ]] || (echo "ratio should less than 55%" && exit 1)
}

test_evict_on_writeback(){
    prepare_test
    ./juicefs format $META_URL myjfs --compress zstd
    ./juicefs mount $META_URL /tmp/jfs -d --writeback --upload-delay 3s
    dd if=/dev/urandom of=/tmp/test bs=1M count=200
    cp /tmp/test /tmp/jfs/test
    sleep 3
    stageBlocks=$(grep "juicefs_staging_blocks" /tmp/jfs/.stats | awk '{print $2}')
    [[ $stageBlocks -eq 0 ]] && echo "stage blocks should not be 0" && exit 1 || true
    ./juicefs warmup /tmp/jfs/test --evict
    wait_stage_uploaded
    compare_md5sum /tmp/test /tmp/jfs/test
}

test_remount_on_writeback(){
    prepare_test
    ./juicefs format $META_URL myjfs --compress lz4
    ./juicefs mount $META_URL /tmp/jfs -d --writeback --upload-delay 3s
    dd if=/dev/urandom of=/tmp/test bs=1M count=200
    cp /tmp/test /tmp/jfs/test
    umount_jfs /tmp/jfs $META_URL
    ./juicefs mount $META_URL /tmp/jfs -d --writeback
    sleep 3
    stage_size=$(du -shm $(get_rawstaging_dir) | awk '{print $1}')
    [[ $stage_size -gt 2 ]] && echo "stage size should not great than 2M" && exit 1 || true
    ./juicefs warmup /tmp/jfs/test --evict
    compare_md5sum /tmp/test /tmp/jfs/test
}
test_memory_cache_none(){
    do_test_memory_cache none
}

test_memory_cache_2_random(){
    do_test_memory_cache 2-random
}

do_test_memory_cache(){
    cache_eviction=$1
    prepare_test
    ./juicefs format $META_URL myjfs --compress lz4
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir memory --cache-size 100M --cache-eviction $cache_eviction
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=200
    ./juicefs warmup /tmp/jfs/test
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    ratio=$(get_warmup_ratio)
    if [[ $cache_eviction == "2-random" ]]; then
        [[ "$ratio" -gt 40 && "$ratio" -lt 60   ]] || (echo "ratio($ratio) should between 40% and 60%" && exit 1)
    elif [[ $cache_eviction == "none" ]]; then
        [[ "$ratio" -gt 40 && "$ratio" -lt 60   ]] || (echo "ratio($ratio) should between 40% and 60%" && exit 1)
    fi
    ./juicefs warmup /tmp/jfs/test --evict
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    ratio=$(get_warmup_ratio)
    [[ "$ratio" = 0 ]] || (echo "ratio($ratio) should less than 0" && exit 1)
}

test_cache_expired(){
    do_test_cache_expired /var/jfsCache/myjfs
}

test_cache_expired_memory(){
    do_test_cache_expired memory
}

do_test_cache_expired(){
    cache_dir=$1
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir $cache_dir --cache-expire 3s
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=200
    for i in $(seq 1 1100); do
        dd if=/dev/zero of=/tmp/jfs/test$i bs=32k count=1 status=none
    done
    ./juicefs warmup /tmp/jfs/ 2>&1 | tee warmup.log
    sleep 15
    ./juicefs warmup /tmp/jfs/ --check 2>&1 | tee warmup.log
    grep "(0.0%)" warmup.log || (echo "cache should expired" && exit 1)
}

test_cache_large_write(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /tmp/jfs -d -v
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=200
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    ratio=$(get_warmup_ratio)
    [[ "$ratio" = 0 ]] || (echo "ratio($ratio) should less than 0" && exit 1)
    ./juicefs mount $META_URL /tmp/jfs -d --cache-large-write 
    dd if=/dev/zero of=/tmp/jfs/test1 bs=1M count=200
    ./juicefs warmup /tmp/jfs/test1 --check 2>&1 | tee warmup.log
    # TODO: should check the ratio
    check_warmup_log 90
}

test_cache_mode(){
    prepare_test
    ./juicefs format $META_URL myjfs
    cache_mode=$(printf "%03o" $((RANDOM % 512)))
    echo "cache mode is $cache_mode"
    ./juicefs mount $META_URL /tmp/jfs -d --cache-mode $cache_mode --writeback --upload-delay 3s
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=32
    ./juicefs warmup /tmp/jfs/test
    find $(get_raw_dir) -type f ! -perm $cache_mode -exec echo "perm of {} is incorrect" \; -exec false {} +
    find $(get_rawstaging_dir) -type f ! -perm $cache_mode -exec echo "perm of {} is incorrect" \; -exec false {} +
    sleep 5s 
    find $(get_raw_dir) -type f ! -perm $cache_mode -exec echo "perm of {} is incorrect" \; -exec false {} +
    find $(get_rawstaging_dir) -type f ! -perm $cache_mode -exec echo "perm of {} is incorrect" \; -exec false {} +
}

test_cache_compressed(){
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test \
        --access-key minioadmin --secret-key minioadmin --compress lz4 --hash-prefix
    ./juicefs mount $META_URL /tmp/jfs -d 
    dd if=/dev/urandom of=/tmp/test bs=1M count=200
    cp /tmp/test /tmp/jfs/test
    ./juicefs warmup /tmp/jfs/test --evict
    ./juicefs warmup /tmp/jfs/test
    docker stop minio
    compare_md5sum /tmp/test /tmp/jfs/test
    docker start minio
}

test_cache_checksum_none(){
    do_test_cache_checksum none
}

test_cache_checksum_full(){
    do_test_cache_checksum full
}

test_cache_checksum_shrink(){
    do_test_cache_checksum shrink
}

test_cache_checksum_extend(){
    do_test_cache_checksum extend
}

do_test_cache_checksum(){
    checksum_level=$1
    prepare_test
    ./juicefs format $META_URL myjfs --compress lz4
    ./juicefs mount $META_URL /tmp/jfs -d --verify-cache-checksum $checksum_level
    mkdir -p /tmp/jfs/rand-rw
    fio --name=seq_rw --rw=readwrite --bsrange=1k-4k --size=80M --numjobs=4 --runtime=5 --time_based --group_reporting --filename=/tmp/jfs/req-rw
    fio --name=rand_rw   --rw=randrw --bsrange=1k-4k --size=80M --numjobs=4 --runtime=5 --time_based --group_reporting --directory=/tmp/jfs/rand-rw --nrfiles=1000 --filesize=4k
}

test_disk_full_2_random(){
    do_test_disk_full 2-random
}

test_disk_full_none(){
    do_test_disk_full none
}

do_test_disk_full(){
    cache_eviction=$1
    prepare_test
    mount_jfsCache1 1G
    ./juicefs format $META_URL myjfs 
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir /var/jfsCache1 --cache-eviction $cache_eviction --free-space-ratio 0.2
    dd if=/dev/zero of=/tmp/test bs=1M count=1200
    cp /tmp/test /tmp/jfs/test
    ./juicefs warmup /tmp/jfs/test
    sleep 3 # wait to free space
    df -h /var/jfsCache1
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    used_percent=$(df /var/jfsCache1 | tail -1  | awk '{print $5}' | tr -d %)
    echo "used percent is $used_percent"
    if [[ $cache_eviction == "2-random" ]]; then 
        [[ $used_percent -gt 80 ]] && echo "used percent($used_percent) should not more than 80%" && exit 1 || true
    elif [[ $cache_eviction == "none" ]]; then
        # cache will not evict even reach the free-space-ratio.
        [[ $used_percent -lt 80 ]] && echo "used percent($used_percent) should not less than 80%" && exit 1 || true
    fi
}

test_inode_full(){
    prepare_test
    mount_jfsCache1 100G 1000
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir /var/jfsCache1 --free-space-ratio 0.2
    seq 1 1000 | xargs -P 8 -I {} sh -c 'echo {} > /tmp/jfs/test_{};'
    ./juicefs warmup /tmp/jfs/
    ./juicefs warmup /tmp/jfs/ --check 2>&1 | tee warmup.log
    sleep 3
    used_percent=$(df -i /var/jfsCache1 | tail -1  | awk '{print $5}' | tr -d %)
    [[ $used_percent -gt 85 ]] && echo "used percent($used_percent) should less than 85%" && exit 1 || true
}

test_disk_full_with_writeback(){
    prepare_test
    mount_jfsCache1 1G
    ./juicefs format $META_URL myjfs --compress zstd
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir /var/jfsCache1 --writeback --free-space-ratio 0.2 --upload-delay 5s
    dd if=/dev/urandom of=/tmp/test bs=1M count=1400
    cp /tmp/test /tmp/jfs/test
    wait_stage_uploaded
    sleep 3
    used_percent=$(df /var/jfsCache1 | tail -1  | awk '{print $5}' | tr -d %)
    [[ $used_percent -gt 80 ]] && echo "used percent($used_percent) should less than 80%" && exit 1 || true
    echo 3 > /proc/sys/vm/drop_caches
    ./juicefs warmup /tmp/jfs/test --evict
    compare_md5sum /tmp/test /tmp/jfs/test
}

test_disk_failover()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/log/juicefs.log
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0 --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    JFS_MAX_DURATION_TO_DOWN=10s JFS_MAX_IO_DURATION=3s ./juicefs mount $META_URL /tmp/jfs -d \
        --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3 --io-retries 1 
    dd if=/dev/urandom of=/tmp/test bs=1M count=1024
    cp /tmp/test /tmp/jfs/test
    /etc/init.d/redis-server stop
    ./juicefs warmup /tmp/jfs/test
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee warmup.log
    check_warmup_log  50
    wait_disk_down 60
    ./juicefs warmup /tmp/jfs/test
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee warmup.log
    check_warmup_log 98
    check_cache_distribute 1024 /var/jfsCache2 /var/jfsCache3
    echo stop minio && docker stop minio
    compare_md5sum /tmp/test /tmp/jfs/test
    docker start minio && sleep 3
}

test_disk_failure_on_writeback()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/log/juicefs.log
    rm -rf /var/jfsCache2 /var/jfsCache3
    mkdir -p /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0 --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    JFS_MAX_DURATION_TO_DOWN=5s JFS_MAX_IO_DURATION=3s ./juicefs mount $META_URL /tmp/jfs -d \
        --cache-dir=/var/jfsCache? --io-retries 1 --writeback -v
    dd if=/dev/urandom of=/tmp/test bs=1M count=1024
    cp /tmp/test /tmp/jfs/test
    dd if=/dev/urandom of=/tmp/jfs/test2 bs=1M count=10
    /etc/init.d/redis-server stop
    ./juicefs warmup /tmp/jfs/test2 &
    sleep 15
    grep -q "state change from unstable to down" /var/log/juicefs.log && echo "disk should not down" && exit 1 || true
    /etc/init.d/redis-server start
    ./juicefs warmup /tmp/jfs/test
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    # TODO: the ratio should be 100%
    check_warmup_log 60
    check_cache_distribute 1024 /var/jfsCache1 /var/jfsCache2 /var/jfsCache3
    compare_md5sum /tmp/test /tmp/jfs/test
}

prepare_test()
{
    df -h /
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
    [[ ! -f /usr/local/bin/mc ]] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc -O /usr/local/bin/mc && chmod +x /usr/local/bin/mc
    mc alias set myminio http://localhost:9000 minioadmin minioadmin
    mc rm --force --recursive myminio/test || true
}

wait_warmup_finish(){
    path=$1
    expected_ratio=$2
    timeout=30
    for i in $(seq 1 $timeout); do
        ./juicefs warmup $path --check 2>&1 |tee warmup.log
        ratio=$(get_warmup_ratio)
        if [[ "$ratio" == "$expected_ratio" ]]; then
            echo "warmup finished after $i seconds, ratio is $ratio, expected ratio is $expected_ratio"
            break
        else
            echo "wait warmup finish $i"
            sleep 1
        fi
        if [[ $i -eq $timeout ]]; then
            echo "wait warmup finish timeout after $timeout seconds" && exit 1
        fi
    done
}

wait_stage_uploaded()
{
    echo "wait stage upload"
    for i in {1..30}; do
        stageBlocks=$(grep "juicefs_staging_blocks" /tmp/jfs/.stats | awk '{print $2}')
        if [[ "$stageBlocks" -eq 0 ]]; then
            echo "stageBlocks is now 0"
            break
        fi
        echo "wait stage upload $i" && sleep 1
    done
    if [[ "$stageBlocks" -ne 0 ]]; then
        echo "stage blocks have not uploaded: $stageBlocks" && exit 1
    fi
}

mount_jfsCache1(){
    capacity=$1
    [[ -z $capacity ]] && capacity=100G
    inodes=$2
    [[ -z $inodes ]] && inodes=10000000
    /etc/init.d/redis-server start
    timeout 30s bash -c 'until nc -zv localhost 6379; do sleep 1; done'
    umount -l /var/jfsCache1 || true
    rm -rf /var/jfsCache1
    redis-cli flushall
    rm -rf /var/jfs/test
    ./juicefs format "redis://localhost/1?read-timeout=3&write-timeout=1&max-retry-backoff=3" test --trash-days 0 --capacity $capacity --inodes $inodes
    ./juicefs mount redis://localhost/1 /var/jfsCache1 -d --log /tmp/juicefs.log
    # trap "echo umount /var/jfsCache1 && umount -l /var/jfsCache1" EXIT
}

get_cache_dir(){
    grep CacheDir /tmp/jfs/.config | awk -F'"' '{print $4}'
}

get_raw_dir(){
    echo $(get_cache_dir)/raw/
}

get_rawstaging_dir(){
    echo $(get_cache_dir)/rawstaging/
}

check_evict_log(){
    ratio=$(get_warmup_ratio)
    if [[ "$ratio" -gt 0 ]]; then
        echo "cache ratio($ratio) should be 0 after evict"
        exit 1
    fi
}

check_warmup_log(){
    expected_ratio=$1
    ratio=$(get_warmup_ratio)
    if [[ "$ratio" -lt "$expected_ratio" ]]; then
        echo "cache ratio($ratio) should be more than expected_ratio($expected_ratio) after warmup"
        exit 1
    fi
}

get_cache_file_count(){
    sed -n 's/.* \([0-9]\+\) files.*/\1/p' warmup.log
}

get_cache_file_size(){
    sed -n 's/.* \([0-9]*\) MiB of.*/\1/p' warmup.log
}

get_warmup_ratio(){
    sed -n 's/.*(\([0-9]*\.[0-9]*%\)).*/\1/p' warmup.log | sed 's/%//' | awk '{print int($1)}'
}


check_cache_distribute() {
    max_total_size=$(echo "$1 * 1024" | bc | awk '{printf "%.0f", $1}')
    echo check_cache_distribute, max_total_size is $max_total_size
    shift
    total_weight=0
    declare -A weights
    declare -A sizes
    # Parse directory names and weights
    for arg in "$@"; do
        dir=$(echo "$arg" | awk -F: '{print $1}')
        weight=$(echo "$arg" | awk -F: '{print $2}')
        if [[ -z $weight ]]; then
            weight=1
        fi
        weights["$dir"]=$weight
        total_weight=$((total_weight + weight))
    done
    
    # Calculate total size and sizes of each directory
    for dir in "${!weights[@]}"; do
        echo dir is $dir
        du -sh "$dir" || true
        size=$(du -s "$dir" | awk '{print $1}')
        echo size is $size
        sizes["$dir"]=$size
    done
    
    # Check if total size exceeds max limit
    total_size=0
    for dir in "${!sizes[@]}"; do
        size=${sizes["$dir"]}
        total_size=$((total_size + size))
    done
    echo "total size is $total_size, max_total_size is $max_total_size"
    if [[ $total_size -gt $((max_total_size + max_total_size/10)) ]]; then
        echo "Total size of directories exceeds max limit"
        return 1
    fi
    
    # Check if each directory is evenly distributed based on its weight
    for dir in "${!sizes[@]}"; do
        size=${sizes["$dir"]}
        weight=${weights["$dir"]}
        avg_size=$((total_size * weight / total_weight))
        min_size=$((avg_size * 5 / 10))
        max_size=$((avg_size * 20 / 10))
        
        if [[ $size -lt $min_size || $size -gt $max_size ]]; then
            echo "$dir is not evenly distributed, size: $size, weight: $weight, ave_size: $avg_size, min_size: $min_size, max_size: $max_size"
            exit 1
        else
            echo "$dir is evenly distributed"
        fi
    done
}

wait_disk_down()
{
    timeout=$1
    for i in $(seq 1 $timeout); do
        if grep -q "state change from unstable to down" /var/log/juicefs.log; then
            echo "state changed from unstable to down after $i seconds"
            return
        else
            echo "\rWait for state change to down, $i"
            sleep 1
            count=$((count+1))
        fi
    done
    echo "Wait for state change to down timeout after $timeout seconds" && exit 1
}   

source .github/scripts/common/run_test.sh && run_test $@

