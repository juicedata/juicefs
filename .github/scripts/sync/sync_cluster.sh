#!/bin/bash -e
source .github/scripts/common/common.sh
[[ -z "$CI" ]] && CI=false
[[ -z "$META" ]] && META=redis
[[ -z "$KEY_TYPE" ]] && KEY_TYPE=ed25519
[[ -z "$FILE_COUNT" ]] && FILE_COUNT=600
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
start_minio(){
    if ! docker ps | grep "minio/minio"; then
        docker run -d -p 9000:9000 --name minio \
                -e "MINIO_ACCESS_KEY=minioadmin" \
                -e "MINIO_SECRET_KEY=minioadmin" \
                -v /tmp/data:/data \
                -v /tmp/config:/root/.minio \
                minio/minio server /data
        sleep 3s
    fi
    [ ! -x mc ] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc && chmod +x mc
    ./mc alias set myminio http://localhost:9000 minioadmin minioadmin || ./mc alias set myminio http://127.0.0.1:9000 minioadmin minioadmin
}
start_minio
start_worker(){
    if getent group juicedata ; then groupdel -f juicedata; echo delete juicedata group; fi
    if getent passwd juicedata ; then rm -rf /home/juicedata && userdel -f juicedata; echo delete juicedata user; fi
    groupadd juicedata && useradd -ms /bin/bash -g juicedata juicedata -u 1024
    if [ "$CI" != "true" ] && [ -f ~/.ssh/id_rsa ]; then
        echo "ssh key already exists, don't overwrite it in non ci environment"
    else
        echo "generating ssh key with type $KEY_TYPE"
        yes |sudo -u juicedata ssh-keygen -t $KEY_TYPE -C "default" -f /home/juicedata/.ssh/id_rsa -q -N ""
        chmod 600 /home/juicedata/.ssh/id_rsa
    fi
    cp -f /home/juicedata/.ssh/id_rsa.pub .github/scripts/ssh/id_rsa.pub
    docker build -t juicedata/ssh -f .github/scripts/ssh/Dockerfile .github/scripts/ssh
    docker rm worker1 worker2 -f
    docker compose -f .github/scripts/ssh/docker-compose.yml up -d
    sleep 3s
    sudo -u juicedata ssh -o BatchMode=yes -o StrictHostKeyChecking=no juicedata@172.20.0.2 exit
    sudo -u juicedata ssh -o BatchMode=yes -o StrictHostKeyChecking=no juicedata@172.20.0.3 exit
}
start_worker

sed -i 's/bind 127.0.0.1 ::1/bind 0.0.0.0 ::1/g' /etc/redis/redis.conf
systemctl restart redis
META_URL=$(echo $META_URL | sed 's/127\.0\.0\.1/172.20.0.1/g')
# github runner 22.04 will set /home/runner to 750, which make juicefs binary not accessed by other users.
chmod 755 /home/runner/

CLUSTER_CHECKPOINT_OPTS="--enable-checkpoint --checkpoint-interval 2s"

