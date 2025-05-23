#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$ENCRYPT" ]] && ENCRYPT=false
[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
FORMAT_OPTIONS=""
if [ "$ENCRYPT" == "true" ]; then
    export JFS_RSA_PASSPHRASE=the-passwd-for-rsa
    openssl genrsa -aes256 -passout pass:$JFS_RSA_PASSPHRASE -out my-priv-key.pem 2048
    FORMAT_OPTIONS="--encrypt-rsa-key my-priv-key.pem"
fi

generate_source_dir(){
    rm -rf jfs_source
    git clone https://github.com/juicedata/juicefs.git jfs_source --depth 1
    chmod 777 jfs_source
    mkdir jfs_source/empty_dir
    dd if=/dev/urandom of=jfs_source/file bs=5M count=1
    chmod 777 jfs_source/file
    ln -sf file jfs_source/symlink_to_file
    ln -f jfs_source/file jfs_source/hard_link_to_file
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch jfs_source/file2
    ln -s ../cmd jfs_source/pkg/symlink_to_cmd
}

generate_source_dir

generate_fsrand(){
    seed=$(date +%s)
    python3 .github/scripts/fsrand.py -a -c 2000 -s $seed  fsrand
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
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
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
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options --links

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference jfs_source/ /jfs/jfs_source/
}

test_sync_with_loop_link(){
    prepare_test
    options="--dirs --update --perms --check-all --list-threads 10 --list-depth 5"
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ln -s looplink jfs_source/looplink
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options  2>&1 | tee err.log || true
    grep -i "failed to handle 1 objects" err.log || (echo "grep failed" && exit 1)
    rm -rf jfs_source/looplink
}

test_sync_with_deep_link(){
    prepare_test
    options="--dirs --update --perms --check-all --list-threads 10 --list-depth 5"
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    touch jfs_source/symlink_1
    for i in {1..41}; do
        ln -s symlink_$i jfs_source/symlink_$((i+1))
    done
    ./juicefs sync jfs_source/ /jfs/jfs_source/ $options  2>&1 | tee err.log || true
    grep -i "failed to handle 1 objects" err.log || (echo "grep failed" && exit 1)
    rm -rf jfs_source/symlink_*
}

skip_test_sync_fsrand_with_mount_point(){
    generate_fsrand
    do_test_sync_fsrand_with_mount_point 
    do_test_sync_fsrand_with_mount_point --list-threads 10 --list-depth 5
    do_test_sync_fsrand_with_mount_point --dirs --update --perms --check-all 
    do_test_sync_fsrand_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_test_sync_fsrand_with_mount_point(){
    prepare_test
    options=$@
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync fsrand/ /jfs/fsrand/ $options --links

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    diff -ur --no-dereference fsrand/ /jfs/fsrand/
}

test_sync_include_exclude_option(){
    prepare_test
    ./juicefs format --trash-days 0 $FORMAT_OPTIONS $META_URL myjfs
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

test_sync_with_time(){
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf data/
    mkdir data
    echo "old" > data/file1
    echo "old" > data/file2
    echo "old" > data/file3
    sleep 1
    start_time=$(date "+%Y-%m-%d %H:%M:%S")
    sleep 1
    echo "new" > data/file2
    sleep 1
    mid_time=$(date "+%Y-%m-%d %H:%M:%S")
    sleep 1
    echo "new" > data/file3
    sleep 1
    end_time=$(date "+%Y-%m-%d %H:%M:%S")
    mkdir -p sync_dst1 sync_dst2
    ./juicefs sync --start-time "$start_time" data/ /jfs/sync_dst1/
    [ "$(cat /jfs/sync_dst1/file1 2>/dev/null)" = "" ] || (echo "file1 should not exist" && exit 1)
    [ "$(cat /jfs/sync_dst1/file2)" = "new" ] || (echo "file2 should be new" && exit 1)
    [ "$(cat /jfs/sync_dst1/file3)" = "new" ] || (echo "file3 should be new" && exit 1)
    ./juicefs sync --start-time "$start_time" --end-time "$mid_time" data/ /jfs/sync_dst2/
    [ "$(cat /jfs/sync_dst2/file1 2>/dev/null)" = "" ] || (echo "file1 should not exist" && exit 1)
    [ "$(cat /jfs/sync_dst2/file2)" = "new" ] || (echo "file2 should be new" && exit 1)
    [ "$(cat /jfs/sync_dst2/file3 2>/dev/null)" = "" ] || (echo "file3 should not exist" && exit 1)
}

test_sync_check_change()
{
    prepare_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf data/
    mkdir data
    nohup bash -c 'for i in `seq 1 1000000`; do echo $i >> data/echo; done' > /dev/null 2>&1 &
    pid=$!
    sleep 0.5
    ./juicefs sync --check-change data/ /jfs/data/ 2>&1 | grep "changed during sync" || (echo "should detect file changes during sync" && exit 1 )
    kill $pid || true
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
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
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
