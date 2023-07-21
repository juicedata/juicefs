#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=redis
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
    # ./mc alias set myminio http://localhost:9000 minioadmin minioadmin
    ./mc config host add myminio http://127.0.0.1:9000 minioadmin minioadmin
}
start_minio
restore_key(){
    exit=$?
    echo restore key on clean up
    [ -f ~/.ssh/id_rsa.bak ] && mv -f ~/.ssh/id_rsa.bak ~/.ssh/id_rsa
    [ -f ~/.ssh/id_rsa.pub.bak ] && mv -f ~/.ssh/id_rsa.pub.bak ~/.ssh/id_rsa.pub 
    exit $exit
}
# trap restore_key EXIT
[ -f ~/.ssh/id_rsa ] && mv -f ~/.ssh/id_rsa ~/.ssh/id_rsa.bak
[ -f ~/.ssh/id_rsa.pub ] && mv -f ~/.ssh/id_rsa.pub ~/.ssh/id_rsa.pub.bak 
ssh-keygen -t ed25519 -C "default" -f ~/.ssh/id_rsa -q -N ""
cp -f ~/.ssh/id_rsa.pub .github/scripts/ssh/id_rsa.pub
diff ~/.ssh/id_rsa.pub .github/scripts/ssh/id_rsa.pub
docker build -t juicedata/ssh -f .github/scripts/ssh/Dockerfile .github/scripts/ssh
docker rm worker1 worker2 -f
docker compose -f .github/scripts/ssh/docker-compose.yml up -d

test_sync_small_files_without_mount_point(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    file_count=600
    mkdir -p /jfs/data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=1M count=5 status=none
    done
    (./mc rb myminio/data1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/data1
    redis-cli config set protected-mode no
    meta_url=$(echo $META_URL | sed 's/127\.0\.0\.1/172.20.0.1/g')
    meta_url=$meta_url ./juicefs sync -v jfs://meta_url/data/ minio://minioadmin:minioadmin@172.20.0.1:9000/data1/ \
         --manager 172.20.0.1:8081 --worker sshuser@172.20.0.2,sshuser@172.20.0.3 \
         --list-threads 10 --list-depth 5\
         2>&1 | tee sync.log
    # diff data/ /jfs/data1/
    check_sync_log $file_count
}

skip_test_sync_small_files_without_mount_point2(){
    prepare_test
    file_count=600
    mkdir -p data
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=data/file$i bs=1M count=5 status=none
    done
    (./mc rb myminio/data > /dev/null 2>&1 --force || true) && ./mc mb myminio/data
    ./mc cp -r data myminio/data
    (./mc rb myminio/data1 > /dev/null 2>&1 --force || true) && ./mc mb myminio/data1
    redis-cli config set protected-mode no
    meta_url=$(echo $META_URL | sed 's/127\.0\.0\.1/172.20.0.1/g')
    meta_url=$meta_url ./juicefs sync -v  minio://minioadmin:minioadmin@172.20.0.1:9000/data/ jfs://meta_url/ \
         --manager 172.20.0.1:8081 --worker sshuser@172.20.0.2,sshuser@172.20.0.3 \
         --list-threads 10 --list-depth 5\
         2>&1 | tee sync.log
    diff data/ /jfs/data1/
    check_sync_log $file_count
}

test_sync_small_files(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/test
    file_count=600
    for i in $(seq 1 $file_count); do
        dd if=/dev/urandom of=/jfs/file$i bs=1M count=5 status=none
    done
    start_gateway
    ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9005/myjfs/ \
         minio://minioadmin:minioadmin@172.20.0.1:9000/myjfs/ \
        --manager 172.20.0.1:8081 --worker sshuser@172.20.0.2,sshuser@172.20.0.3 \
        --list-threads 10 --list-depth 5 \
        2>&1 | tee sync.log
    count1=$(./mc ls myminio/myjfs/test -r |wc -l)
    count2=$(./mc ls juicegw/myjfs/test -r | awk '$4=="5MiB"' | wc -l)
    if [ "$count1" != "$count2" ]; then
        echo "count not equal, $count1, $count2"
        exit 1
    fi
    check_sync_log $file_count
}


test_sync_big_file(){
    prepare_test
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    start_gateway
    ./juicefs sync -v minio://minioadmin:minioadmin@172.20.0.1:9005/myjfs/ \
         minio://minioadmin:minioadmin@172.20.0.1:9000/myjfs/ \
        --manager 172.20.0.1:8081 --worker sshuser@172.20.0.2,sshuser@172.20.0.3 \
        2>&1 | tee sync.log
    md51=$(./mc cat myminio/myjfs/bigfile | md5sum)
    md52=$(cat /tmp/bigfile | md5sum)
    if [ "$md51" != "$md52" ]; then
        echo "md5sum not equal, $md51, $md52"
        exit 1
    fi
}

check_sync_log(){
    grep "<FATAL>" sync.log && exit 1 || true
    file_count=$1
    file_copied=$(tail -1 sync.log  | sed 's/.*copied: \([0-9]*\).*/\1/' )
    if [ "$file_copied" != "$file_count" ]; then
        echo "file_copied not equal, $file_copied, $file_count"
        exit 1
    fi
    count2=$(cat sync.log | grep 172.20.0.2 | grep "receive stats" | gawk '{sum += gensub(/.*Copied:([0-9]+).*/, "\\1", "g");} END {print sum;}')
    count3=$(cat sync.log | grep 172.20.0.3 | grep "receive stats" | gawk '{sum += gensub(/.*Copied:([0-9]+).*/, "\\1", "g");} END {print sum;}')
    count1=$((file_count - count2 - count3))
    echo "count1, $count1, count2, $count2, count3, $count3"
    min_count=$((file_count / 6))
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
