#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

SMB_CONTAINER_NAME="juicefs-ci-smb"
SMB_USER="juicefs"
SMB_PASSWORD="juicefs"
SMB_SHARE="share"

cleanup_smb_container()
{
    docker rm -f "$SMB_CONTAINER_NAME" >/dev/null 2>&1 || true
    rm -rf /tmp/${SMB_CONTAINER_NAME}-data >/dev/null 2>&1 || true
}

start_smb_container()
{
    cleanup_smb_container
    mkdir -p /tmp/${SMB_CONTAINER_NAME}-data
    chmod 0777 /tmp/${SMB_CONTAINER_NAME}-data
    if [[ "$(uname)" == "Darwin" ]]; then
        docker run -d --name "$SMB_CONTAINER_NAME" -p 1445:445 \
            -v /tmp/${SMB_CONTAINER_NAME}-data:/mount \
            dperson/samba \
            -u "$SMB_USER;$SMB_PASSWORD" \
            -s "$SMB_SHARE;/mount;yes;no;no;$SMB_USER" >/dev/null
        wait_tcp_ready 127.0.0.1 1445 40
        SMB_ENDPOINT="127.0.0.1:1445/${SMB_SHARE}"
        export SMB_ENDPOINT
        return
    fi

    docker run -d --name "$SMB_CONTAINER_NAME" \
        -v /tmp/${SMB_CONTAINER_NAME}-data:/mount \
        dperson/samba \
        -u "$SMB_USER;$SMB_PASSWORD" \
        -s "$SMB_SHARE;/mount;yes;no;no;$SMB_USER" >/dev/null

    local container_ip
    container_ip=$(docker container inspect "$SMB_CONTAINER_NAME" --format '{{ .NetworkSettings.IPAddress }}')
    wait_tcp_ready "$container_ip" 445 40
    SMB_ENDPOINT="${container_ip}/${SMB_SHARE}"
    export SMB_ENDPOINT
}

assert_objbench_result()
{
    local log_file=$1
    local test_name=$2
    local expected=$3
    if ! grep -E "${test_name}.*${expected}" "$log_file" >/dev/null; then
        echo "objbench assertion failed: test=${test_name}, expected=${expected}"
        echo "--- objbench log ---"
        cat "$log_file"
        exit 1
    fi
}

kill_gateway_by_port()
{
    local port=$1
    lsof -t -i :$port | xargs -r kill -9 >/dev/null 2>&1 || true
}

wait_tcp_ready()
{
    local host=$1
    local port=$2
    local timeout=${3:-30}
    for _ in $(seq 1 "$timeout"); do
        if (echo > /dev/tcp/${host}/${port}) >/dev/null 2>&1; then
            return
        fi
        sleep 1
    done
    echo "tcp ${host}:${port} is not ready in ${timeout} seconds"
    exit 1
}

ensure_mc_binary()
{
    if [[ -x ./mc ]]; then
        return
    fi
    local os_arch
    local cpu_arch
    cpu_arch=$(uname -m)
    if [[ "$(uname)" == "Darwin" ]]; then
        if [[ "$cpu_arch" == "arm64" ]]; then
            os_arch="darwin-arm64"
        else
            os_arch="darwin-amd64"
        fi
    else
        if [[ "$cpu_arch" == "aarch64" || "$cpu_arch" == "arm64" ]]; then
            os_arch="linux-arm64"
        else
            os_arch="linux-amd64"
        fi
    fi
    wget -q "https://dl.min.io/client/mc/release/${os_arch}/mc" -O ./mc
    chmod +x ./mc
}

generate_sha_manifest()
{
    local root_dir=$1
    local output_file=$2
    rm -f "$output_file"
    if [[ "$(uname)" == "Darwin" ]]; then
        while IFS= read -r rel; do
            sum=$(shasum -a 256 "$root_dir/$rel" | awk '{print $1}')
            echo "$sum  $rel" >> "$output_file"
        done < <(cd "$root_dir" && find . -type f | sort | sed 's#^\./##')
    else
        while IFS= read -r rel; do
            sum=$(sha256sum "$root_dir/$rel" | awk '{print $1}')
            echo "$sum  $rel" >> "$output_file"
        done < <(cd "$root_dir" && find . -type f | sort | sed 's#^\./##')
    fi
}

prepare_sync_source_tree()
{
    local src_dir=$1
    mkdir -p "$src_dir/dir1/dir2"
    echo "hello-juicefs" > "$src_dir/plain.txt"
    echo "with space" > "$src_dir/dir1/file with space.txt"
    echo "cifs-中文文件" > "$src_dir/dir1/中文文件.txt"
    : > "$src_dir/empty.file"
    dd if=/dev/urandom of="$src_dir/dir1/dir2/binary.bin" bs=1M count=4 >/dev/null 2>&1
}

