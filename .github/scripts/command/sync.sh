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
    ./juicefs sync jfs_source/ minio://minioadmin:minioadmin@localhost:9000/jfs/ $options

    # if [[ ! "$options" =~ "--dirs" ]]; then
    #     find jfs_source -type d -empty -delete
    # fi
    # find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    # diff -ur --no-dereference jfs_source/ /jfs/jfs_source/
}

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


source .github/scripts/common/run_test.sh && run_test $@
