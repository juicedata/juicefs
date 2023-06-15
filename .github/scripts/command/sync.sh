#!/bin/bash

set -ex
python3 -c "import minio" || sudo pip install minio 
python3 -c "import hypothesis" || sudo pip install hypothesis
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)


test_sync_with_mount_point(){
    do_sync_with_mount_point 
    do_sync_with_mount_point --list-threads 10 --list-depth 5
    do_sync_with_mount_point --dirs --update --perms --check-all 
    do_sync_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

test_sync_without_mount_point(){
    do_sync_without_mount_point 
    do_sync_without_mount_point --list-threads 10 --list-depth 5
    do_sync_without_mount_point --dirs --update --perms --check-all 
    do_sync_without_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_sync_without_mount_point(){
    prepare_test
    options=$@
    generate_source_dir
    ./juicefs format $META_URL myjfs
    meta_url=$META_URL ./juicefs sync jfs_source/ jfs://meta_url/jfs_source/ $options --links

    ./juicefs mount -d $META_URL /jfs
    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference  jfs_source/ /jfs/jfs_source
}

do_sync_with_mount_point(){
    prepare_test
    options=$@
    generate_source_dir
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options --links

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference jfs_source/ /jfs/jfs_source/
}


generate_source_dir(){
    [[ ! -d jfs_source ]] && git clone https://github.com/juicedata/juicefs.git jfs_source
    [[ -d jfs_source/empty_dir ]] && rm jfs_source/empty_dir -rf
    chmod 777 jfs_source
    mkdir jfs_source/empty_dir
    dd if=/dev/urandom of=jfs_source/file bs=5M count=1
    chmod 777 jfs_source/file
    ln -sf file jfs_source/symlink_to_file
    ln -f jfs_source/file jfs_source/hard_link_to_file
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch jfs_source/file2
}

test_sync_randomly(){
    prepare_test
    [[ ! -d jfs_source ]] && git clone https://github.com/juicedata/juicefs.git jfs_source
    META_URL=$META_URL python3 .github/scripts/testSync.py
}

test_sync_include_exclude_option(){
    prepare_test
    ./juicefs format --trash-days 0 $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    ./juicefs sync jfs_source/ /jfs/
    for source_dir in "/jfs/" "jfs_source/" ; do 
        while IFS=, read -r jfs_option rsync_option status; do
            printf '\n%s, %s, %s\n' "$jfs_option" "$rsync_option" "$status"
            status=$(echo $status| xargs)
            if [[ -z "$status" || "$status" = "disable" ]]; then 
                continue
            fi
            if [ "$source_dir" == "/jfs/" ]; then 
                jfs_option="--exclude .stats --exclude .config $jfs_option " 
                rsync_option="--exclude .stats --exclude .config $rsync_option " 
            fi
            rm rsync_dir/ -rf && mkdir rsync_dir
            set -o noglob
            rsync -a $source_dir rsync_dir/ $rsync_option
            rm jfs_sync_dir/ -rf && mkdir jfs_sync_dir/
            ./juicefs sync $source_dir jfs_sync_dir/ $jfs_option --list-threads 2
            set -u noglob
            printf 'juicefs sync %s %s %s\n' "$source_dir"  "jfs_sync_dir/" "$jfs_option" 
            printf 'rsync %s %s %s\n' "$source_dir" "rsync_dir/"  "$rsync_option" 
            printf 'diff between juicefs sync and rsync:\n'
            diff -ur jfs_sync_dir rsync_dir
        done < .github/workflows/resources/sync-options.txt
    done
}

test_ignore_existing()
{
    prepare_test
    rm -rf /tmp/src_dir /tmp/rsync_dir /tmp/jfs_sync_dir
    mkdir -p /tmp/src_dir/d1
    mkdir -p /tmp/jfs_sync_dir/d1
    echo abc > /tmp/src_dir/file1
    echo 1234 > /tmp/jfs_sync_dir/file1
    echo abcde > /tmp/src_dir/d1/d1file1
    echo 123456 > /tmp/jfs_sync_dir/d1/d1file1
    cp -rf /tmp/jfs_sync_dir/ /tmp/rsync_dir
    
    mkdir /tmp/src_dir/no-exist-dir
    echo 1111 > /tmp/src_dir/no-exist-dir/f1
    echo 123456 > /tmp/src_dir/d1/no-exist-file

    ./juicefs sync /tmp/src_dir /tmp/jfs_sync_dir --existing
    rsync -r /tmp/src_dir/ /tmp/rsync_dir --existing --size-only
    diff -ur /tmp/jfs_sync_dir /tmp/rsync_dir
    
    rm -rf /tmp/src_dir /tmp/rsync_dir
    mkdir -p /tmp/src_dir/d1
    mkdir -p /tmp/jfs_sync_dir/d1
    echo abc > /tmp/src_dir/file1
    echo 1234 > /tmp/jfs_sync_dir/file1
    echo abcde > /tmp/src_dir/d1/d1file1
    echo 123456 > /tmp/jfs_sync_dir/d1/d1file1
    echo abc > /tmp/src_dir/file2
    echo abcde > /tmp/src_dir/d1/d1file2
    cp -rf /tmp/jfs_sync_dir/ /tmp/rsync_dir
    
    ./juicefs sync /tmp/src_dir /tmp/jfs_sync_dir --ignore-existing 
    rsync -r /tmp/src_dir/ /tmp/rsync_dir --ignore-existing --size-only
    diff -ur /tmp/jfs_sync_dir /tmp/rsync_dir
}
test_file_head(){
    # issue link: https://github.com/juicedata/juicefs/issues/2125
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    mkdir /jfs/jfs_source/
    [[ ! -d jfs_source ]] && git clone https://github.com/juicedata/juicefs.git jfs_source
    ./juicefs sync jfs_source/ /jfs/jfs_source/  --update --perms --check-all --bwlimit=81920 --dirs --threads=30 --list-threads=3 --debug
    echo "test" > jfs_source/test_file
    mkdir -p jfs_source/test_dir
    ./juicefs sync jfs_source/ /jfs/jfs_source/  --update --perms --check-all --bwlimit=81920 --dirs --threads=30 --list-threads=2 --debug
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur jfs_source/ /jfs/jfs_source
}

prepare_test()
{
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}

source .github/scripts/common/run_test.sh && run_test $@