test_sync_without_mount_point(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=$FILE_COUNT
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=1M count=1 status=none
    done
    dd if=/dev/urandom of=/jfs/data/file$file_count bs=1M count=1024
    (./mc rb myminio/data1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/data1
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/data1/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change \
         >sync.log 2>&1
    # diff data/ /jfs/data1/
    check_sync_log $file_count
    ./mc rm -r --force myminio/data1
}

test_sync_without_mount_point2(){
    prepare_test
    file_count=$FILE_COUNT
    rm -rf data/
    mkdir -p data/
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=data/file$i bs=1M count=1 status=none
    done
    dd if=/dev/urandom of=data/file$file_count bs=1M count=1024
    (./mc rb myminio/data > /dev/null 2>&1 --force || true) && ./mc mb myminio/data
    ./mc cp -r data myminio/data
    
    # (./mc rb myminio/data1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/data1
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change \
         >sync.log 2>&1
    set +o pipefail
    check_sync_log $file_count
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 --start-time 2020-01-01 \
         --list-threads 10 --list-depth 5 --check-all \
         >sync.log 2>&1
    set +o pipefail
    ./juicefs mount -d $META_URL /jfs
    diff data/ /jfs/data/
    current_time=$(date -d "1 minute ago" "+%Y-%m-%d %H:%M:%S")
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=data/file$i bs=1M count=2 status=none
    done
    dd if=/dev/urandom of=data/file$file_count bs=1M count=10
    ./mc cp -r data myminio/data
    sleep 2
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 --start-time "$current_time" \
         --list-threads 10 --list-depth 5 --update \
         >sync.log 2>&1
    set +o pipefail
    diff data/ /jfs/data/
    ./mc rm -r --force myminio/data
    rm -rf data
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal or ERROR in sync.log" && exit 1 || true
}   

test_sync_delete_src_and_update(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=$FILE_COUNT
    rm -rf data
    mkdir -p data
    for i in $(seq 1 $file_count); do
        echo "test-$i" > data/test-$i
    done
    ./mc cp -r data myminio/data
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --dirs --check-change \
         >sync.log 2>&1
    set +o pipefail
    diff data/ /jfs/data/
    rm sync.log
    for i in $(seq 1 $file_count); do
        echo "test-update-$i" > data/test-$i
    done
    ./mc cp -r data myminio/data
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --delete-src --update --dirs --check-change \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    set +o pipefail
    diff data/ /jfs/data/
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --delete-src --dirs --check-change \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    set +o pipefail
    if ./mc ls myminio/data/ | grep -q .; then
        echo "Error: MinIO bucket /data is not empty"
        exit 1
    fi
    diff data/ /jfs/data/
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal or ERROR in sync.log" && exit 1 || true
}

test_sync_delete_dst(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=$FILE_COUNT
    rm -rf data
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=1M count=1 status=none
    done
    dd if=/dev/urandom of=/jfs/data/file$file_count bs=1M count=1024
    echo "retain" > /jfs/data/retain
    chmod -R 777 /jfs/data
    rm -rf empty && mkdir empty
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --delete-dst --match-full-path --exclude='retain' --include='*' \
         ./empty/ jfs://meta_url/data/  --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-new \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    [ ! -f /jfs/data/retain ] && echo "Error: retain file was incorrectly deleted" && exit 1 || true
}

test_sync_with_random_test(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/test || true
    mkdir /jfs/test2 || true
    current_time=$(date -d "1 minute ago" "+%Y-%m-%d %H:%M:%S")
    ./random-test runOp -baseDir /jfs/test -files 100000 -ops 1000000 -threads 50 -dirSize 100 -duration 60s -createOp 30,uniform \
    -deleteOp 5,end --linkOp 10,uniform --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform
    chmod -R 777 /jfs/test
    chmod -R 777 /jfs/test2
    run_id=1
    for sig in INT KILL INT; do
        sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-new --links --dirs --start-time "$current_time" \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1 &
        sync_pid=$!
        sleep 2
        signal_cluster_sync_child "$sync_pid" "$sig"
        wait "$sync_pid" || true
        # Clean up stale worker processes (especially after SIGKILL which can't gracefully shutdown workers)
        sudo -u juicedata ssh -o ConnectTimeout=3 juicedata@172.20.0.2 "pkill -9 -f 'juicefs'" 2>/dev/null || true
        sudo -u juicedata ssh -o ConnectTimeout=3 juicedata@172.20.0.3 "pkill -9 -f 'juicefs'" 2>/dev/null || true
        sleep 1
        checkpoint_count=$(find /jfs/test2 -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
        if [ "$checkpoint_count" -eq 0 ]; then
            echo "checkpoint file should exist after interrupted cluster sync run $run_id"
            exit 1
        fi
        run_id=$((run_id + 1))
    done
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-new --links --dirs --start-time "$current_time" \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    diff -ur --no-dereference --exclude='.jfs.file*.tmp.*' /jfs/test /jfs/test2
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs --delete-src --match-full-path jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-all --links --start-time 2199-12-30 \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true 
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs --delete-src --match-full-path jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 --dirs \
         --list-threads 10 --list-depth 5 --check-all --links --start-time "$current_time" \
         --enable-checkpoint --checkpoint-interval 2s \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    [ -z "$(ls -A /jfs/test)" ] || exit 1
    echo "delete src test passed"
    rm -rf empty || true
    mkdir empty || true
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs --delete-dst --match-full-path  --include='*' \
         ./empty/ jfs://meta_url/test2/ --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change --dirs --links --start-time "$current_time" \
         --enable-checkpoint --checkpoint-interval 2s \
         2>&1 | tee sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    [ -z "$(ls -A /jfs/test2)" ] || exit 1
}

test_sync_files_from_file(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/test || true
    mkdir /jfs/test2 || true
    ./random-test runOp -baseDir /jfs/test -files 50000 -ops 500000 -threads 50 -dirSize 100 -duration 30s -createOp 30,uniform \
    -deleteOp 5,end --linkOp 10,uniform --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform
    chmod -R 777 /jfs/test
    chmod -R 777 /jfs/test2
    ls /jfs/test > files | tee files
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-all --check-change --links --dirs --files-from files \
         --enable-checkpoint --checkpoint-interval 2s \
         2>&1 | tee sync.log
    grep "panic\|<FATAL>\|ERROR" sync.log && echo "panic or fatal or error in sync.log" && exit 1 || true
}

test_sync_chown_perms(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/data
    for i in $(seq 1 $FILE_COUNT); do
        mkdir /jfs/data/test$i
        dd if=/dev/urandom of=/jfs/data/test$i/file$i bs=1M count=1 status=none
    done
    sudo chown 1000:1000 /jfs/data -R
    sudo chmod -R 777 /jfs/data
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/data/ jfs://meta_url/data2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-all --links --dirs --perms \
         >sync.log 2>&1
    grep "panic\|<FATAL>\|ERROR" sync.log && echo "panic or fatal or error in sync.log" && exit 1 || true
    diff /jfs/data/ /jfs/data2/
}

skip_test_sync_between_oss(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/test
    file_count=$FILE_COUNT
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/file$i bs=1M count=1 status=none
    done
    start_gateway
    sudo -u juicedata ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9005/myjfs/ \
         minio://minioadmin:minioadmin@172.20.0.1:9000/myjfs/ \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        > sync.log 2>&1
    count1=$(./mc ls myminio/myjfs/test -r | wc -l)
    count2=$(./mc ls juicegw/myjfs/test -r | awk '$4=="5MiB"' | wc -l)
    if [ "$count1" != "$count2" ]; then
        echo "count not equal, $count1, $count2"
        exit 1
    fi
    check_sync_log $file_count
}

test_sync_worker_down(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=$FILE_COUNT 
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        echo "test-$i" > /jfs/data/test-$i
    done
    docker stop worker1
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/data/ jfs://meta_url/data2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-new \
         >sync.log 2>&1
    diff /jfs/data/ /jfs/data2/
    docker start worker1
}

check_sync_log(){
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    file_count=$1
    if tail -1 sync.log | grep -q "close session"; then
      file_copied=$(tail -n 3 sync.log | head -n 1  | sed 's/.*copied: \([0-9]*\).*/\1/' )
    else
      file_copied=$(tail -1 sync.log  | sed 's/.*copied: \([0-9]*\).*/\1/' )
    fi
    if [ "$file_copied" != "$file_count" ]; then
        echo "file_copied not equal, $file_copied, $file_count"
        exit 1
    fi
    count2=$(grep 172.20.0.2 sync.log | grep "receive stats" | awk '{if (match($0, /Copied:[0-9]+/)) sum += substr($0, RSTART + 7, RLENGTH - 7)} END {print sum + 0}')
    [ -z "$count2" ] && count2=0
#    count3=$(cat sync.log | grep 172.20.0.3 | grep "receive stats" | gawk '{sum += gensub(/.*Copied:([0-9]+).*/, "\\1", "g");} END {print sum;}')
    count3=$(grep 172.20.0.3 sync.log | grep "receive stats" | awk '{if (match($0, /Copied:[0-9]+/)) sum += substr($0, RSTART + 7, RLENGTH - 7)} END {print sum + 0}')
    [ -z "$count3" ] && count3=0
    count1=$((file_count - count2 - count3))
    echo "count1, $count1, count2, $count2, count3, $count3"
    min_count=10
    # check if count1 is less than min_count
    if [ "$count1" -lt "$min_count" ] || [ "$count2" -lt "$min_count" ] || [ "$count3" -lt "$min_count" ]; then
        echo "count is less than min_count, $count1, $count2, $count3, $min_count"
        exit 1
    fi
}

signal_cluster_sync_child(){
    local sync_pid=$1
    local signal=$2
    local juicefs_pid=""
    for _ in $(seq 1 5); do
        juicefs_pid=$(ps -o pid= --ppid "$sync_pid" | head -n 1 | tr -d ' ')
        [ -n "$juicefs_pid" ] && break
        sleep 1
    done
    [ -z "$juicefs_pid" ] && echo "failed to find juicefs sync child process" && exit 1
    kill -$signal "$juicefs_pid" || true
}

prepare_test(){
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
    rm -rf /var/jfsCache/myjfs
    (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
    ./juicefs format $META_URL myjfs --storage minio --access-key minioadmin --secret-key minioadmin --bucket http://172.20.0.1:9000/myjfs
}
start_gateway(){
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL 172.20.0.1:9005 &
    ./mc alias set juicegw http://172.20.0.1:9005 minioadmin minioadmin --api S3v4
}

# ---- sync encryption / decryption tests (cluster) ----

setup_sync_encrypt_keys(){
    openssl genrsa -out /tmp/sync-enc-nopass.pem 2048
    openssl genrsa -out /tmp/sync-enc-wrong.pem 2048
    openssl genrsa -aes256 -passout pass:cluster-enc-pass -out /tmp/sync-enc-withpass.pem 2048
    # Make keys readable by juicedata user
    chmod 666 /tmp/sync-enc-nopass.pem /tmp/sync-enc-wrong.pem /tmp/sync-enc-withpass.pem
    # Copy keys to worker nodes
    for node in 172.20.0.2 172.20.0.3; do
        sudo -u juicedata scp -o StrictHostKeyChecking=no /tmp/sync-enc-nopass.pem juicedata@$node:/tmp/sync-enc-nopass.pem
        sudo -u juicedata scp -o StrictHostKeyChecking=no /tmp/sync-enc-wrong.pem juicedata@$node:/tmp/sync-enc-wrong.pem
        sudo -u juicedata scp -o StrictHostKeyChecking=no /tmp/sync-enc-withpass.pem juicedata@$node:/tmp/sync-enc-withpass.pem
    done
}
setup_sync_encrypt_keys

# Encrypt: jfs -> minio with distributed workers
test_sync_encrypt_cluster_basic(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=100
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=1K count=$((RANDOM % 512 + 1)) status=none
    done
    # One multi-chunk file
    dd if=/dev/urandom of=/jfs/data/bigfile bs=1M count=5 status=none
    chmod -R 777 /jfs/data

    (./mc rb myminio/encdata > /dev/null 2>&1 --force || true) && ./mc mb myminio/encdata
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/encdata/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in encrypt sync.log" && exit 1 || true

    # Verify minio has all files
    enc_count=$(./mc ls -r myminio/encdata/ | wc -l)
    src_count=$(find /jfs/data -type f | wc -l)
    [ "$enc_count" -eq "$src_count" ] || (echo "FAIL: file count mismatch enc=$enc_count src=$src_count" && exit 1)

    # Decrypt back to a new JFS volume
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs /var/jfsCache/myjfs
    (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
    ./juicefs format $META_URL myjfs --storage minio --access-key minioadmin --secret-key minioadmin --bucket http://172.20.0.1:9000/myjfs
    ./juicefs mount -d $META_URL /jfs
    chmod -R 777 /jfs

    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/encdata/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in decrypt sync.log" && exit 1 || true

    dec_count=$(find /jfs/decdata -type f | wc -l)
    [ "$dec_count" -eq "$src_count" ] || (echo "FAIL: decrypt count mismatch dec=$dec_count src=$src_count" && exit 1)
    echo "test_sync_encrypt_cluster_basic passed"
}

# Encrypt with chacha20-rsa in cluster mode
test_sync_encrypt_cluster_chacha20(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 50); do
        echo "chacha20-cluster-$i" > /jfs/data/file$i.txt
    done
    dd if=/dev/urandom of=/jfs/data/large.bin bs=1M count=3 status=none
    chmod -R 777 /jfs/data

    (./mc rb myminio/encch > /dev/null 2>&1 --force || true) && ./mc mb myminio/encch
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/encch/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo chacha20-rsa $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true

    chmod 777 /jfs
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/encch/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo chacha20-rsa $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in decrypt sync.log" && exit 1 || true

    [ "$(cat /jfs/decdata/file1.txt)" = "chacha20-cluster-1" ] || (echo "FAIL: chacha20 cluster decrypt mismatch" && exit 1)
    cmp /jfs/data/large.bin /jfs/decdata/large.bin || (echo "FAIL: large binary mismatch" && exit 1)
    echo "test_sync_encrypt_cluster_chacha20 passed"
}

# Re-encrypt in cluster mode: decrypt key1 -> encrypt key2
test_sync_reencrypt_cluster(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 30); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=1K count=$((RANDOM % 256 + 1)) status=none
    done
    chmod -R 777 /jfs/data

    (./mc rb myminio/reenc1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/reenc1
    (./mc rb myminio/reenc2 > /dev/null 2>&1 --force || true) && ./mc mb myminio/reenc2

    # Encrypt with key1
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/reenc1/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    # Re-encrypt: key1 -> key2
    sudo -u juicedata ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/reenc1/ minio://minioadmin:minioadmin@172.20.0.1:9000/reenc2/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-rsa-key /tmp/sync-enc-wrong.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    # Decrypt with key2 and verify
    chmod 777 /jfs
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/reenc2/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-wrong.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    diff /jfs/data/ /jfs/decdata/ || (echo "FAIL: reencrypt cluster data mismatch" && exit 1)
    echo "test_sync_reencrypt_cluster passed"
}

# Encrypt with passphrase-protected key in cluster mode
test_sync_encrypt_cluster_passphrase(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 20); do
        echo "passphrase-test-$i" > /jfs/data/file$i.txt
    done
    chmod -R 777 /jfs/data

    (./mc rb myminio/encpass > /dev/null 2>&1 --force || true) && ./mc mb myminio/encpass
    sudo -u juicedata JFS_ENCRYPT_RSA_PASSPHRASE=cluster-enc-pass meta_url=$META_URL \
        ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/encpass/ \
        --encrypt-rsa-key /tmp/sync-enc-withpass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in encrypt sync.log" && exit 1 || true

    chmod 777 /jfs
    sudo -u juicedata JFS_DECRYPT_RSA_PASSPHRASE=cluster-enc-pass meta_url=$META_URL \
        ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/encpass/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-withpass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in decrypt sync.log" && exit 1 || true

    [ "$(cat /jfs/decdata/file1.txt)" = "passphrase-test-1" ] || (echo "FAIL: passphrase decrypt mismatch" && exit 1)
    diff /jfs/data/ /jfs/decdata/ || (echo "FAIL: passphrase cluster data mismatch" && exit 1)
    echo "test_sync_encrypt_cluster_passphrase passed"
}

# Encrypt with --update --check-all in cluster mode
test_sync_encrypt_cluster_update(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 50); do
        echo "initial-$i" > /jfs/data/file$i.txt
    done
    chmod -R 777 /jfs/data

    (./mc rb myminio/encupd > /dev/null 2>&1 --force || true) && ./mc mb myminio/encupd
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/encupd/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    # Update some files
    sleep 2
    for i in $(seq 1 10); do
        echo "updated-$i" > /jfs/data/file$i.txt
    done

    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/encupd/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --update --check-all $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    # Decrypt and verify
    chmod 777 /jfs
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/encupd/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal" && exit 1 || true

    [ "$(cat /jfs/decdata/file1.txt)" = "updated-1" ] || (echo "FAIL: updated file mismatch" && exit 1)
    [ "$(cat /jfs/decdata/file20.txt)" = "initial-20" ] || (echo "FAIL: non-updated file mismatch" && exit 1)
    echo "test_sync_encrypt_cluster_update passed"
}


# ---- sync global traffic control tests (cluster) ----

TC_PORT=12345
TC_URL="http://172.20.0.1:${TC_PORT}/"
TC_LOG="/tmp/tc_server.log"

start_traffic_control_server(){
    local bwlimit=${1:-0}
    kill_traffic_control_server
    python3 .github/scripts/traffic_control_server.py --port $TC_PORT --bwlimit $bwlimit --log $TC_LOG &
    TC_PID=$!
    sleep 1
    if ! kill -0 $TC_PID 2>/dev/null; then
        echo "FAIL: traffic control server failed to start"
        exit 1
    fi
}

kill_traffic_control_server(){
    lsof -i :$TC_PORT -t 2>/dev/null | xargs -r kill -9 || true
    sleep 0.5
}

check_tc_log(){
    local min_requests=${1:-1}
    [ ! -f $TC_LOG ] && echo "FAIL: TC log not found" && exit 1
    local req_count=$(wc -l < $TC_LOG)
    if [ "$req_count" -lt "$min_requests" ]; then
        echo "FAIL: expected at least $min_requests TC requests, got $req_count"
        cat $TC_LOG
        exit 1
    fi
    echo "TC log: $req_count requests"
}

# Traffic control with bwlimit combined: JFS -> minio with cluster
test_sync_traffic_control_cluster_with_bwlimit(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=100
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=50K count=1 status=none
    done
    chmod -R 777 /jfs/data
    (./mc rb myminio/tcdata > /dev/null 2>&1 --force || true) && ./mc mb myminio/tcdata
    start_traffic_control_server 0
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/tcdata/ \
        --traffic-control-url $TC_URL --bwlimit 8192 \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    enc_count=$(./mc ls -r myminio/tcdata/ | wc -l)
    [ "$enc_count" -eq "$file_count" ] || (echo "FAIL: count mismatch enc=$enc_count expected=$file_count" && exit 1)
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_cluster_with_bwlimit passed"
}


# Traffic control with encrypt in cluster mode
test_sync_traffic_control_cluster_encrypt(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 30); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=10K count=1 status=none
    done
    chmod -R 777 /jfs/data
    (./mc rb myminio/tcenc > /dev/null 2>&1 --force || true) && ./mc mb myminio/tcenc
    start_traffic_control_server 0
    # Encrypt with traffic control
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/tcenc/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --traffic-control-url $TC_URL $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in encrypt sync.log" && exit 1 || true
    # Decrypt with traffic control
    chmod 777 /jfs
    sudo -u juicedata meta_url=$META_URL ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9000/tcenc/ jfs://meta_url/decdata/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --traffic-control-url $TC_URL $CLUSTER_CHECKPOINT_OPTS \
        --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        >sync.log 2>&1
    cat sync.log
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in decrypt sync.log" && exit 1 || true
    diff /jfs/data/ /jfs/decdata/ || (echo "FAIL: traffic control encrypt/decrypt data mismatch" && exit 1)
    check_tc_log 2
    kill_traffic_control_server
    echo "test_sync_traffic_control_cluster_encrypt passed"
}

