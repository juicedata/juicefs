#!/bin/bash -e
source .github/scripts/common/common.sh
[[ -z "$CI" ]] && CI=false
[[ -z "$META" ]] && META=redis
[[ -z "$KEY_TYPE" ]] && KEY_TYPE=ed25519
[[ -z "$FILE_COUNT" ]] && FILE_COUNT=600
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
dpkg -s gawk || .github/scripts/apt_install.sh gawk
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
         >sync.log 2>&1
    set +o pipefail
    diff data/ /jfs/data/
    set -o pipefail
    sudo -u juicedata meta_url=$META_URL ./juicefs sync  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --delete-src --dirs --check-change \
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
    ./random-test runOp -baseDir /jfs/test -files 100000 -ops 1000000 -threads 50 -dirSize 100 -duration 30s -createOp 30,uniform \
    -deleteOp 5,end --linkOp 10,uniform --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform
    chmod -R 777 /jfs/test
    chmod -R 777 /jfs/test2
    sudo -u juicedata meta_url=$META_URL ./juicefs sync jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-new --links --dirs --start-time "$current_time" \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --delete-src --match-full-path jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-all --links --start-time 2199-12-30 \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true 
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --delete-src --match-full-path jfs://meta_url/test/ jfs://meta_url/test2/ \
         --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 --dirs \
         --list-threads 10 --list-depth 5 --check-all --links --start-time "$current_time" \
         >sync.log 2>&1
    grep "panic:\|<FATAL>\|ERROR" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    [ -z "$(ls -A /jfs/test)" ] || exit 1
    rm -rf empty || mkdir empty
    sudo -u juicedata meta_url=$META_URL ./juicefs sync --delete-dst --match-full-path  --include='*' \
         ./empty/ jfs://meta_url/test2/ --manager-addr 172.20.0.1:8081 --worker juicedata@172.20.0.2,juicedata@172.20.0.3 \
         --list-threads 10 --list-depth 5 --check-change --dirs --links --start-time "$current_time" \
         >sync.log 2>&1
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
         >sync.log 2>&1
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
    count2=$(cat sync.log | grep 172.20.0.2 | grep "receive stats" | gawk '{sum += gensub(/.*Copied:([0-9]+).*/, "\\1", "g");} END {print sum;}')
    [ -z "$count2" ] && count2=0
    count3=$(cat sync.log | grep 172.20.0.3 | grep "receive stats" | gawk '{sum += gensub(/.*Copied:([0-9]+).*/, "\\1", "g");} END {print sum;}')
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


source .github/scripts/common/run_test.sh && run_test $@
