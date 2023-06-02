
#!/bin/bash
set -e

sudo dpkg -s redis-tools || sudo apt install redis-tools
sudo dpkg -s attr || sudo apt install fio

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_gc_trash_slices(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    PATH1=/tmp/test PATH2=/jfs/test python3 .github/scripts/random_read_write.py 
    ./juicefs status --more $META_URL
    ./juicefs config $META_URL --trash-days 0 --yes
    ./juicefs gc $META_URL 
    ./juicefs gc $META_URL --delete
    ./juicefs status --more $META_URL
}

test_gc_trash_files(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand
    rm -rf /jfs/fsrand
    ./juicefs status --more $META_URL
    ./juicefs config $META_URL --trash-days 0 --yes
    ./juicefs gc $META_URL 
    ./juicefs gc $META_URL --delete
    ./juicefs status --more $META_URL
}

skip_test_delete_compact(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 1
    ./juicefs mount -d $META_URL /jfs
    fio --name=abc --rw=randwrite --refill_buffers --size=2G --bs=256k --directory=/jfs
    redis-cli save
    # don't skip files when gc compact
    export JFS_SKIPPED_TIME=1
    ./juicefs gc --compact --delete $META_URL
    container_id=$(docker ps -a | grep redis | awk '{print $1}')
    sudo killall -9 redis-server
    sudo docker restart $container_id
    sleep 3
    ./juicefs fsck $meta
}
          

prepare_test()
{
    umount_jfs /jfs $META_URL
    ls -l /jfs/.config && exit 1 || true
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

