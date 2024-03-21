#!/bin/bash -e
source .github/scripts/common/common.sh
.github/scripts/apt_install.sh  redis-tools redis-server
META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

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
    ./juicefs format redis://localhost/1 test --trash-days 0
    ./juicefs mount redis://localhost/1 /var/jfsCache1 -d --log /tmp/juicefs.log
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
    result=$(cat $log |  sed 's/.*(\([0-9]*\.[0-9]*%\)).*/\1/' | sed 's/%//')
    if (( $(echo "$result < 98" | bc -l) )); then
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
        min_size=$((avg_size * 7 / 10))
        max_size=$((avg_size * 13 / 10))
        
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
    rm -rf /var/jfsCache2 /var/jfsCache3
    ./juicefs mount $META /tmp/jfs --cache-dir=/var/jfsCache1:/var/jfsCache2:/var/jfsCache3
    rm -rf /tmp/test_failover
    dd if=/dev/urandom of=/tmp/test_failover bs=1M count=$TEST_FILE_SIZE
    cp /tmp/test_failover /tmp/jfs/test_failover
    ./juicefs warmup /tmp/jfs/test_failover
    du -sh /var/jfsCache?
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log
    check_cache_distribute $TEST_FILE_SIZE /var/jfsCache1 /var/jfsCache2 /var/jfsCache3
    ./juicefs warmup --evict /tmp/jfs/test_failover
    du -sh /var/jfsCache?
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_evict_log check.log
    /etc/init.d/redis-server stop
    sleep 10
    ./juicefs warmup /tmp/jfs/test_failover
    du -sh /var/jfsCache2 /var/jfsCache3
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    sleep 10
    ./juicefs warmup /tmp/jfs/test_failover
    du -sh /var/jfsCache2 /var/jfsCache3
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_warmup_log check.log
    check_cache_distribute $TEST_FILE_SIZE /var/jfsCache2 /var/jfsCache3
    echo stop minio && docker stop minio
    compare_md5sum /tmp/test_failover /tmp/jfs/test_failover
    docker start minio && sleep 3
    ./juicefs warmup --evict /tmp/jfs
    ./juicefs warmup --check /tmp/jfs 2>&1 | tee check.log
    check_evict_log check.log
}


source .github/scripts/common/run_test.sh && run_test $@

