#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
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
    # ./mc alias set myminio http://localhost:9000 minioadmin minioadmin
    ./mc config host add myminio http://127.0.0.1:9000 minioadmin minioadmin
    if ./mc ls myminio/jfs; then
        ./mc rb --force myminio/jfs
    fi
    ./mc mb myminio/jfs
}
start_minio

test_sync_big_file(){
    prepare_test
    start_minio
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cp mymino/myjfs/bigfile /tmp/bigfile2
    diff /tmp/bigfile /tmp/bigfile2
}

test_sync_small_files(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest /test --dirs 10 --depth 3 --files 10 --threads 10
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ --list-threads 100 --list-depth 10
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ $count1 -eq $count2 ]
}
test_sync_with_limit(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    ./juicefs mdtest /test --dirs 10 --depth 2 --files 10 --threads 10
    ./juicefs sync --limit 1000 minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    count=$(./mc ls myminio/myjfs -r | wc -l)
    echo count is $count
    [ $count -eq 1000 ]
}
test_sync_with_existing(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    echo abc > /jfs/abc
    ./juicefs sync --existing minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc ls myminio/myjfs/abc && echo "myminio/myjfs/abc should not exist" && exit 1 || true
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc ls myminio/myjfs/abc
}
test_sync_with_update(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    echo def > def
    ./mc cp def myminio/myjfs/abc
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep def
}

test_sync_with_force_update(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    echo def > def
    ./mc cp def myminio/myjfs/abc
    ./juicefs sync --force-update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cat myminio/myjfs/abc | grep def
}

test_sync_external_link(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    touch hello
    ln -s $(realpath hello) /jfs/hello
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ myjfs/
    # ./mc ls minio/myjfs | grep hello
}

source .github/scripts/common/run_test.sh && run_test $@
