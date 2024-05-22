#!/bin/bash -e
dpkg -s redis-server || .github/scripts/apt_install.sh  redis-tools redis-server
source .github/scripts/common/common.sh
source .github/scripts/start_meta_engine.sh
[[ -z "$META" ]] && META=sqlite3
start_meta_engine $META minio
META_URL=$(get_meta_url $META)
TEST_FILE_SIZE=1024

prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
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

test_disk_failover()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/log/juicefs.log
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0
    JFS_MAX_DURATION_TO_DOWN=10s ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3 --io-retries 1
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

test_mount_same_disk_after_failure()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3
    rm -rf /tmp/test_failover
    dd if=/dev/urandom of=/tmp/test_failover bs=1M count=$TEST_FILE_SIZE
    cp /tmp/test_failover /tmp/jfs/test_failover
    ./juicefs warmup /tmp/jfs/test_failover
    # 坏盘恢复后重新挂载
    mv cache.db cache.db.bak
    cp /tmp/jfs/test_failover  /dev/null
    echo "sleep 5s to wait clean up" && sleep 5
    mv cache.db.bak cache.db
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache2:/var/jfsCache3:/var/jfsCache1
    echo "sleep 3s to wait to build cache in memory " && sleep 3
    du -sh /var/jfsCache1 /var/jfsCache2 /var/jfsCache3 || true
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log 98
    echo stop minio && docker stop minio
    compare_md5sum /tmp/test_failover /tmp/jfs/test_failover
    docker start minio && sleep 3
}


skip_test_rebalance_after_disk_failure_and_replace()
{
    prepare_test
    mount_jfsCache1
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3
    rm -rf /tmp/test_failover
    dd if=/dev/urandom of=/tmp/test_failover bs=1M count=$TEST_FILE_SIZE
    cp /tmp/test_failover /tmp/jfs/test_failover
    ./juicefs warmup /tmp/jfs/test_failover
    du -sh /var/jfsCache? || true
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log 98
    check_cache_distribute $TEST_FILE_SIZE /var/jfsCache1 /var/jfsCache2 /var/jfsCache3
    # 坏盘后换一张新盘挂载
    mv cache.db cache.db.bak
    # echo "stop redis server" && /etc/init.d/redis-server stop
    cp /tmp/jfs/test_failover  /dev/null
    echo "sleep 5s to wait cleanup" && sleep 5
    ./juicefs warmup /tmp/jfs/test_failover
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log 98
    umount /var/jfsCache1 -l || true
    rm /var/jfsCache1 -rf 
    ./juicefs mount $META_URL /tmp/jfs -d --cache-dir=/var/jfsCache2:/var/jfsCache1:/var/jfsCache3
    echo "wait rebalance after disk replacement" 
    for i in {1..30}; do
        du -sh /var/jfsCache1 /var/jfsCache2 /var/jfsCache3 || true
        ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
        grep "(100.0%)" check.log && "check cache succeed" && break
        echo "sleep to wait rebalance... " && sleep 1
    done
    check_warmup_log check.log 98
    check_cache_distribute $TEST_FILE_SIZE /var/jfsCache1 /var/jfsCache2 /var/jfsCache3
    echo stop minio && docker stop minio
    compare_md5sum /tmp/test_failover /tmp/jfs/test_failover
    docker start minio && sleep 3
    rm -rf /tmp/test_failover
}



source .github/scripts/common/run_test.sh && run_test $@