test_sync_files_from_ignore_nonexistent_cluster(){
    # PR #6339: verify files-from gracefully ignores non-existent paths in cluster mode
    prepare_test
    ./juicefs mount -d $META_URL /jfs

    # Create source files
    mkdir -p /jfs/src
    for i in $(seq 1 30); do
        echo "content-$i" > /jfs/src/file$i
    done
    chmod -R 777 /jfs/src

    # Create files-from list with both existing and non-existing paths
    cat > /tmp/files_list << 'EOF'
file1
file2
file3
missing_aaa
missing_bbb
missing_ccc
file10
file20
file30
EOF
    chmod 644 /tmp/files_list

    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/src/ jfs://meta_url/dst/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --files-from /tmp/files_list \
         >sync.log 2>&1

    # Verify non-existent paths were ignored
    grep "Ignored 3 non-existent paths from the file list" sync.log || (echo "FAIL: expected 3 ignored paths" && cat sync.log && exit 1)

    # Verify existing files in the list were synced
    for name in file1 file2 file3 file10 file20 file30; do
        [ -f "/jfs/dst/$name" ] || (echo "FAIL: $name not synced" && exit 1)
    done

    # Verify files NOT in the list were NOT synced
    for name in file4 file5 file15; do
        [ ! -f "/jfs/dst/$name" ] || (echo "FAIL: $name should not be synced (not in files list)" && exit 1)
    done

    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    echo "PASS: test_sync_files_from_ignore_nonexistent_cluster"
}

