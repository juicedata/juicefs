#!/bin/bash
set -e

python3 -c "import minio" || sudo pip install minio 
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

if ! docker ps | grep -q minio; then
    docker run -d -p 9000:9000 --name minio \
            -e "MINIO_ACCESS_KEY=minioadmin" \
            -e "MINIO_SECRET_KEY=minioadmin" \
            -v /tmp/data:/data \
            -v /tmp/config:/root/.minio \
            minio/minio server /data
fi
[[ ! -f /usr/local/bin/mc ]] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc -O /usr/local/bin/mc && chmod +x /usr/local/bin/mc
mc alias set myminio http://localhost:9000 minioadmin minioadmin
python3 -c "import xattr" || sudo pip install xattr

test_dump_load_with_iflag(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt
    chattr +i /jfs/hello.txt
    ./juicefs dump $META_URL dump.json
    ./juicefs load sqlite3://test2.db dump.json
    ./juicefs mount -d sqlite3://test2.db /jfs2 --enable-ioctl
    echo "hello" > /jfs2/hello.txt && echo "write should fail" && exit 1 || true
    chattr -i /jfs2/hello.txt
    echo "world" > /jfs2/hello.txt
    cat /jfs2/hello.txt | grep world
}

test_dump_with_keep_secret()
{
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    ./juicefs dump --keep-secret-key $META_URL dump.json
    ./juicefs load sqlite3://test2.db dump.json
    ./juicefs mount -d sqlite3://test2.db /jfs2
    echo "hello" > /jfs2/hello.txt
    cat /jfs2/hello.txt | grep hello
}

test_dump_without_keep_secret()
{
    prepare_test
    ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/test --access-key minioadmin --secret-key minioadmin
    ./juicefs dump $META_URL dump.json
    ./juicefs load sqlite3://test2.db dump.json
    ./juicefs mount -d sqlite3://test2.db /jfs2 && echo "mount should fail" && exit 1 || true
    ./juicefs config --secret-key minioadmin sqlite3://test2.db
    ./juicefs mount -d sqlite3://test2.db /jfs2
    echo "hello" > /jfs2/hello.txt
    cat /jfs2/hello.txt | grep hello
}

test_dump_load()
{
    prepare_test
    do_dump_load dump.json
}

test_dump_load_gzip()
{
    prepare_test
    do_dump_load dump.json.gz
}

test_load_encrypted_meta_backup()
{
    prepare_test
    [[ ! -f my-priv-key.pem ]] && openssl genrsa -out my-priv-key.pem -aes256 -passout pass:12345678 2048
    export JFS_RSA_PASSPHRASE=12345678
    ./juicefs format $META_URL myjfs --encrypt-rsa-key my-priv-key.pem
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand -v -a
    umount /jfs
    SKIP_BACKUP_META_CHECK=true ./juicefs mount -d --backup-meta 10s $META_URL /jfs
    sleep 10s
    backup_file=$(ls -l /var/jfs/myjfs/meta/ |tail -1 | awk '{print $NF}')
    backup_path=/var/jfs/myjfs/meta/$backup_file
    ls -l $backup_path
    ./juicefs load sqlite3://test2.db $backup_path --encrypt-rsa-key my-priv-key.pem --encrypt-algo aes256gcm-rsa
    ./juicefs mount -d sqlite3://test2.db /jfs2
    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference

    ./juicefs umount /jfs
    ./juicefs umount /jfs2

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid

    uuid=$(./juicefs status sqlite3://test2.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test2.db $uuid
}

prepare_test(){
    umount /jfs || true
    umount /jfs2 || true
    rm -rf test.db test2.db || true
    rm -rf /var/jfs/myjfs || true
    mc rm --force --recursive myminio/test || true
}

do_dump_load(){
    dump_file=$1
    umount /jfs || true
    umount /jfs2 || true
    rm -rf test.db test2.db
    [[ -d /var/jfs1/myjfs ]] && rm -rf /var/jfs1/myjfs
    ./juicefs format $META_URL myjfs --bucket /var/jfs1
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand -v -a
    ./juicefs dump   $META_URL $dump_file

    [[ -d /var/jfs1/myjfs ]] && rm -rf /var/jfs2/myjfs
    # ./juicefs format sqlite3://test2.db myjfs --bucket /var/jfs2
    ./juicefs load   sqlite3://test2.db $dump_file   
    ./juicefs mount -d sqlite3://test2.db /jfs2

    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
    ./juicefs umount /jfs
    ./juicefs umount /jfs2

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid

    uuid=$(./juicefs status sqlite3://test2.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test2.db $uuid
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done

