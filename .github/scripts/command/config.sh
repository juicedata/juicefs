#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

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


source .github/scripts/common/run_test.sh && run_test $@

