#!/bin/bash -e
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
sleep 3s
mc alias set myminio http://localhost:9000 minioadmin minioadmin
python3 -c "import xattr" || sudo pip install xattr

test_dump_load_with_iflag(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs --enable-ioctl
    echo "hello" > /jfs/hello.txt
    chattr +i /jfs/hello.txt
    ./juicefs dump $META_URL dump.json
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
    ./juicefs dump --keep-secret-key $META_URL dump.json
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
    ./juicefs dump $META_URL dump.json
    python3 .github/scripts/flush_meta.py $META_URL
    ./juicefs load $META_URL dump.json
    ./juicefs mount -d $META_URL /jfs && echo "mount should fail" && exit 1 || true
    ./juicefs config --secret-key minioadmin $META_URL
    ./juicefs mount -d $META_URL /jfs
    echo "hello" > /jfs/hello.txt
    cat /jfs/hello.txt | grep hello
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

do_dump_load(){
    dump_file=$1
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand -v -a
    ./juicefs dump  $META_URL $dump_file

    ./juicefs load  sqlite3://test2.db $dump_file   
    ./juicefs mount -d sqlite3://test2.db /jfs2

    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
}

source .github/scripts/common/run_test.sh && run_test $@