skip_test_checkpoint_cluster_save_on_check_change_failure(){
    # Issue #6890: cluster sync with --check-change that fails should save checkpoint
    # Use long checkpoint-interval so only explicit save-on-failure creates the checkpoint.
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/data
    for i in $(seq 1 2000); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=64K count=1 status=none
    done
    chmod -R 777 /jfs/data
    (./mc rb myminio/data1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/data1
    # Background modifier: continuously append to source files via FUSE
    (while true; do
        for i in $(seq 1 300); do
            echo "m" >> /jfs/data/file$i 2>/dev/null
        done
    done) &
    modifier_pid=$!
    # Cluster sync with --check-change + long checkpoint interval → should fail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/data/ \
         minio://minioadmin:minioadmin@172.20.0.1:9000/data1/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change \
         --enable-checkpoint --checkpoint-interval 60s \
         > sync1.log 2>&1 || true
    kill $modifier_pid 2>/dev/null
    wait $modifier_pid 2>/dev/null || true
    # Verify check-change failure
    grep -i "changed during sync\|failed to handle" sync1.log || (echo "expected check-change failure" && exit 1)
    # Key assertion: checkpoint should be saved on failure (issue #6890)
    checkpoint_count=$(./mc find myminio/data1/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "FAIL: checkpoint should be saved when cluster sync fails (issue #6890)"
        exit 1
    fi
    # Resume (source no longer changing) should succeed
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/data/ \
         minio://minioadmin:minioadmin@172.20.0.1:9000/data1/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change \
         --enable-checkpoint --checkpoint-interval 2s \
         > sync2.log 2>&1
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
    ./mc rm -r --force myminio/data1
}

test_checkpoint_force_reset_cluster(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/src /jfs/dst
    for i in $(seq 1 800); do
        dd if=/dev/urandom of=/jfs/src/file$i bs=32K count=1 status=none
    done
    chmod -R 777 /jfs/src /jfs/dst
    timeout 2 sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs jfs://meta_url/src/ jfs://meta_url/dst/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --dirs --check-change $CLUSTER_CHECKPOINT_OPTS \
         --threads 2 >sync1.log 2>&1 || true
    checkpoint_count=$(find /jfs/dst -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "checkpoint file should exist after interrupted cluster sync"
        exit 1
    fi
    echo "force-reset-marker" > /jfs/src/force-reset-marker
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --mountpoint /jfs jfs://meta_url/src/ jfs://meta_url/dst/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --dirs --check-change $CLUSTER_CHECKPOINT_OPTS \
         --checkpoint-force-reset >sync2.log 2>&1
    grep "Force reset checkpoint, starting fresh" sync2.log || (echo "expected force reset log" && exit 1)
    diff -ur --no-dereference --exclude='.jfs.file*.tmp.*' /jfs/src /jfs/dst
    checkpoint_count_after=$(find /jfs/dst -maxdepth 1 -name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count_after" -ne 0 ]; then
        echo "checkpoint file should be deleted after successful force reset cluster sync"
        exit 1
    fi
    grep "panic:\|<FATAL>\|ERROR" sync2.log && echo "panic or fatal or ERROR in sync2.log" && exit 1 || true
}

source .github/scripts/common/run_test.sh && run_test $@
