#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
META_URL2=$(get_meta_url2 $META)
[[ -z "$SEED" ]] && SEED=$(date +%s)
HEARTBEAT_INTERVAL=2
DIR_QUOTA_FLUSH_INTERVAL=4
# [[ -z "$SEED" ]] && SEED=1711594639
[[ -z "$BINARY" ]] && BINARY=false
[[ -z "$FAST" ]] && FAST=false

trap "echo random seed is $SEED" EXIT

if ! docker ps | grep -q minio; then
    docker run -d -p 9000:9000 --name minio \
            -e "MINIO_ACCESS_KEY=minioadmin" \
            -e "MINIO_SECRET_KEY=minioadmin" \
            -v /tmp/data:/data \
            -v /tmp/config:/root/.minio \
            minio/minio server /data
fi
[[ ! -f /usr/local/bin/mc ]] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc -O /usr/local/bin/mc && chmod +x /usr/local/bin/mc
sleep 3s
mc alias set myminio http://localhost:9000 minioadmin minioadmin
python3 -c "import xattr" || sudo pip install xattr

test_dump_load_big_dir(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    count=11000
    mkdir /jfs/test
    for i in $(seq 1 $count); do
        mkdir /jfs/test/dir$i
        echo "hello" > /jfs/test/dir$i/hello.txt
        ln -s /jfs/test/dir$i/hello.txt /jfs/test/dir$i/hello_link.txt
    done
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs
    ls /jfs/test | wc -l
}

test_dump_load_sustained_file(){
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount -d $META_URL /jfs
    file_count=1001
    for i in $(seq 1 $file_count); do
        touch /jfs/file$i
        exec {fd}<>/jfs/file$i
        echo fd is $fd
        fds[$i]=$fd
        rm /jfs/file$i
    done
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    for i in $(seq 1 $file_count); do
        fd=${fds[$i]}
        exec {fd}>&-
    done
    if [[ "$BINARY" == "true" ]]; then
        sustained=$(./juicefs load dump.json --binary --stat | grep sustained | awk -F"|" '{print $2}')
    else
        sustained=$(jq '.Sustained[].inodes | length' dump.json)
    fi
    echo "sustained file count: $sustained"
    [[ "$sustained" -eq "$file_count" ]] || (echo "sustained file count($sustained) should be $file_count" && exit 1)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs 
}

test_dump_load_with_copy_file_range(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/zero of=/tmp/test bs=1M count=4096
    cp /tmp/test /jfs/test
    node .github/scripts/copyFile.js /jfs/test /jfs/test1
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs
    compare_md5sum /tmp/test /jfs/test1
}

test_dump_load_with_clone(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/test bs=1M count=1024
    cp /tmp/test /jfs/test
    ./juicefs clone /jfs/test /jfs/test2
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs
    compare_md5sum /tmp/test /jfs/test2
}

test_dump_load_with_quota(){
    prepare_test
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs --heartbeat $HEARTBEAT_INTERVAL
    mkdir -p /jfs/d
    ./juicefs quota set $META_URL --path /d --inodes 1000 --capacity 1
    ./juicefs dump --log-level error $META_URL $(get_dump_option) > dump.json
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount $META_URL /jfs -d --heartbeat $HEARTBEAT_INTERVAL
    ./juicefs quota get $META_URL --path /d
    dd if=/dev/zero of=/jfs/d/test1 bs=1G count=1
    sleep $DIR_QUOTA_FLUSH_INTERVAL
    echo a | tee -a /jfs/d/test1 2>error.log && echo "write should fail on out of space" && exit 1 || true
    grep "Disk quota exceeded" error.log || (echo "grep failed" && exit 1)
}

test_dump_load_with_iflag(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt
    chattr +i /jfs/hello.txt
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt && echo "write should fail" && exit 1 || true
    chattr -i /jfs/hello.txt
    echo "world" > /jfs/hello.txt
    cat /jfs/hello.txt | grep world
}

test_dump_load_with_keep_secret_key()
{
    option=$@
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    ./juicefs dump --keep-secret-key $META_URL dump.json $(get_dump_option)
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs
    echo "hello" > /jfs/hello.txt
    cat /jfs/hello.txt | grep hello

    umount_jfs /jfs $META_URL
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json $(get_load_option)
    ./juicefs mount -d $META_URL /jfs && echo "mount should fail" && exit 1 || true
    ./juicefs config --secret-key minioadmin $META_URL
    ./juicefs mount -d $META_URL /jfs
    echo "hello" > /jfs/hello.txt
    cat /jfs/hello.txt | grep hello
}

test_dump_load_with_trash(){
    echo "TOOD"
}

