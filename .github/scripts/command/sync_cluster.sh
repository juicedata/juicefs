#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

rm ~/.ssh/id_rsa -rf 
rm ~/.ssh/id_rsa.pub -rf 
ssh-keygen -t ed25519 -C "default" -f ~/.ssh/id_rsa -q -N ""
cp -f ~/.ssh/id_rsa.pub .github/scripts/ssh/id_rsa.pub
docker build -t juicedata/ssh -f .github/scripts/ssh/Dockerfile .github/scripts/ssh

docker compose -f .github/scripts/ssh/docker-compose.yml up -d

test_sync_with_mount_point(){
    do_sync_with_mount_point 
    # do_sync_with_mount_point --list-threads 10 --list-depth 5
    # do_sync_with_mount_point --dirs --update --perms --check-all 
    # do_sync_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_sync_with_mount_point(){
    prepare_test
    options=$@
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ \
         minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --manager 172.20.0.1:8081 --worker 172.20.0.2,172.20.0.3
    mc ls myminio/myjfs -r
}

test_sync_without_mount_point(){
    exit 0
    # do_sync_without_mount_point 
    # do_sync_without_mount_point --list-threads 10 --list-depth 5
    # do_sync_without_mount_point --dirs --update --perms --check-all 
    # do_sync_without_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_sync_without_mount_point(){
    prepare_test
    options=$@
    generate_source_dir
    ./juicefs format $META_URL myjfs
    meta_url=$META_URL ./juicefs sync jfs_source/ jfs://meta_url/jfs_source/ $options --links

    ./juicefs mount -d $META_URL /jfs
    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference  jfs_source/ /jfs/jfs_source
}

prepare_test(){
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
    rm -rf /var/jfsCache/myjfs
    (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
}


source .github/scripts/common/run_test.sh && run_test $@
