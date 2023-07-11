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

test_sync_with_object_storage(){
    do_sync_with_object_storage
}

do_sync_with_object_storage(){
    prepare_test
    options=$@
    generate_source_dir
    start_minio
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ln -s go.mod /jfs/go.mod
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005
    ./mc alias set minio http://localhost:9005 minioadmin minioadmin --api S3v4
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ myjfs/
    ./mc ls minio/myjfs | grep go.mod
}

source .github/scripts/common/run_test.sh && run_test $@