test_dump_load_with_fsrand()
{
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0 --enable-acl
    ./juicefs mount -d $META_URL /jfs --enable-xattr
    SEED=$SEED LOG_LEVEL=WARNING MAX_EXAMPLE=30 STEP_COUNT=20 PROFILE=generate ROOT_DIR1=/jfs/fsrand ROOT_DIR2=/tmp/fsrand python3 .github/scripts/hypo/fs.py || true    
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    create_database $META_URL2
    ./juicefs load $META_URL2 dump.json $(get_load_option)
    ./juicefs dump $META_URL2 dump2.json $(get_dump_option)
    # if [[ "$BINARY" == "false" ]]; then
    #     compare_dump_json
    # fi
    ./juicefs mount -d $META_URL2 /jfs2
    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
    compare_stat_acl_xattr /jfs/fsrand /jfs2/fsrand
    umount_jfs /jfs2 $META_URL2
    ./juicefs status $META_URL2 && UUID=$(./juicefs status $META_URL2 | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --yes $META_URL2 $UUID
}

compare_dump_json(){
    cp dump.json dump.json.bak
    cp dump2.json dump2.json.bak
    sed -i '/usedSpace/d' dump*.json.bak
    sed -i '/usedInodes/d' dump*.json.bak
    sed -i '/nextInodes/d' dump*.json.bak
    sed -i '/nextChunk/d' dump*.json.bak
    sed -i '/nextTrash/d' dump*.json.bak
    sed -i '/nextSession/d' dump*.json.bak
    sed -i 's/"inode":[0-9]\+/"inode":0/g' dump*.json.bak
    diff -ur dump.json.bak dump2.json.bak
}

compare_stat_acl_xattr(){
    dir1=$1
    dir2=$2
    files1=($(find "$dir1" -type f -o -type d -exec stat -c "%n" {} + | sort))
    files2=($(find "$dir2" -type f -o -type d -exec stat -c "%n" {} + | sort))
    [[ ${#files1[@]} -ne ${#files2[@]} ]] && echo "compare_stat_acl: number of files differs" && exit 1
    for i in "${!files1[@]}"; do
        stat1=$(stat -c "%F %a %s %h %U %G" "${files1[$i]}")
        stat2=$(stat -c "%F %a %s %h %U %G" "${files2[$i]}")
        acl1=$(getfacl -p "${files1[$i]}" | tail -n +2)
        acl2=$(getfacl -p "${files2[$i]}" | tail -n +2)
        xattr1=$(getfattr -d -m . -e hex "${files1[$i]}" 2>/dev/null | tail -n +2 | sort)
        xattr2=$(getfattr -d -m . -e hex "${files2[$i]}" 2>/dev/null | tail -n +2 | sort)
        [[ "$stat1" != "$stat2" ]] && echo "compare_stat_acl: stat for ${files1[$i]} and ${files2[$i]} differs" && echo $stat1 && echo $stat2 && exit 1
        [[ "$acl1" != "$acl2" ]] && echo "compare_stat_acl: ACLs for ${files1[$i]} and ${files2[$i]} differs" && echo $acl1 && echo $acl2 && exit 1
        [[ "$xattr1" != "$xattr2" ]] && echo "compare_stat_acl: xattrs for ${files1[$i]} and ${files2[$i]} differs" && echo $xattr1 && echo $xattr2 && exit 1

    done
    echo "compare_stat_acl: ACLs and stats are the same"
}

test_load_encrypted_meta_backup()
{
    prepare_test
    [[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256 -passout pass:12345678 2048
    export JFS_RSA_PASSPHRASE=12345678
    ./juicefs format $META_URL myjfs --encrypt-rsa-key my-priv-key.pem
    ./juicefs mount -d $META_URL /jfs
    SEED=$SEED LOG_LEVEL=WARNING MAX_EXAMPLE=50 STEP_COUNT=50 PROFILE=generate ROOT_DIR1=/jfs/fsrand ROOT_DIR2=/tmp/fsrand python3 .github/scripts/hypo/fs.py || true
    umount /jfs
    SKIP_BACKUP_META_CHECK=true ./juicefs mount -d --backup-meta 10s $META_URL /jfs
    sleep 10s
    backup_file=$(ls -l /var/jfs/myjfs/meta/ |tail -1 | awk '{print $NF}')
    backup_path=/var/jfs/myjfs/meta/$backup_file
    ls -l $backup_path

    ./juicefs load sqlite3://test2.db $backup_path --encrypt-rsa-key my-priv-key.pem --encrypt-algo aes256gcm-rsa
    ./juicefs mount -d sqlite3://test2.db /jfs2
    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
    umount_jfs /jfs2 sqlite3://test2.db
    rm test2.db -rf
}


get_dump_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    elif [[ "$FAST" == "true" ]]; then
        option="--fast"
    else
        option=""
    fi
    echo $option
}

get_load_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    else
        option=""
    fi
    echo $option
}

prepare_test(){
    umount_jfs /jfs $META_URL
    umount_jfs /jfs2 sqlite3://test2.db
    python3 .github/scripts/flush_meta.py $META_URL
    rm test2.db -rf 
    rm -rf /var/jfs/myjfs || true
    mc rm --force --recursive myminio/test || true
}

source .github/scripts/common/run_test.sh && run_test $@
