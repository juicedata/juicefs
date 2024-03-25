#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_mount_process_exit_on_format()
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


source .github/scripts/common/run_test.sh && run_test $@

