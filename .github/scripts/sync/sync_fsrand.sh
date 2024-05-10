#!/bin/bash -e
source .github/scripts/common/common.sh
[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

[[ -z "$SEED" ]] && SEED=$(date +%s)
[[ -z "$DERANDOMIZE" ]] && DERANDOMIZE=false
[[ -z "$MAX_EXAMPLE" ]] && MAX_EXAMPLE=100
[[ -z "$GOCOVERDIR" ]] && GOCOVERDIR=/tmp/cover
[[ -z "$USER" ]] && USER=root
if [ ! -d "$GOCOVERDIR" ]; then
    mkdir -p $GOCOVERDIR
fi
trap "echo random seed is $SEED" EXIT
SOURCE_DIR1=/tmp/fsrand1/
SOURCE_DIR2=/tmp/fsrand2/
DEST_DIR1=/tmp/jfs/fsrand1/
DEST_DIR2=/tmp/jfs/fsrand2/

rm $SOURCE_DIR1 -rf && sudo -u $USER mkdir $SOURCE_DIR1
rm $SOURCE_DIR2 -rf && sudo -u $USER mkdir $SOURCE_DIR2
EXCLUDE_RULES="utime"
PROFILE=generate EXCLUDE_RULES=$EXCLUDE_RULES MAX_EXAMPLE=$MAX_EXAMPLE SEED=$SEED ROOT_DIR1=$SOURCE_DIR1 ROOT_DIR2=$SOURCE_DIR2 python3 .github/scripts/hypo/fs.py || true
prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
    ./juicefs format $META_URL myjfs
}

test_cmp_cp(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option="--dirs --perms --check-all --links --list-threads 10 --list-depth 5"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option 2>&1| tee sync.log || true
    do_copy $sync_option
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_cmp_cp_without_perms(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option="--dirs --check-all --links --list-threads 10 --list-depth 5"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option 2>&1| tee sync.log || true
    do_copy $sync_option
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_cmp_cp_without_links(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option="--dirs --check-all --perms --list-threads 10 --list-depth 5"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option 2>&1| tee sync.log || true
    do_copy $sync_option
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_no_mount_point(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option="--dirs --perms --check-all --links --list-threads 10 --list-depth 5"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option 2>&1| tee sync1.log || true
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR meta_url=$META_URL ./juicefs sync -v $SOURCE_DIR1 jfs://meta_url/fsrand2/ $sync_option 2>&1| tee sync2.log || true
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_inplace(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option1="--dirs --perms --check-all --links --list-threads 10 --list-depth 5"
    sync_option2="--dirs --perms --check-all --links --list-threads 10 --list-depth 5 --inplace"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR meta_url=$META_URL ./juicefs sync -v $SOURCE_DIR1 jfs://meta_url/fsrand1/ $sync_option1 2>&1| tee sync1.log || true
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR meta_url=$META_URL ./juicefs sync -v $SOURCE_DIR1 jfs://meta_url/fsrand2/ $sync_option2 2>&1| tee sync2.log || true
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_list_threads(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option1="--dirs --perms --check-all --links --list-threads 10 --list-depth 5"
    sync_option2="--dirs --perms --check-all --links"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option1 2>&1| tee sync1.log || true
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR2 $sync_option2 2>&1| tee sync2.log || true
    check_diff $DEST_DIR1 $DEST_DIR2
}

test_update(){
    prepare_test
    ./juicefs mount $META_URL /tmp/jfs -d
    sync_option="--dirs --perms --check-all --links --list-threads 10 --list-depth 5"
    sudo -u $USER GOCOVERDIR=$GOCOVERDIR ./juicefs sync -v $SOURCE_DIR1 $DEST_DIR1 $sync_option 2>&1| tee sync.log || true
    do_copy $sync_option
    check_diff $DEST_DIR1 $DEST_DIR2
    
    sudo -u $USER PROFILE=generate EXCLUDE_RULES=$EXCLUDE_RULES MAX_EXAMPLE=$MAX_EXAMPLE SEED=$SEED ROOT_DIR1=$SOURCE_DIR1 ROOT_DIR2=$SOURCE_DIR2 python3 .github/scripts/hypo/fs.py || true
    # chmod 777 $SOURCE_DIR1
    # chmod 777 $SOURCE_DIR2
    do_copy $sync_option
    for i in {1..5}; do
        sync_option+=" --update --delete-dst"
        echo sudo -u $USER GOCOVERDIR=$GOCOVERDIR meta_url=$META_URL ./juicefs sync $SOURCE_DIR1 jfs://meta_url/fsrand1/ $sync_option
        sudo -u $USER GOCOVERDIR=$GOCOVERDIR meta_url=$META_URL ./juicefs sync $SOURCE_DIR1 jfs://meta_url/fsrand1/ $sync_option 2>&1| tee sync.log || true
        if grep -q "Failed to delete" sync.log; then
            echo "failed to delete, retry sync"
        else
            echo "sync delete success"
            break
        fi
    done
    diff -ur --no-dereference $DEST_DIR1 $DEST_DIR2
}

do_copy(){
    local sync_option=$@
    local preserve="timestamps"
    local no_preserve=""
    if [[ "$sync_option" =~ "--perms" ]]; then
        preserve+=",mode,ownership"
    else
        no_preserve+="mode,ownership"
    fi
    if [[ "$sync_option" =~ "--links" ]]; then
       preserve+=",links"
    fi
    local cp_option="--recursive --preserve=$preserve"
    if [[ -n "$no_preserve" ]]; then
        cp_option+=" --no-preserve=$no_preserve"
    fi
    if [[ "$sync_option" =~ "--links" ]]; then
        cp_option+=" --no-dereference"
    else
        cp_option+=" --dereference"
    fi
    rm -rf $DEST_DIR2 
    sudo -u $USER cp  $SOURCE_DIR1 $DEST_DIR2 $cp_option || true
    echo sudo -u $USER cp  $SOURCE_DIR1 $DEST_DIR2 $cp_option
}

check_diff(){
    local dir1=$1
    local dir2=$2
    diff -ur --no-dereference $dir1 $dir2
    pushd . && diff <(cd $dir1 && find . -printf "%p:%m:%u:%g:%y\n" | sort) <(cd $dir2 && find . -printf "%p:%m:%u:%g:%y\n" | sort) && popd
    if [ $? -ne 0 ]; then
        echo "permission or owner or group not equal"
        exit 1
    fi
    # pushd . && diff <(cd $dir1 && find . ! -type d -printf "%p:%.23T+\n" | sort) <(cd $dir2 && find . ! -type d -printf "%p:%.23T+\n" | sort) && popd
    # if [ $? -ne 0 ]; then
    #     echo "mtime not equal"
    #     exit 1
    # fi
    # TODO: uncomment this after xattr is supported
    # pushd . && diff <(cd $dir1 && find . -exec getfattr -dm- {} + | sort) <(cd $dir2 && find . -exec getfattr -dm- {} + | sort) && popd
    # if [ $? -ne 0 ]; then
    #     echo "xattr not equal"
    #     exit 1
    # fi
    echo "check diff success"
}


source .github/scripts/common/run_test.sh && run_test $@