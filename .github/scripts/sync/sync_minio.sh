#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META minio
META_URL=$(get_meta_url $META)

test_sync_small_files(){
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 5 --threads 10
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ --list-threads 100 --list-depth 10
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ $count1 -eq $count2 ]
}

test_sync_big_file_with_jfs(){
    prepare_test
    [[ ! -f "/tmp/bigfile" ]] && dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    ./mc cp /tmp/bigfile myminio/myjfs/bigfile
    export dst_jfs=$META_URL 
    timeout 10 ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/myjfs/bigfile jfs://dst_jfs/bigfile --threads=64 --force-update
    cmp /tmp/bigfile /jfs/bigfile
}

test_sync_big_file(){
    prepare_test
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cp myminio/myjfs/bigfile /tmp/bigfile2
    cmp /tmp/bigfile /tmp/bigfile2
}

test_sync_with_limit(){
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 2 --files 5 --threads 10
    ./juicefs sync --limit 1000 minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    count=$(./mc ls myminio/myjfs -r | wc -l)
    echo count is $count
    [ $count -eq 1000 ]
}
test_sync_with_existing(){
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync --existing minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc find myminio/myjfs/abc && echo "myminio/myjfs/abc should not exist" && exit 1 || true
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc find myminio/myjfs/abc
}
test_sync_with_update(){
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    echo def > def
    ./mc cp def myminio/myjfs/abc
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def" && exit 1)
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def" && exit 1)
    ./juicefs sync --force-update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc" && exit 1)
    echo hijk > hijk
    ./mc cp hijk myminio/myjfs/abc
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep hijk || (echo "content should be hijk" && exit 1)
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc" && exit 1)
}

test_sync_hard_link(){
    prepare_test
    echo abc > /jfs/abc
    ln /jfs/abc /jfs/def
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ 
    ./mc cat myminio/myjfs/def | grep abc || (echo "content should be abc" && exit 1)
    echo abcd > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    ./mc cat myminio/myjfs/def | grep abcd || (echo "content should be abcd" && exit 1)
}

test_sync_external_link(){
    prepare_test
    touch hello
    ln -s $(realpath hello) /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    [ -z $(./mc cat myminio/myjfs/hello) ]
}

# list object should be skipped when encountering a loop symlink
test_sync_loop_symlink(){
    prepare_test
    touch hello
    ln -s hello /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    rm -rf /jfs/hello
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
}

test_sync_deep_symlink(){
    prepare_test
    cd /jfs
    echo hello > hello
    ln -s hello symlink_1
    for i in {1..40}; do
        ln -s symlink_$i symlink_$((i+1))
    done
    cat symlink_40 | grep hello
    cat symlink_41 && echo "cat symlink_41 fail" && exit 1 || true
    cd -
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/
    for i in {1..40}; do
        ./mc cat myminio/myjfs/symlink_$i | grep "^hello$"
    done
}

test_sync_list_object_symlink(){
    prepare_test
    cd /jfs
    mkdir dir1
    mkdir -p dir2/src_dir
    echo abc > dir2/src_dir/afile
    ln -s ./../dir2/src_dir dir1/symlink_dir
    cd -
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/dir1/ minio://minioadmin:minioadmin@localhost:9000/myjfs/dir3/
    ./mc cat myminio/myjfs/dir3/symlink_dir/afile | grep abc || (echo "content should be abc" && exit 1)
}

