#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

skip_test_mount_process_exit_on_format()
{
    prepare_test
    echo "round $i"
    ./juicefs format $META_URL volume-$i
    ./juicefs mount -d $META_URL /tmp/myjfs$i_$j --no-usage-report
    cd /tmp/myjfs$i_$j
    bash -c 'for k in {1..300}; do echo abc>$k; sleep 0.2; done' || true & 
    cd -
    sleep 3
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid
    ./juicefs format $META_URL new-volume-$i 
    sleep 15   
    ps -ef | grep juicefs
    # TODO: fix the bug and remove the following line
    # SEE https://github.com/juicedata/juicefs/issues/4534
    pidof juicefs && exit 1
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid
}

test_format_sftp_object()
{
    docker run -d --name sftp -p 2222:22 juicedata/ci-sftp
    prepare_test
    CONTAINER_IP=$(docker container inspect sftp --format '{{ .NetworkSettings.IPAddress }}')
    echo "round $i"
    ./juicefs format $META_URL volume-$i --storage sftp \
    --bucket $CONTAINER_IP:myjfs/ \
    --access-key testUser1 \
    --secret-key password
    ./juicefs mount -d $META_URL /tmp/jfs --no-usage-report --cache-size 0
    cd /tmp/jfs
    bash -c 'for k in {1..100}; do echo abc>$k; sleep 0.1; done' || true &
    bg_pid=$!
    cd -
    sleep 1
    docker stop sftp
    sleep 10
    docker start sftp
    sleep 2
    wait $bg_pid
    echo "Checking JuiceFS read/write"
    echo abc > /tmp/jfs/101
    for k in {1..100}; do
        if [[ $(cat /tmp/jfs/$k) != "abc" ]]; then
            echo "ERROR: File $k corrupted after SFTP restart!"
            exit 1
        fi
    done
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    ./juicefs format $META_URL new-volume-$i
}

source .github/scripts/common/run_test.sh && run_test $@

