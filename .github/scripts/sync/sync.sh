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

# ---- sync encryption / decryption tests ----

setup_sync_encrypt_keys(){
    # RSA key without passphrase
    openssl genrsa -out /tmp/sync-enc-nopass.pem 2048
    # RSA key with passphrase
    openssl genrsa -aes256 -passout pass:sync-enc-pass -out /tmp/sync-enc-withpass.pem 2048
    # A different RSA key (for wrong-key tests)
    openssl genrsa -out /tmp/sync-enc-wrong.pem 2048
    # 4096-bit RSA key
    openssl genrsa -out /tmp/sync-enc-4096.pem 4096
}
setup_sync_encrypt_keys

generate_encrypt_source(){
    rm -rf /tmp/sync_enc_src
    mkdir -p /tmp/sync_enc_src/subdir
    echo "hello world" > /tmp/sync_enc_src/small.txt
    echo "foo bar baz" > /tmp/sync_enc_src/subdir/nested.txt
    touch /tmp/sync_enc_src/empty.txt
    dd if=/dev/urandom of=/tmp/sync_enc_src/medium.bin bs=1K count=100 status=none
    # Exactly 1 MiB (chunk boundary)
    dd if=/dev/urandom of=/tmp/sync_enc_src/exact_1m.bin bs=1M count=1 status=none
    # Slightly over chunk boundary
    dd if=/dev/urandom of=/tmp/sync_enc_src/over_1m.bin bs=1K count=1025 status=none
    # Multi-chunk file (5 MiB)
    dd if=/dev/urandom of=/tmp/sync_enc_src/large.bin bs=1M count=5 status=none
    echo -n "x" > /tmp/sync_enc_src/tiny.txt
}

prepare_encrypt_test(){
    prepare_test
    rm -rf /tmp/sync_enc_dst /tmp/sync_enc_dec
    mkdir -p /tmp/sync_enc_dst /tmp/sync_enc_dec
}

verify_encrypted(){
    local src_dir=$1
    local dst_dir=$2
    for f in $(find "$src_dir" -type f -printf '%P\n'); do
        [ ! -f "$dst_dir/$f" ] && echo "FAIL: $dst_dir/$f not found" && exit 1
        local src_size=$(stat -c%s "$src_dir/$f")
        local dst_size=$(stat -c%s "$dst_dir/$f")
        if [ "$src_size" -gt 0 ]; then
            cmp -s "$src_dir/$f" "$dst_dir/$f" && echo "FAIL: $f not encrypted" && exit 1
            [ "$dst_size" -le "$src_size" ] && echo "FAIL: encrypted $f should be larger" && exit 1
        fi
    done
    echo "verify_encrypted passed"
}

verify_decrypted(){
    local src_dir=$1
    local dec_dir=$2
    for f in $(find "$src_dir" -type f -printf '%P\n'); do
        [ ! -f "$dec_dir/$f" ] && echo "FAIL: $dec_dir/$f not found" && exit 1
        cmp -s "$src_dir/$f" "$dec_dir/$f" || (echo "FAIL: $f mismatch" && exit 1)
    done
    echo "verify_decrypted passed"
}

