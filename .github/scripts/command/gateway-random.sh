#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$SUBDIR" ]] && SUBDIR=false
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
[[ ! -x /usr/local/bin/mc ]] && wget -q https://dl.min.io/client/mc/release/linux-amd64/archive/mc.RELEASE.2021-04-22T17-40-00Z -O /usr/local/bin/mc && sudo chmod +x /usr/local/bin/mc
# docker ps -aq --filter "status=exited" --filter "name=minio_old" | xargs -r docker rm -v
if ! docker ps --filter "name=minio_old$" | grep minio_old; then
    echo start minio_old
    docker run -d -p 9000:9000 --name minio_old -e "MINIO_ACCESS_KEY=minioadmin" -e "MINIO_SECRET_KEY=minioadmin" minio/minio:RELEASE.2021-04-22T15-44-28Z server /tmp/minio_old
    while ! curl -s http://localhost:9000/minio/health/live > /dev/null; do
        echo "Waiting for MinIO to be ready..."
        sleep 1
    done
    echo "MinIO is ready."
fi

timeout 30 bash -c 'counter=0; until lsof -i:9000; do echo -ne "wait port ready in $counter\r" && ((counter++)) && sleep 1; done'

[[ -n $CI ]] && trap 'kill_gateway 9005;' EXIT
kill_gateway() {
    port=$1
    lsof -i:$port || true
    lsof -t -i :$port | xargs -r kill -9 || true
}

prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    kill_gateway 9005
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    ./juicefs format $META_URL myjfs  --trash-days 0
    ./juicefs mount -d $META_URL /tmp/jfs
    if [ "$SUBDIR" = true ]; then
        echo "start gateway with subdir"
        mkdir /tmp/jfs/subdir
        MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway \
            $META_URL localhost:9005 --multi-buckets --keep-etag -d --subdir /subdir
    else
        MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway \
            $META_URL localhost:9005 --multi-buckets --keep-etag -d
    fi
}

test_run_example()
{
    prepare_test
    python3 .github/scripts/hypo/s3_test.py
}

test_run_all()
{
    prepare_test
    python3 .github/scripts/hypo/s3.py
}




source .github/scripts/common/run_test.sh && run_test $@

