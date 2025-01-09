#!/bin/bash -ex
dpkg -s redis-server || .github/scripts/apt_install.sh  redis-tools redis-server
source .github/scripts/common/common.sh
source .github/scripts/start_meta_engine.sh
[[ -z "$META" ]] && META=sqlite3
start_meta_engine $META minio
META_URL=$(get_meta_url $META)
TEST_FILE_SIZE=1024

test_batch_warmup(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d
    rm -f file.list
    file_count=11000
    time seq 1 $file_count | xargs -P 8 -I {} sh -c 'echo {} > /tmp/jfs/test_{}; echo /tmp/jfs/test_{} >> file.list'
    # time for i in $(seq 1 $file_count); do echo $i > /tmp/jfs/test_$i; echo /tmp/jfs/test_$i >> file.list; done
    ./juicefs warmup -f file.list 2>&1 | tee warmup.log
    files=$(sed -n 's/.*cache: \([0-9]\+\) files.*/\1/p'  warmup.log)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    ./juicefs warmup -f file.list --check 2>&1 | tee warmup.log
    files=$(sed -n 's/.*cache: \([0-9]\+\) files.*/\1/p'  warmup.log)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    grep "(100.0%)" warmup.log || (echo "warmup failed, expect 100.0% warmup" && exit 1)

    ./juicefs warmup -f file.list --evict 2>&1 | tee warmup.log 
    files=$(sed -n 's/.*cache: \([0-9]\+\) files.*/\1/p'  warmup.log)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    ./juicefs warmup -f file.list --check 2>&1 | tee warmup.log
    files=$(sed -n 's/.*cache: \([0-9]\+\) files.*/\1/p'  warmup.log)
    [[ $files -ne $file_count ]] && echo "warmup failed, expect $file_count files, actual $files" && exit 1 || true
    grep "(0.0%)" warmup.log || (echo "warmup failed, expect 0.0% warmup" && exit 1)
}

test_evict_on_writeback(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d --writeback --upload-delay 5s
    dd if=/dev/urandom of=/tmp/test bs=1M count=$TEST_FILE_SIZE
    cp /tmp/test /tmp/jfs/test
    ./juicefs warmup /tmp/jfs/test --evict
    wait_stage_uploaded
    compare_md5sum /tmp/test /tmp/jfs/test
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
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir $cache_dir --cache-expire 3s 
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=200
    ./juicefs warmup /tmp/jfs/test 2>&1 | tee warmup.log
    sleep 15
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    grep "(0.0%)" warmup.log || (echo "cache should expired" && exit 1)
}

test_cache_large_write(){
    cache_dir=$1
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir $cache_dir --cache-large-write 
    dd if=/dev/zero of=/tmp/jfs/test bs=1M count=200
    ./juicefs warmup /tmp/jfs/test --check 2>&1 | tee warmup.log
    check_warmup_log warmup.log 90
}

test_disk_failover()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/log/juicefs.log
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0
    JFS_MAX_DURATION_TO_DOWN=10s JFS_MAX_IO_DURATION=3s ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3 --io-retries 1
    dd if=/dev/urandom of=/tmp/test_failover bs=1M count=$TEST_FILE_SIZE
    cp /tmp/test_failover /tmp/jfs/test_failover
    /etc/init.d/redis-server stop
    ./juicefs warmup /tmp/jfs/test_failover
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log 50
    wait_disk_down 60
    ./juicefs warmup /tmp/jfs/test_failover
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log 98
    check_cache_distribute $TEST_FILE_SIZE /var/jfsCache2 /var/jfsCache3
    echo stop minio && docker stop minio
    compare_md5sum /tmp/test_failover /tmp/jfs/test_failover
    docker start minio && sleep 3
}


prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
}

wait_stage_uploaded()
{
    echo "wait stage upload"
    for i in {1..30}; do
        stageBlocks=$(grep "stageBlocks:" /tmp/jfs/.stats | awk '{print $2}')
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

wait_cache_expired()
{
    file=$1
    timeout=$2
    [[ -z $timeout ]] && timeout=30
    echo "wait cache expired"
    for i in $(seq 1 $timeout); do
        ./juicefs warmup $file --check 2>&1 | tee warmup.log
        if grep "(0.0%)" warmup.log; then
            echo "cache expired for $file"
            return
        fi
        echo "wait cache expired $i" && sleep 1
    done
    echo "cache not expired failed for $file in $timeout sec" && exit 1
}

mount_jfsCache1(){
    /etc/init.d/redis-server start
    timeout 30s bash -c 'until nc -zv localhost 6379; do sleep 1; done'
    umount -l /var/jfsCache1 || true
    rm -rf /var/jfsCache1
    redis-cli flushall
    rm -rf /var/jfs/test
    ./juicefs format "redis://localhost/1?read-timeout=3&write-timeout=1&max-retry-backoff=3" test --trash-days 0
    ./juicefs mount redis://localhost/1 /var/jfsCache1 -d --log /tmp/juicefs.log
    trap "echo umount /var/jfsCache1 && umount -l /var/jfsCache1" EXIT
}

check_evict_log(){
    log=$1
    result=$(cat $log |  sed 's/.*(\([0-9]*\.[0-9]*%\)).*/\1/' | sed 's/%//')
    if (( $(echo "$result > 1" | bc -l) )); then
        echo "cache ratio should be less than 1% after evict, actual is $result"
        exit 1
    fi
}

check_warmup_log(){
    log=$1
    ratio=$2
    result=$(cat $log |  sed 's/.*(\([0-9]*\.[0-9]*%\)).*/\1/' | sed 's/%//')
    if (( $(echo "$result < $ratio" | bc -l) )); then
        echo "cache ratio should be more than 98% after warmup, actual is $result"
        exit 1
    fi
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
            echo "state changed from unstable to down"
            return
        else
            echo "\rWait for state change to down, $i"
            sleep 1
            count=$((count+1))
        fi
    done
    echo "Wait for state change to down timeout" && exit 1
}   

source .github/scripts/common/run_test.sh && run_test $@

