#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

rm ~/.ssh/myKey -rf 
ssh-keygen -t ed25519 -C "myKey" -f ~/.ssh/myKey -q -N ""
cp -f ~/.ssh/myKey.pub .github/scripts/ssh/myKey.pub
docker build -t juicedata/ssh -f .github/scripts/ssh/Dockerfile .github/scripts/ssh

docker compose -f .github/scripts/ssh/docker-compose.yml up -d

test_sync_with_mount_point(){
    do_sync_with_mount_point 
    do_sync_with_mount_point --list-threads 10 --list-depth 5
    do_sync_with_mount_point --dirs --update --perms --check-all 
    do_sync_with_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
}

do_sync_with_mount_point(){
    prepare_test
    options=$@
    generate_source_dir
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync jfs_source/ /jfs/jfs_source/ --manager 172.20.0.1 --worker 172.20.0.2,172.20.0.3

    if [[ ! "$options" =~ "--dirs" ]]; then
        find jfs_source -type d -empty -delete
    fi
    find /jfs/jfs_source -type f -name ".*.tmp*" -delete
    diff -ur --no-dereference jfs_source/ /jfs/jfs_source/
}

test_sync_without_mount_point(){
    exit 0
    # do_sync_without_mount_point 
    # do_sync_without_mount_point --list-threads 10 --list-depth 5
    # do_sync_without_mount_point --dirs --update --perms --check-all 
    # do_sync_without_mount_point --dirs --update --perms --check-all --list-threads 10 --list-depth 5
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


source .github/scripts/common/run_test.sh && run_test $@
