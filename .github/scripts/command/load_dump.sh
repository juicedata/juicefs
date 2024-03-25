#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
[[ -z "$SEED" ]] && SEED=$(date +%s)
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

test_dump_load_with_iflag(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt
    chattr +i /jfs/hello.txt
    ./juicefs dump $META_URL dump.json --fast
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt && echo "write should fail" && exit 1 || true
    chattr -i /jfs/hello.txt
    echo "world" > /jfs/hello.txt
    cat /jfs/hello.txt | grep world
}

test_dump_with_keep_secret()
{
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    ./juicefs dump --keep-secret-key $META_URL dump.json --fast
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount -d $META_URL /jfs
    echo "hello" > /jfs/hello.txt
    cat /jfs/hello.txt | grep hello
}

test_dump_without_keep_secret()
{
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    ./juicefs dump $META_URL dump.json --fast
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount -d $META_URL /jfs && echo "mount should fail" && exit 1 || true
    ./juicefs config --secret-key minioadmin $META_URL
    ./juicefs mount -d $META_URL /jfs
    echo "hello" > /jfs/hello.txt
    cat /jfs/hello.txt | grep hello
}

test_dump_load_with_fsrand1(){
    dump_load_with_fsrand
}

test_dump_load_with_fsrand2(){
    dump_load_with_fsrand --skip-trash
}

test_dump_load_with_fsrand3(){
    dump_load_with_fsrand --fast
}

test_dump_load_with_fsrand4(){
    dump_load_with_fsrand --fast --skip-trash
}

dump_load_with_fsrand()
{
    option=$@
    echo option is $option
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 1 --enable-acl
    ./juicefs mount -d $META_URL /jfs --enable-xattr
    SEED=$SEED MAX_EXAMPLE=100 STEP_COUNT=50 PROFILE=generate ROOT_DIR1=/jfs/fsrand ROOT_DIR2=/tmp/fsrand python3 .github/scripts/hypo/fsrand2.py || true
    ./juicefs dump $META_URL dump.json --fast
    ./juicefs load sqlite3://test2.db dump.json
    ./juicefs dump sqlite3://test2.db dump2.json --fast
    compare_dump_json
    ./juicefs mount -d sqlite3://test2.db /jfs2
    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
    compare_stat_acl /jfs/fsrand /jfs2/fsrand
}

compare_dump_json(){
    sed -i '/usedSpace/d' dump*.json
    sed -i '/usedInodes/d' dump*.json
    sed -i '/nextInodes/d' dump*.json
    sed -i '/nextChunk/d' dump*.json
    sed -i '/nextSession/d' dump*.json
    sed -i 's/"inode":[0-9]\+/"inode":0/g' dump*.json
    diff -ur dump.json dump2.json
}

compare_stat_acl(){
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
        [[ "$stat1" != "$stat2" ]] && echo "compare_stat_acl: stat for ${files1[$i]} and ${files2[$i]} differs" && echo $stat1 && echo $stat2 && exit 1
        [[ "$acl1" != "$acl2" ]] && echo "compare_stat_acl: ACLs for ${files1[$i]} and ${files2[$i]} differs" && echo $acl1 && echo $acl2 && exit 1
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
    SEED=$SEED MAX_EXAMPLE=50 STEP_COUNT=50 PROFILE=generate ROOT_DIR1=/jfs/fsrand ROOT_DIR2=/tmp/fsrand python3 .github/scripts/hypo/fsrand2.py || true
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

prepare_test(){
    umount_jfs /jfs $META_URL
    umount_jfs /jfs2 sqlite3://test2.db
    python3 .github/scripts/flush_meta.py $META_URL
    rm test2.db -rf 
    rm -rf /var/jfs/myjfs || true
    mc rm --force --recursive myminio/test || true
}

source .github/scripts/common/run_test.sh && run_test $@