prepare_test(){
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs
    rm -rf /var/jfsCache/myjfs
    (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    lsof -i :9005 | awk 'NR!=1 {print $2}' | xargs -r kill -9 || true
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin ./juicefs gateway $META_URL localhost:9005 &
    wait_gateway_ready
    ./mc alias set juicegw http://localhost:9005 minioadmin minioadmin --api S3v4
}

wait_gateway_ready(){
    timeout=30
    for i in $(seq 1 $timeout); do
        if [[ -z $(lsof -i :9005) ]]; then
            echo "$i Waiting for port 9005 to be ready..."
            sleep 1
        else
            echo "gateway is now ready on port 9005"
            break
        fi
    done
    if [[ -z $(lsof -i :9005) ]]; then
        echo "gateway is not ready after $timeout seconds"
        exit 1
    fi
}

create_sparse_marker_file(){
    local file_path=$1
    local file_size=$2
    python3 - "$file_path" "$file_size" <<'PY'
import sys

path = sys.argv[1]
size = int(sys.argv[2])
markers = [
    (0, b"juicefs-multipart-checkpoint-begin\n"),
    (size // 2, b"juicefs-multipart-checkpoint-middle\n"),
    (size - len(b"juicefs-multipart-checkpoint-end\n"), b"juicefs-multipart-checkpoint-end\n"),
]

with open(path, "wb") as f:
    f.truncate(size)
    for offset, data in markers:
        f.seek(offset)
        f.write(data)
PY
}

assert_checkpoint_contains_multipart_state(){
    local checkpoint_file=$1
    local object_key=$2
    local expected_size=$3
    python3 - "$checkpoint_file" "$object_key" "$expected_size" <<'PY'
import json
import sys

checkpoint_file, object_key, expected_size = sys.argv[1], sys.argv[2], int(sys.argv[3])
with open(checkpoint_file, "r", encoding="utf-8") as f:
    checkpoint = json.load(f)

uploads = checkpoint.get("multipart_uploads") or {}
state = uploads.get(object_key)
if not state:
    raise SystemExit(f"missing multipart checkpoint for {object_key}")

upload = state.get("upload") or {}
upload_id = upload.get("UploadID")
if not upload_id:
    raise SystemExit(f"missing upload id for {object_key}")

if state.get("size") != expected_size:
    raise SystemExit(f"unexpected checkpoint size: {state.get('size')} != {expected_size}")

parts = state.get("parts") or {}
if not parts:
    raise SystemExit(f"missing uploaded parts for {object_key}")

print(upload_id)
PY
}

get_minio_object_size(){
    local target=$1
    ./mc stat --json "$target" | python3 -c 'import json, sys; payload = json.load(sys.stdin); size = payload.get("size"); metadata = payload.get("metadata") or {}; size = metadata.get("content-length") if size is None else size; assert size is not None, "unable to get object size from mc stat output"; print(int(size))'
}

# ---- sync encryption / decryption tests (minio) ----

setup_sync_encrypt_keys(){
    openssl genrsa -out /tmp/sync-enc-nopass.pem 2048
    openssl genrsa -out /tmp/sync-enc-wrong.pem 2048
}
setup_sync_encrypt_keys

# Encrypt: JuiceFS(gateway/minio) -> minio, then decrypt back and verify
test_sync_encrypt_minio_to_minio(){
    prepare_test
    # Put test data into JuiceFS via mount
    echo "hello minio encrypt" > /jfs/enc_test1.txt
    dd if=/dev/urandom of=/jfs/enc_large.bin bs=1M count=3 status=none
    mkdir -p /jfs/enc_subdir
    echo "nested" > /jfs/enc_subdir/nested.txt

    # Encrypt sync: gateway(src) -> minio(dst)
    (./mc rb myminio/enctest > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/enctest/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem

    # Raw content in minio should not equal plaintext
    ./mc cp myminio/enctest/enc_test1.txt /tmp/enc_raw.txt
    if cmp -s /tmp/enc_raw.txt <(echo "hello minio encrypt"); then
        echo "FAIL: enc_test1.txt should be encrypted" && exit 1
    fi

    # Decrypt sync: minio(encrypted) -> local dir
    rm -rf /tmp/minio_dec && mkdir -p /tmp/minio_dec
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest/ /tmp/minio_dec/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem

    # Verify decrypted content
    [ "$(cat /tmp/minio_dec/enc_test1.txt)" = "hello minio encrypt" ] || (echo "FAIL: decrypted text mismatch" && exit 1)
    [ "$(cat /tmp/minio_dec/enc_subdir/nested.txt)" = "nested" ] || (echo "FAIL: nested decrypted mismatch" && exit 1)
    cmp /jfs/enc_large.bin /tmp/minio_dec/enc_large.bin || (echo "FAIL: large binary mismatch" && exit 1)
    echo "test_sync_encrypt_minio_to_minio passed"
}

# Encrypt sync: local -> minio with chacha20-rsa, decrypt back
test_sync_encrypt_minio_chacha20(){
    prepare_test
    rm -rf /tmp/minio_enc_src && mkdir -p /tmp/minio_enc_src
    echo "chacha20 test" > /tmp/minio_enc_src/file1.txt
    dd if=/dev/urandom of=/tmp/minio_enc_src/file2.bin bs=1M count=2 status=none

    (./mc rb myminio/enctest2 > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest2
    ./juicefs sync /tmp/minio_enc_src/ minio://minioadmin:minioadmin@localhost:9000/enctest2/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-algo chacha20-rsa

    rm -rf /tmp/minio_dec2 && mkdir -p /tmp/minio_dec2
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest2/ /tmp/minio_dec2/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --decrypt-algo chacha20-rsa

    [ "$(cat /tmp/minio_dec2/file1.txt)" = "chacha20 test" ] || (echo "FAIL: chacha20 decrypted mismatch" && exit 1)
    cmp /tmp/minio_enc_src/file2.bin /tmp/minio_dec2/file2.bin || (echo "FAIL: binary mismatch" && exit 1)
    echo "test_sync_encrypt_minio_chacha20 passed"
}

# Decrypt with wrong key from minio should fail
test_sync_encrypt_minio_wrong_key(){
    prepare_test
    echo "secret data" > /jfs/secret.txt

    (./mc rb myminio/enctest3 > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest3
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/enctest3/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem

    rm -rf /tmp/minio_dec3 && mkdir -p /tmp/minio_dec3
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest3/ /tmp/minio_dec3/ \
        --decrypt-rsa-key /tmp/sync-enc-wrong.pem 2>&1 | tee /tmp/minio_enc_err.log || true

    if [ -f /tmp/minio_dec3/secret.txt ] && [ "$(cat /tmp/minio_dec3/secret.txt)" = "secret data" ]; then
        echo "FAIL: wrong key should not decrypt correctly" && exit 1
    fi
    echo "test_sync_encrypt_minio_wrong_key passed"
}

# Re-encrypt between minio buckets with different keys
test_sync_reencrypt_minio(){
    prepare_test
    echo "reencrypt data" > /jfs/reenc.txt
    dd if=/dev/urandom of=/jfs/reenc.bin bs=1M count=2 status=none

    (./mc rb myminio/enctest4 > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest4
    (./mc rb myminio/enctest5 > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest5

    # Encrypt with key1
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/enctest4/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem

    # Re-encrypt: decrypt key1, encrypt key2
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest4/ minio://minioadmin:minioadmin@localhost:9000/enctest5/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --encrypt-rsa-key /tmp/sync-enc-wrong.pem

    # Decrypt with key2
    rm -rf /tmp/minio_dec4 && mkdir -p /tmp/minio_dec4
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest5/ /tmp/minio_dec4/ \
        --decrypt-rsa-key /tmp/sync-enc-wrong.pem

    [ "$(cat /tmp/minio_dec4/reenc.txt)" = "reencrypt data" ] || (echo "FAIL: reencrypted text mismatch" && exit 1)
    cmp /jfs/reenc.bin /tmp/minio_dec4/reenc.bin || (echo "FAIL: reencrypted binary mismatch" && exit 1)
    echo "test_sync_reencrypt_minio passed"
}

# Encrypt sync minio -> jfs:// with --update
test_sync_encrypt_minio_to_jfs(){
    prepare_test
    echo "minio to jfs" > /jfs/m2j.txt
    dd if=/dev/urandom of=/jfs/m2j.bin bs=1M count=2 status=none

    (./mc rb myminio/enctest6 > /dev/null 2>&1 --force || true) && ./mc mb myminio/enctest6
    # Encrypt gateway -> minio
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/enctest6/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem

    # Decrypt minio -> jfs:// (new JFS volume)
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs /var/jfsCache/myjfs
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    meta_url=$META_URL ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/enctest6/ jfs://meta_url/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --update

    [ "$(cat /jfs/m2j.txt)" = "minio to jfs" ] || (echo "FAIL: jfs decrypt mismatch" && exit 1)
    echo "test_sync_encrypt_minio_to_jfs passed"
}

# ---- sync global traffic control tests (minio) ----

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

# Traffic control: JFS gateway -> minio with traffic control
test_sync_traffic_control_minio(){
    prepare_test
    for i in $(seq 1 50); do
        dd if=/dev/urandom of=/jfs/tc_file$i bs=10K count=1 status=none
    done
    dd if=/dev/urandom of=/jfs/tc_large bs=1M count=2 status=none
    start_traffic_control_server 0
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ "$count1" -eq "$count2" ] || (echo "FAIL: count mismatch $count1 vs $count2" && exit 1)
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_minio passed"
}

# Traffic control with rate limit: minio -> JFS
test_sync_traffic_control_minio_to_jfs(){
    prepare_test
    (./mc rb myminio/tcsrc > /dev/null 2>&1 --force || true) && ./mc mb myminio/tcsrc
    for i in $(seq 1 20); do
        dd if=/dev/urandom of=/tmp/tc_minio_file$i bs=50K count=1 status=none
        ./mc cp /tmp/tc_minio_file$i myminio/tcsrc/file$i
    done
    start_traffic_control_server 0
    meta_url=$META_URL ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/tcsrc/ jfs://meta_url/tc_from_minio/ \
        --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
    dec_count=$(find /jfs/tc_from_minio -type f | wc -l)
    [ "$dec_count" -eq 20 ] || (echo "FAIL: expected 20 files, got $dec_count" && exit 1)
    for i in $(seq 1 20); do
        cmp /tmp/tc_minio_file$i /jfs/tc_from_minio/file$i || (echo "FAIL: file$i mismatch" && exit 1)
    done
    check_tc_log 1
    kill_traffic_control_server
    echo "test_sync_traffic_control_minio_to_jfs passed"
}

# Traffic control combined with encrypt/decrypt
test_sync_traffic_control_encrypt(){
    prepare_test
    echo "tc encrypt test" > /jfs/tc_enc1.txt
    dd if=/dev/urandom of=/jfs/tc_enc_large.bin bs=1M count=2 status=none
    (./mc rb myminio/tcenc > /dev/null 2>&1 --force || true) && ./mc mb myminio/tcenc
    start_traffic_control_server 0
    # Encrypt with traffic control
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/tcenc/ \
        --encrypt-rsa-key /tmp/sync-enc-nopass.pem --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in encrypt sync.log" && exit 1 || true
    # Decrypt with traffic control
    rm -rf /tmp/tc_enc_dec && mkdir -p /tmp/tc_enc_dec
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9000/tcenc/ /tmp/tc_enc_dec/ \
        --decrypt-rsa-key /tmp/sync-enc-nopass.pem --traffic-control-url $TC_URL >sync.log 2>&1
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in decrypt sync.log" && exit 1 || true
    [ "$(cat /tmp/tc_enc_dec/tc_enc1.txt)" = "tc encrypt test" ] || (echo "FAIL: decrypt mismatch" && exit 1)
    cmp /jfs/tc_enc_large.bin /tmp/tc_enc_dec/tc_enc_large.bin || (echo "FAIL: large file mismatch" && exit 1)
    check_tc_log 2
    kill_traffic_control_server
    echo "test_sync_traffic_control_encrypt passed"
}

test_checkpoint_minio_basic(){
    # Test: checkpoint basic resume for minio-to-minio sync
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 5 --threads 10
    # First sync with checkpoint, interrupt early
    timeout 5 ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync1.log 2>&1 || true
    cat sync1.log
    # Checkpoint file should exist in destination
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "checkpoint file should exist after interrupted minio sync"
        exit 1
    fi
    # Resume sync
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    cat sync2.log
    count1=$(./mc ls -r juicegw/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch: $count1 vs $count2" && exit 1)
    # Checkpoint should be cleaned up
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -ne 0 ]; then
        echo "checkpoint file should be deleted after successful minio sync"
        exit 1
    fi
}

test_checkpoint_minio_cleanup_on_success(){
    # Test: checkpoint file is deleted after successful minio sync (no interruption)
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 5 --depth 2 --files 10 --threads 10
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync.log 2>&1
    count1=$(./mc ls -r juicegw/myjfs/ | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch: $count1 vs $count2" && exit 1)
    # Verify checkpoint cleaned up
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -ne 0 ]; then
        echo "checkpoint file should be deleted after success"
        exit 1
    fi
    grep "panic:\|<FATAL>" sync.log && echo "panic or fatal in sync.log" && exit 1 || true
}

test_checkpoint_minio_stats_correctness(){
    # Test: checkpoint stats correctness for minio sync
    prepare_test
    echo abc > /jfs/abc
    echo def > /jfs/def
    echo ghi > /jfs/ghi
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        2>&1 | tee sync.log
    # Verify stats show correct copied count
    copied=$(grep -oP 'Copied:\s*\K\d+' sync.log || grep -oP 'copied:\s*\K\d+' sync.log || echo "0")
    if [ "$copied" -lt 3 ]; then
        echo "Warning: copied count ($copied) seems low for 3 files"
    fi
    # Rerun - should skip existing files
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        2>&1 | tee sync2.log
    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_minio_with_update(){
    # Test: checkpoint + --update for minio sync
    prepare_test
    echo abc > /jfs/abc
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    echo def > def
    ./mc cp def myminio/myjfs/abc
    # Sync with --update + checkpoint should keep newer dst
    ./juicefs sync --update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    ./mc cat myminio/myjfs/abc | grep def || (echo "content should be def with --update" && exit 1)
    # Sync with --force-update + checkpoint should overwrite
    ./juicefs sync --force-update minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --enable-checkpoint --checkpoint-interval 2s
    ./mc cat myminio/myjfs/abc | grep abc || (echo "content should be abc with --force-update" && exit 1)
}

test_checkpoint_minio_big_file_resume(){
    # Test: checkpoint resume for large file minio sync
    prepare_test
    dd if=/dev/urandom of=/tmp/bigfile_ckpt bs=1M count=256
    cp /tmp/bigfile_ckpt /jfs/bigfile
    # First sync with checkpoint, interrupt
    timeout 10 ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync1.log 2>&1 || true
    # Resume
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    ./mc cp myminio/myjfs/bigfile /tmp/bigfile_ckpt2
    cmp /tmp/bigfile_ckpt /tmp/bigfile_ckpt2 || (echo "big file content mismatch after checkpoint resume" && exit 1)
    rm -f /tmp/bigfile_ckpt /tmp/bigfile_ckpt2
}

test_checkpoint_minio_multipart_resume(){
    # Test: multipart upload checkpoint should persist upload state and resume for >4GiB objects
    prepare_test
    local key="multipart-checkpoint.bin"
    local size=$((5 * 1024 * 1024 * 1024 + 17 * 1024 * 1024))
    local checkpoint_json=/tmp/minio-multipart-checkpoint.json
    rm -f "$checkpoint_json"
    create_sparse_marker_file "/jfs/$key" "$size"

    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --threads 1 --list-threads 1 --debug \
        --enable-checkpoint --checkpoint-interval 60s \
        >sync1.log 2>&1 &
    sync_pid=$!
    sleep 4
    kill -INT $sync_pid || true
    wait $sync_pid || true

    checkpoint_file=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | head -1)
    if [ -z "$checkpoint_file" ]; then
        echo "checkpoint file should exist after interrupted multipart minio sync"
        cat sync1.log
        exit 1
    fi

    ./mc cat "$checkpoint_file" > "$checkpoint_json"
    upload_id=$(assert_checkpoint_contains_multipart_state "$checkpoint_json" "$key" "$size")
    [ -n "$upload_id" ] || (echo "multipart upload id should not be empty" && exit 1)

    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --threads 1 --list-threads 1 --debug \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1

    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -ne 0 ]; then
        echo "checkpoint file should be deleted after multipart minio sync resumes successfully"
        cat sync2.log
        exit 1
    fi

    dst_size=$(get_minio_object_size "myminio/myjfs/$key")
    if [ "$dst_size" -ne "$size" ]; then
        echo "multipart minio object size mismatch: $dst_size vs $size"
        exit 1
    fi

    grep "panic:\|<FATAL>" sync2.log && echo "panic or fatal in sync2.log" && exit 1 || true
}

test_checkpoint_minio_signal_save(){
    # Test: SIGINT saves checkpoint for minio sync
    prepare_test
    ./juicefs mdtest $META_URL /test --dirs 10 --depth 3 --files 10 --threads 10
    # Start sync in background
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 60s \
        >sync1.log 2>&1 &
    sync_pid=$!
    sleep 3
    kill -INT $sync_pid || true
    wait $sync_pid || true
    # Checkpoint should be saved
    checkpoint_count=$(./mc find myminio/myjfs/ --name ".juicefs-sync-checkpoint*" 2>/dev/null | wc -l)
    if [ "$checkpoint_count" -eq 0 ]; then
        echo "checkpoint file should exist after SIGINT"
        exit 1
    fi
    # Resume should work
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/ minio://minioadmin:minioadmin@localhost:9000/myjfs/ \
        --list-threads 100 --list-depth 10 \
        --enable-checkpoint --checkpoint-interval 2s \
        >sync2.log 2>&1
    count1=$(./mc ls -r juicegw/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    count2=$(./mc ls -r myminio/myjfs/ | grep -v ".juicefs-sync-checkpoint" | wc -l)
    [ $count1 -eq $count2 ] || (echo "file count mismatch after resume: $count1 vs $count2" && exit 1)
}

test_sync_multipart_stream_various_sizes(){
    prepare_test
    local part=$((5 * 1024 * 1024))  # 5MiB default part size
    local sizes=(
        $((part - 1))       # 5MiB-1  - single part
        $((part + 1))       # 5MiB+1  - triggers 2 parts
        $((part * 2))       # 10MiB   - 2 parts
        $((part * 2 + 17))  # 10MiB+17 - odd-size 2 parts
        $((part * 3 - 1))   # 15MiB-1 - 3 parts (tests non-aligned)
    )

    mkdir -p /jfs/stream_sizes
    for size in "${sizes[@]}"; do
        local filename="file_${size}"
        # Write 4KiB random header then truncate to target size
        dd if=/dev/urandom of="/jfs/stream_sizes/$filename" bs=4096 count=1 2>/dev/null
        truncate -s "$size" "/jfs/stream_sizes/$filename"
    done

    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/stream_sizes/ \
        minio://minioadmin:minioadmin@localhost:9000/myjfs/stream_sizes/ \
        --threads 4 --debug 2>&1 | tee /tmp/sync_stream_sizes.log

    # Verify all files synced correctly
    for size in "${sizes[@]}"; do
        local filename="file_${size}"
        ./mc cp "myminio/myjfs/stream_sizes/$filename" "/tmp/$filename"
        cmp "/jfs/stream_sizes/$filename" "/tmp/$filename" || \
            (echo "FAIL: content mismatch for $filename (size=$size)" && exit 1)
        rm -f "/tmp/$filename"
    done

    echo "PASS: multipart stream upload with various sizes"
}

test_sync_multipart_stream_concurrent(){
    prepare_test
    local num_files=3
    local part=$((5 * 1024 * 1024))  # 5MiB default part size
    local file_size=$((part * 2 + 1024))  # ~10MiB - triggers 3 parts

    mkdir -p /jfs/stream_concurrent
    for i in $(seq 1 $num_files); do
        # Random header so cmp can detect corruption
        dd if=/dev/urandom of="/jfs/stream_concurrent/file_$i" bs=4096 count=1 2>/dev/null
        truncate -s "$file_size" "/jfs/stream_concurrent/file_$i"
    done

    # Sync with multiple threads to trigger concurrent multipart uploads
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/stream_concurrent/ \
        minio://minioadmin:minioadmin@localhost:9000/myjfs/stream_concurrent/ \
        --threads 6 --debug 2>&1 | tee /tmp/sync_stream_concurrent.log

    # Verify all files
    for i in $(seq 1 $num_files); do
        ./mc cp "myminio/myjfs/stream_concurrent/file_$i" "/tmp/file_$i"
        cmp "/jfs/stream_concurrent/file_$i" "/tmp/file_$i" || \
            (echo "FAIL: content mismatch for file_$i" && exit 1)
        rm -f "/tmp/file_$i"
    done

    # Check for any errors in log
    grep -i "panic:\|<FATAL>" /tmp/sync_stream_concurrent.log && \
        (echo "FAIL: panic or fatal error in concurrent stream sync" && exit 1) || true

    echo "PASS: concurrent multipart stream upload"
}

test_sync_multipart_stream_integrity(){
    prepare_test
    local part=$((5 * 1024 * 1024))  # 5MiB default part size
    local file_size=$((part * 3))    # 15MiB - 3 parts
    local filename="integrity_check.bin"

    # Full random content so MD5 detects any corruption
    dd if=/dev/urandom of="/jfs/$filename" bs=1M count=15 2>/dev/null
    local src_md5
    src_md5=$(md5sum "/jfs/$filename" | awk '{print $1}')
    local src_size
    src_size=$(stat -c%s "/jfs/$filename" 2>/dev/null || stat -f%z "/jfs/$filename")

    # Sync using stream API
    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/$filename \
        minio://minioadmin:minioadmin@localhost:9000/myjfs/$filename \
        --threads 4 --debug 2>&1 | tee /tmp/sync_stream_integrity.log

    # Download and verify
    ./mc cp "myminio/myjfs/$filename" "/tmp/$filename"
    local dst_md5
    dst_md5=$(md5sum "/tmp/$filename" | awk '{print $1}')
    local dst_size
    dst_size=$(stat -c%s "/tmp/$filename" 2>/dev/null || stat -f%z "/tmp/$filename")

    if [ "$src_md5" != "$dst_md5" ]; then
        echo "FAIL: MD5 checksum mismatch"
        echo "  src: $src_md5"
        echo "  dst: $dst_md5"
        exit 1
    fi

    if [ "$src_size" != "$dst_size" ]; then
        echo "FAIL: file size mismatch"
        echo "  src: $src_size"
        echo "  dst: $dst_size"
        exit 1
    fi

    rm -f "/tmp/$filename"
    echo "PASS: multipart stream upload integrity check"
}

test_sync_multipart_stream_boundary(){
    prepare_test
    local part=$((5 * 1024 * 1024))  # 5MiB default part size

    mkdir -p /jfs/stream_boundary
    local sizes=(
        $((part - 1))       # Just below 1 part
        $part               # Exact 1 part
        $((part + 1))       # Just above 1 part
        $((part * 2 - 1))   # Just below 2 parts
        $((part * 2))       # Exact 2 parts
        $((part * 2 + 1))   # Just above 2 parts
        $((part * 3 - 1))   # Just below 3 parts
        $((part * 3))       # Exact 3 parts
        $((part * 3 + 1))   # Just above 3 parts
    )

    for size in "${sizes[@]}"; do
        local filename="boundary_${size}"
        # Random 4KiB header + sparse tail for fast creation
        dd if=/dev/urandom of="/jfs/stream_boundary/$filename" bs=4096 count=1 2>/dev/null
        truncate -s "$size" "/jfs/stream_boundary/$filename"
    done

    ./juicefs sync minio://minioadmin:minioadmin@localhost:9005/myjfs/stream_boundary/ \
        minio://minioadmin:minioadmin@localhost:9000/myjfs/stream_boundary/ \
        --threads 2 --debug 2>&1 | tee /tmp/sync_stream_boundary.log

    # Verify all files
    for size in "${sizes[@]}"; do
        local filename="boundary_${size}"
        ./mc cp "myminio/myjfs/stream_boundary/$filename" "/tmp/$filename"
        cmp "/jfs/stream_boundary/$filename" "/tmp/$filename" || \
            (echo "FAIL: content mismatch for $filename (size=$size)" && exit 1)
        rm -f "/tmp/$filename"
    done

    echo "PASS: multipart stream upload boundary test"
}

test_sync_multipart_stream_fallback(){
    prepare_test
    local part=$((5 * 1024 * 1024))  # 5MiB default part size
    local file_size=$((part * 2 + 1024))  # ~10MiB - 3 parts

    dd if=/dev/urandom of="/jfs/fallback_test.bin" bs=4096 count=1 2>/dev/null
    truncate -s "$file_size" "/jfs/fallback_test.bin"

    # Sync from JFS mount directly to minio (no gateway in between)
    ./juicefs sync /jfs/fallback_test.bin minio://minioadmin:minioadmin@localhost:9000/myjfs/fallback_test.bin \
        --threads 2 --debug 2>&1 | tee /tmp/sync_stream_fallback.log

    # Download and verify
    ./mc cp "myminio/myjfs/fallback_test.bin" "/tmp/fallback_test.bin"
    cmp "/jfs/fallback_test.bin" "/tmp/fallback_test.bin" || \
        (echo "FAIL: content mismatch for fallback test" && exit 1)

    # Check log doesn't have fatal errors
    grep -i "panic:\|<FATAL>" /tmp/sync_stream_fallback.log && \
        (echo "FAIL: panic or fatal error in fallback sync" && exit 1) || true

    rm -f "/tmp/fallback_test.bin"
    echo "PASS: multipart stream fallback test"
}

source .github/scripts/common/run_test.sh && run_test $@
