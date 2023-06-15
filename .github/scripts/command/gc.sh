#!/bin/bash
set -e

python3 -c "import xattr" || sudo pip install xattr 
sudo dpkg -s redis-tools || sudo .github/scripts/apt_install.sh redis-tools
sudo dpkg -s attr || sudo .github/scripts/apt_install.sh fio

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

prepare_test()
{
    umount_jfs /jfs $META_URL
    ls -l /jfs/.config && exit 1 || true
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}

source .github/scripts/common/run_test.sh && run_test $@