skip_test_mount_process_exit_on_format()
{
    prepare_test
    echo "round $i"
    ./juicefs format $META_URL volume-$i
    ./juicefs mount -d $META_URL /tmp/myjfs$i_$j --no-usage-report
    cd /tmp/myjfs$i_$j
    bash -c 'for k in {1..300}; do echo abc>$k; sleep 0.2; done' || true & 
    cd -
    sleep 3
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid
    ./juicefs format $META_URL new-volume-$i 
    sleep 15   
    ps -ef | grep juicefs
    # TODO: fix the bug and remove the following line
    # SEE https://github.com/juicedata/juicefs/issues/4534
    pidof juicefs && exit 1
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force $META_URL $uuid
}

test_format_sftp_object()
{
    docker run -d --name sftp -p 2222:22 juicedata/ci-sftp
    prepare_test
    CONTAINER_IP=$(docker container inspect sftp --format '{{ .NetworkSettings.IPAddress }}')
    echo "round $i"
    ./juicefs format $META_URL volume-$i --storage sftp \
    --bucket $CONTAINER_IP:myjfs/ \
    --access-key testUser1 \
    --secret-key password
    ./juicefs mount -d $META_URL /tmp/jfs --no-usage-report --cache-size 0
    cd /tmp/jfs
    bash -c 'for k in {1..100}; do echo abc>$k; sleep 0.1; done' || true &
    bg_pid=$!
    cd -
    sleep 1
    docker stop sftp
    sleep 10
    docker start sftp
    sleep 2
    wait $bg_pid
    echo "Checking JuiceFS read/write"
    echo abc > /tmp/jfs/101
    for k in {1..100}; do
        if [[ $(cat /tmp/jfs/$k) != "abc" ]]; then
            echo "ERROR: File $k corrupted after SFTP restart!"
            exit 1
        fi
    done
    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    ./juicefs format $META_URL new-volume-$i
}

test_format_cifs_objbench_matrix()
{
    prepare_test
    start_smb_container
    local log_raw=/tmp/objbench-cifs-raw.log
    local log_plain=/tmp/objbench-cifs.log
    ./juicefs objbench --storage cifs \
        --access-key "$SMB_USER" \
        --secret-key "$SMB_PASSWORD" \
        --threads 2 \
        --small-objects 5 \
        --small-object-size 4K \
        --block-size 1M \
        --big-object-size 8M \
        "$SMB_ENDPOINT" 2>&1 | tee "$log_raw"

    sed -E 's/\x1B\[[0-9;]*[mK]//g' "$log_raw" > "$log_plain"

    assert_objbench_result "$log_plain" "create a bucket" "pass"
    assert_objbench_result "$log_plain" "put an object" "pass"
    assert_objbench_result "$log_plain" "get an object" "pass"
    assert_objbench_result "$log_plain" "get non-exist" "pass"
    assert_objbench_result "$log_plain" "get partial object" "pass"
    assert_objbench_result "$log_plain" "head an object" "pass"
    assert_objbench_result "$log_plain" "delete an object" "pass"
    assert_objbench_result "$log_plain" "delete non-exist" "pass"
    assert_objbench_result "$log_plain" "list objects" "pass"
    assert_objbench_result "$log_plain" "special key" "put encode file failed"
    assert_objbench_result "$log_plain" "put a big object" "pass"
    assert_objbench_result "$log_plain" "put an empty object" "pass"
    assert_objbench_result "$log_plain" "multipart upload" "not support"
    assert_objbench_result "$log_plain" "change owner/group" "failed to chown object"
    assert_objbench_result "$log_plain" "change permission" "expect mode 777 but got"
    assert_objbench_result "$log_plain" "change mtime" "pass"

    cleanup_smb_container
}

test_format_smb_object_alias()
{
    prepare_test
    start_smb_container
    local volume_name="smb-alias-$RANDOM"
    local mount_point="/tmp/jfs-smb-$RANDOM"
    ./juicefs format $META_URL "$volume_name" --storage smb \
        --bucket "$SMB_ENDPOINT" \
        --access-key "$SMB_USER" \
        --secret-key "$SMB_PASSWORD"

    mkdir -p "$mount_point"
    ./juicefs mount -d $META_URL "$mount_point" --no-usage-report --cache-size 0

    echo "smb-alias-ok" > "$mount_point/smb-alias.txt"
    read_content=$(cat "$mount_point/smb-alias.txt")
    [[ "$read_content" != "smb-alias-ok" ]] && echo "smb alias read/write check failed" && exit 1

    ./juicefs umount "$mount_point" || true
    rm -rf "$mount_point"

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    cleanup_smb_container
}