# Basic encrypt/decrypt with local dirs, default algo (aes256gcm-rsa)
test_sync_encrypt_decrypt_local(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt/decrypt with passphrase-protected key
test_sync_encrypt_decrypt_passphrase(){
    generate_encrypt_source
    prepare_encrypt_test
    export JFS_ENCRYPT_RSA_PASSPHRASE=sync-enc-pass
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-withpass.pem
    unset JFS_ENCRYPT_RSA_PASSPHRASE
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    export JFS_DECRYPT_RSA_PASSPHRASE=sync-enc-pass
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-withpass.pem
    unset JFS_DECRYPT_RSA_PASSPHRASE
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt/decrypt with chacha20-rsa algorithm
test_sync_encrypt_decrypt_chacha20(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo chacha20-rsa
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo chacha20-rsa
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt/decrypt with 4096-bit RSA key
test_sync_encrypt_decrypt_rsa4096(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-4096.pem
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-4096.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Decrypt with wrong key should fail
test_sync_encrypt_wrong_key(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-wrong.pem 2>&1 | tee /tmp/sync_enc_err.log || true
    local match=0
    for f in $(find /tmp/sync_enc_src -type f -printf '%P\n'); do
        if [ -f "/tmp/sync_enc_dec/$f" ] && cmp -s "/tmp/sync_enc_src/$f" "/tmp/sync_enc_dec/$f"; then
            match=$((match + 1))
        fi
    done
    local total=$(find /tmp/sync_enc_src -type f | wc -l)
    [ "$match" -eq "$total" ] && echo "FAIL: wrong key should not decrypt all files" && exit 1
    echo "test_sync_encrypt_wrong_key passed"
}

# Decrypt with wrong algorithm should fail
test_sync_encrypt_wrong_algo(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo aes256gcm-rsa
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo chacha20-rsa 2>&1 | tee /tmp/sync_enc_err.log || true
    local match=0
    for f in $(find /tmp/sync_enc_src -type f -printf '%P\n'); do
        if [ -f "/tmp/sync_enc_dec/$f" ] && cmp -s "/tmp/sync_enc_src/$f" "/tmp/sync_enc_dec/$f"; then
            match=$((match + 1))
        fi
    done
    local total=$(find /tmp/sync_enc_src -type f | wc -l)
    [ "$match" -eq "$total" ] && echo "FAIL: wrong algo should not decrypt all files" && exit 1
    echo "test_sync_encrypt_wrong_algo passed"
}

# Large multi-chunk file encrypt/decrypt (9 MiB)
test_sync_encrypt_large_file(){
    prepare_encrypt_test
    rm -rf /tmp/sync_enc_src && mkdir -p /tmp/sync_enc_src
    dd if=/dev/urandom of=/tmp/sync_enc_src/large9m.bin bs=1M count=9 status=none
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt to JuiceFS mount point, then decrypt back
test_sync_encrypt_with_mount_point(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs sync /tmp/sync_enc_src/ /jfs/encrypted/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_encrypted /tmp/sync_enc_src /jfs/encrypted
    ./juicefs sync /jfs/encrypted/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt via jfs:// protocol (no mount point)
test_sync_encrypt_without_mount_point(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs format $META_URL $FORMAT_OPTIONS myjfs
    meta_url=$META_URL ./juicefs sync /tmp/sync_enc_src/ jfs://meta_url/encrypted/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    ./juicefs mount -d $META_URL /jfs
    verify_encrypted /tmp/sync_enc_src /jfs/encrypted
    meta_url=$META_URL ./juicefs sync jfs://meta_url/encrypted/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Re-encrypt: decrypt with key1, encrypt with key2
test_sync_reencrypt(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    rm -rf /tmp/sync_enc_reenc && mkdir -p /tmp/sync_enc_reenc
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_reenc/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-rsa-key /tmp/sync-enc-wrong.pem
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_reenc
    ./juicefs sync /tmp/sync_enc_reenc/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-wrong.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Re-encrypt across different algorithms (aes256gcm-rsa -> chacha20-rsa)
test_sync_reencrypt_diff_algo(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo aes256gcm-rsa
    rm -rf /tmp/sync_enc_reenc && mkdir -p /tmp/sync_enc_reenc
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_reenc/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo aes256gcm-rsa \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo chacha20-rsa
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_reenc
    ./juicefs sync /tmp/sync_enc_reenc/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo chacha20-rsa
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt with --update (incremental sync)
test_sync_encrypt_update(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem
    sleep 2
    echo "updated content" > /tmp/sync_enc_src/small.txt
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --update
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    [ "$(cat /tmp/sync_enc_dec/small.txt)" = "updated content" ] || (echo "FAIL: updated content mismatch" && exit 1)
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt with --check-all --dirs --list-threads combined
test_sync_encrypt_combined_flags(){
    generate_encrypt_source
    prepare_encrypt_test
    mkdir -p /tmp/sync_enc_src/empty_subdir
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem \
        --check-all --update --dirs --list-threads 10 --list-depth 5
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    [ -d /tmp/sync_enc_dst/empty_subdir ] || (echo "FAIL: empty_subdir should exist with --dirs" && exit 1)
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem \
        --check-all --update --dirs --list-threads 10 --list-depth 5
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt with --delete-dst
test_sync_encrypt_delete_dst(){
    generate_encrypt_source
    prepare_encrypt_test
    echo "extra" > /tmp/sync_enc_dst/extra_file.txt
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --delete-dst
    [ -f /tmp/sync_enc_dst/extra_file.txt ] && echo "FAIL: extra_file.txt should be deleted" && exit 1
    verify_encrypted /tmp/sync_enc_src /tmp/sync_enc_dst
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    verify_decrypted /tmp/sync_enc_src /tmp/sync_enc_dec
}

# Encrypt with --exclude filter
test_sync_encrypt_exclude(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --exclude '*.bin'
    bin_count=$(find /tmp/sync_enc_dst -name '*.bin' -type f | wc -l)
    [ "$bin_count" -gt 0 ] && echo "FAIL: *.bin should be excluded" && exit 1
    txt_count=$(find /tmp/sync_enc_dst -name '*.txt' -type f | wc -l)
    [ "$txt_count" -eq 0 ] && echo "FAIL: *.txt should be present" && exit 1
    ./juicefs sync /tmp/sync_enc_dst/ /tmp/sync_enc_dec/ --decrypt-rsa-key /tmp/sync-enc-nopass.pem
    for f in $(find /tmp/sync_enc_src -name '*.txt' -type f -printf '%P\n'); do
        cmp -s "/tmp/sync_enc_src/$f" "/tmp/sync_enc_dec/$f" || (echo "FAIL: $f mismatch" && exit 1)
    done
    echo "test_sync_encrypt_exclude passed"
}

# Encrypt with --dry (should not write any files)
test_sync_encrypt_dry_run(){
    generate_encrypt_source
    prepare_encrypt_test
    ./juicefs sync /tmp/sync_enc_src/ /tmp/sync_enc_dst/ --encrypt-rsa-key /tmp/sync-enc-nopass.pem --dry 2>&1 | tee /tmp/sync_enc_dry.log
    file_count=$(find /tmp/sync_enc_dst -type f 2>/dev/null | wc -l)
    [ "$file_count" -gt 0 ] && echo "FAIL: --dry should not write files" && exit 1
    echo "test_sync_encrypt_dry_run passed"
}


# ---- sync global traffic control tests ----

TC_PORT=12345
TC_URL="http://localhost:${TC_PORT}/"
TC_LOG="/tmp/tc_server.log"

start_traffic_control_server(){
    local bwlimit=${1:-0}
    kill_traffic_control_server
    python3 .github/scripts/traffic_control_server.py --port $TC_PORT --bwlimit $bwlimit --log $TC_LOG &
    TC_PID=$!
    sleep 1
    if ! kill -0 $TC_PID 2>/dev/null; then
        echo "FAIL: traffic control server failed to start"
        exit 1
    fi
}

kill_traffic_control_server(){
    lsof -i :$TC_PORT -t 2>/dev/null | xargs -r kill -9 || true
    sleep 0.5
}

check_tc_log(){
    local min_requests=${1:-1}
    [ ! -f $TC_LOG ] && echo "FAIL: TC log not found" && exit 1
    local req_count=$(wc -l < $TC_LOG)
    if [ "$req_count" -lt "$min_requests" ]; then
        echo "FAIL: expected at least $min_requests TC requests, got $req_count"
        cat $TC_LOG
        exit 1
    fi
    echo "TC log: $req_count requests"
}

# Basic traffic control: sync local dirs with --traffic-control-url
test_sync_traffic_control_basic(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf /tmp/tc_src && mkdir -p /tmp/tc_src
    for i in $(seq 1 50); do
        dd if=/dev/urandom of=/tmp/tc_src/file$i bs=10K count=1 status=none
    done
    start_traffic_control_server 0
    ./juicefs sync /tmp/tc_src/ /jfs/tc_dst/ --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    diff /tmp/tc_src/ /jfs/tc_dst/
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_basic passed"
}

# Traffic control with bwlimit (combined local + global limiting)
test_sync_traffic_control_with_bwlimit(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf /tmp/tc_src && mkdir -p /tmp/tc_src
    for i in $(seq 1 20); do
        dd if=/dev/urandom of=/tmp/tc_src/file$i bs=100K count=1 status=none
    done
    start_traffic_control_server 0
    ./juicefs sync /tmp/tc_src/ /jfs/tc_dst/ --traffic-control-url $TC_URL --bwlimit 8192 >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    diff /tmp/tc_src/ /jfs/tc_dst/
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_with_bwlimit passed"
}

# Traffic control with rate-limited server
test_sync_traffic_control_ratelimit(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf /tmp/tc_src && mkdir -p /tmp/tc_src
    for i in $(seq 1 10); do
        dd if=/dev/urandom of=/tmp/tc_src/file$i bs=100K count=1 status=none
    done
    # 500KB/s limit on the server side
    start_traffic_control_server 512000
    ./juicefs sync /tmp/tc_src/ /jfs/tc_dst/ --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    diff /tmp/tc_src/ /jfs/tc_dst/
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_ratelimit passed"
}

# Traffic control with JFS source (without mount point) 
test_sync_traffic_control_jfs_source(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    mkdir -p /jfs/data
    for i in $(seq 1 30); do
        dd if=/dev/urandom of=/jfs/data/file$i bs=10K count=1 status=none
    done
    rm -rf /tmp/tc_dec && mkdir -p /tmp/tc_dec
    start_traffic_control_server 0
    meta_url=$META_URL ./juicefs sync jfs://meta_url/data/ /tmp/tc_dec/ --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    diff /jfs/data/ /tmp/tc_dec/
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_jfs_source passed"
}

# Traffic control with --update and --check-all
test_sync_traffic_control_update(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d
    rm -rf /tmp/tc_src && mkdir -p /tmp/tc_src
    for i in $(seq 1 20); do
        echo "initial-$i" > /tmp/tc_src/file$i.txt
    done
    start_traffic_control_server 0
    ./juicefs sync /tmp/tc_src/ /jfs/tc_dst/ --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    # Update some files
    sleep 1
    for i in $(seq 1 5); do
        echo "updated-$i" > /tmp/tc_src/file$i.txt
    done
    ./juicefs sync /tmp/tc_src/ /jfs/tc_dst/ --traffic-control-url $TC_URL --update --check-all >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    [ "$(cat /jfs/tc_dst/file1.txt)" = "updated-1" ] || (echo "FAIL: file1 not updated" && exit 1)
    [ "$(cat /jfs/tc_dst/file10.txt)" = "initial-10" ] || (echo "FAIL: file10 should not change" && exit 1)
    check_tc_log 2
    kill_traffic_control_server
    echo "test_sync_traffic_control_update passed"
}

source .github/scripts/common/run_test.sh && run_test $@
