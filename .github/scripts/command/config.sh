#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META minio
META_URL=$(get_meta_url $META)
[ ! -x mc ] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc && chmod +x mc

download_juicefs_client(){
    version=$1
    wget -q https://github.com/juicedata/juicefs/releases/download/v$version/juicefs-$version-linux-amd64.tar.gz
    tar -xzf juicefs-$version-linux-amd64.tar.gz -C /tmp/
    sudo cp /tmp/juicefs juicefs-$version
    ./juicefs-$version version
}

test_config_min_client_version()
{
    prepare_test
    download_juicefs_client 1.0.0
    ./juicefs format $META_URL myjfs
    ./juicefs-1.0.0 mount $META_URL /jfs -d && exit 1 || true
    ./juicefs config $META_URL --min-client-version 1.0.1
    ./juicefs-1.0.0 mount $META_URL /jfs -d && exit 1 || true
    ./juicefs config $META_URL --min-client-version 1.0.0
    ./juicefs-1.0.0 mount $META_URL /jfs -d
}

test_config_max_client_version()
{
    prepare_test
    current_version=$(./juicefs version | awk '{print $3}')
    download_juicefs_client 1.0.0
    ./juicefs-1.0.0 format $META_URL myjfs
    ./juicefs-1.0.0 config $META_URL --max-client-version 1.0.1
    ./juicefs mount $META_URL /jfs -d && exit 1 || true
    ./juicefs config $META_URL --max-client-version $current_version
    ./juicefs mount $META_URL /jfs -d
}

test_config_secret_key(){
    # # Consider command as failed when any component of the pipe fails:
    # https://stackoverflow.com/questions/1221833/pipe-output-and-capture-exit-status-in-bash
    prepare_test
    set -o pipefail
    ./mc config host add minio http://127.0.0.1:9000 minioadmin minioadmin
    ./mc admin user add minio juicedata juicedata
    ./mc admin policy attach minio consoleAdmin --user juicedata
    ./juicefs format --storage minio --bucket http://localhost:9000/jfs-test --access-key juicedata --secret-key juicedata $meta_url myjfs
    ./juicefs mount $META_URL /jfs -d --io-retries 1 --no-usage-report --heartbeat 3

    ./mc admin user remove minio juicedata
    ./mc admin user add minio juicedata1 juicedata1
    ./mc admin policy attach minio consoleAdmin --user juicedata1
    ./juicefs config $META_URL --access-key juicedata1 --secret-key juicedata1
    sleep 6
    echo abc | tee /jfs/abc.txt && echo "write success"
    cat /jfs/abc.txt | grep abc && echo "read success"
}
          

source .github/scripts/common/run_test.sh && run_test $@