test_format_cifs_sync_consistency()
{
    prepare_test
    start_smb_container
    local volume_name="cifs-sync-$RANDOM"
    local mount_point="/tmp/jfs-cifs-sync-$RANDOM"
    local mount_data_dir
    local src_dir="/tmp/cifs-sync-src-$RANDOM"
    local dst_dir="/tmp/cifs-sync-dst-$RANDOM"
    local src_manifest="/tmp/cifs-sync-src-$RANDOM.sha256"
    local dst_manifest="/tmp/cifs-sync-dst-$RANDOM.sha256"

    ./juicefs format $META_URL "$volume_name" --storage cifs \
        --bucket "$SMB_ENDPOINT" \
        --access-key "$SMB_USER" \
        --secret-key "$SMB_PASSWORD"

    mkdir -p "$mount_point"
    ./juicefs mount -d $META_URL "$mount_point" --no-usage-report --cache-size 0
    mount_data_dir="$mount_point/sync-data"
    mkdir -p "$mount_data_dir"

    rm -rf "$src_dir" "$dst_dir"
    mkdir -p "$src_dir" "$dst_dir"
    prepare_sync_source_tree "$src_dir"

    ./juicefs sync "$src_dir/" "$mount_data_dir/" --threads 8 --dirs
    ./juicefs sync "$mount_data_dir/" "$dst_dir/" --threads 8 --dirs

    generate_sha_manifest "$src_dir" "$src_manifest"
    generate_sha_manifest "$dst_dir" "$dst_manifest"
    diff "$src_manifest" "$dst_manifest"

    src_count=$(find "$src_dir" -type f | wc -l | tr -d ' ')
    dst_count=$(find "$dst_dir" -type f | wc -l | tr -d ' ')
    [[ "$src_count" != "$dst_count" ]] && echo "sync file count mismatch: $src_count vs $dst_count" && exit 1

    ./juicefs umount "$mount_point" || true
    rm -rf "$mount_point" "$src_dir" "$dst_dir"

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    cleanup_smb_container
}

test_format_cifs_object_recovery()
{
    prepare_test
    start_smb_container
    local volume_name="cifs-recovery-$RANDOM"
    local mount_point="/tmp/jfs-cifs-recovery-$RANDOM"

    ./juicefs format $META_URL "$volume_name" --storage cifs \
        --bucket "$SMB_ENDPOINT" \
        --access-key "$SMB_USER" \
        --secret-key "$SMB_PASSWORD"

    mkdir -p "$mount_point"
    ./juicefs mount -d $META_URL "$mount_point" --no-usage-report --cache-size 0

    for k in {1..20}; do
        echo "before-restart-$k" > "$mount_point/before-$k.txt"
    done

    docker stop "$SMB_CONTAINER_NAME"
    sleep 8
    docker start "$SMB_CONTAINER_NAME"
    container_ip=$(docker container inspect "$SMB_CONTAINER_NAME" --format '{{ .NetworkSettings.IPAddress }}')
    wait_tcp_ready "$container_ip" 445 40
    sleep 3

    for k in {1..20}; do
        content=$(cat "$mount_point/before-$k.txt")
        [[ "$content" != "before-restart-$k" ]] && echo "file check failed after restart: before-$k.txt" && exit 1
    done
    echo "after-restart" > "$mount_point/after-restart.txt"
    [[ "$(cat "$mount_point/after-restart.txt")" != "after-restart" ]] && echo "write/read failed after cifs restart" && exit 1

    ./juicefs umount "$mount_point" || true
    rm -rf "$mount_point"

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    cleanup_smb_container
}

test_format_cifs_gateway_read_write()
{
    prepare_test
    start_smb_container
    ensure_mc_binary
    local volume_name="cifs-gateway-$RANDOM"
    local gateway_port=9015

    ./juicefs format $META_URL "$volume_name" --storage cifs \
        --bucket "$SMB_ENDPOINT" \
        --access-key "$SMB_USER" \
        --secret-key "$SMB_PASSWORD"

    kill_gateway_by_port $gateway_port
    export MINIO_ROOT_USER=admin
    export MINIO_ROOT_PASSWORD=admin123
    ./juicefs gateway $META_URL 127.0.0.1:${gateway_port} --multi-buckets --keep-etag --object-tag -background
    wait_tcp_ready 127.0.0.1 $gateway_port 30

    ./mc alias set cifsgw http://127.0.0.1:${gateway_port} admin admin123 --api S3v4
    ./mc mb cifsgw/test-cifs-gw
    echo "gateway-cifs-ok" > /tmp/cifs-gateway-file.txt
    ./mc cp /tmp/cifs-gateway-file.txt cifsgw/test-cifs-gw/cifs-gateway-file.txt
    ./mc cat cifsgw/test-cifs-gw/cifs-gateway-file.txt | grep "gateway-cifs-ok"

    ./mc rm cifsgw/test-cifs-gw/cifs-gateway-file.txt
    ./mc rb cifsgw/test-cifs-gw --force
    kill_gateway_by_port $gateway_port

    uuid=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --force $META_URL $uuid
    cleanup_smb_container
}

source .github/scripts/common/run_test.sh && run_test $@

