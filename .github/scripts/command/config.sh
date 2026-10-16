#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META minio
META_URL=$(get_meta_url $META)
# version lower than 1.3.0 does not support parameter max_open_conns
if [[ $META_URL == *"?max_open_conns="* ]]; then
    META_URL=${META_URL%%\?*}
fi
LEGACY_META_URL=$META_URL
if [[ "$META" == "redis" ]]; then
    LEGACY_META_URL=${META_URL%%\?*}
fi
[ ! -x mc ] && .github/scripts/download_mc.sh linux-amd64 ./mc && chmod +x mc

download_juicefs_client(){
    version=$1
    wget -q https://github.com/juicedata/juicefs/releases/download/v$version/juicefs-$version-linux-amd64.tar.gz
    tar -xzf juicefs-$version-linux-amd64.tar.gz -C /tmp/
    sudo cp /tmp/juicefs juicefs-$version
    ./juicefs-$version version
}

test_config_min_client_version()
{
    prepare_test
    download_juicefs_client 1.0.0
    ./juicefs format $META_URL myjfs
    ./juicefs-1.0.0 mount $LEGACY_META_URL /jfs -d && exit 1 || true
    if ./juicefs config $META_URL --min-client-version 1.0.1; then
        echo "<FATAL>: expect min-client-version downgrade to fail"
        exit 1
    fi
    ./juicefs-1.0.0 mount $LEGACY_META_URL /jfs -d && exit 1 || true
}

test_config_max_client_version()
{
    prepare_test
    current_version=$(./juicefs version | awk '{print $3}')
    download_juicefs_client 1.0.0
    ./juicefs-1.0.0 format $LEGACY_META_URL myjfs
    ./juicefs-1.0.0 config $LEGACY_META_URL --max-client-version 1.0.1
    ./juicefs mount $META_URL /jfs -d && exit 1 || true
    if ./juicefs config $META_URL --max-client-version $current_version; then
        echo "<FATAL>: expect max-client-version update by a disallowed client to fail"
        exit 1
    fi
    ./juicefs config $META_URL --max-client-version $current_version --force
    ./juicefs mount $META_URL /jfs -d
}

test_config_secret_key(){
    # # Consider command as failed when any component of the pipe fails:
    # https://stackoverflow.com/questions/1221833/pipe-output-and-capture-exit-status-in-bash
    prepare_test
    set -o pipefail
    ./mc alias set minio http://127.0.0.1:9000 minioadmin minioadmin
    ./mc admin user add minio juicedata juicedata
    ./mc admin policy attach minio consoleAdmin --user juicedata
    ./juicefs format --storage minio --bucket http://localhost:9000/jfs-test --access-key juicedata --secret-key juicedata $META_URL myjfs
    ./juicefs mount $META_URL /jfs -d --io-retries 1 --no-usage-report --heartbeat 3

    ./mc admin user remove minio juicedata
    ./mc admin user add minio juicedata1 juicedata1
    ./mc admin policy attach minio consoleAdmin --user juicedata1
    ./juicefs config $META_URL --access-key juicedata1 --secret-key juicedata1
    sleep 6
    echo abc | tee /jfs/abc.txt && echo "write success"
    cat /jfs/abc.txt | grep abc && echo "read success"
}

test_reload_storage_after_transient_create_failure()
{
    if [[ "$META" != "redis" ]]; then
        echo "skip reload retry regression for META=$META; redis coverage is sufficient"
        return
    fi

    prepare_test
    RELOAD_OLD_BUCKET="reload-old-$$"
    RELOAD_NEW_BUCKET="reload-new-$$"
    RELOAD_TLS_DIR=$(mktemp -d /tmp/jfs-reload-tls.XXXXXX)
    RELOAD_STAGED_DIR="$RELOAD_TLS_DIR/staged"
    RELOAD_LIVE_DIR="$RELOAD_TLS_DIR/live"
    RELOAD_MOUNT_LOG="$RELOAD_TLS_DIR/mount.log"
    mkdir -p "$RELOAD_STAGED_DIR" "$RELOAD_LIVE_DIR"

    cleanup_reload_retry_test()
    {
        umount_jfs /jfs "$META_URL" || true
        ./mc rb --force "myminio/$RELOAD_OLD_BUCKET" >/dev/null 2>&1 || true
        ./mc rb --force "myminio/$RELOAD_NEW_BUCKET" >/dev/null 2>&1 || true
        rm -rf "$RELOAD_TLS_DIR"
    }
    trap cleanup_reload_retry_test EXIT

    ./mc alias set myminio http://127.0.0.1:9000 minioadmin minioadmin
    ./mc mb --ignore-existing "myminio/$RELOAD_OLD_BUCKET"
    ./mc mb --ignore-existing "myminio/$RELOAD_NEW_BUCKET"
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
        -keyout "$RELOAD_STAGED_DIR/client.key" -out "$RELOAD_STAGED_DIR/client.pem" >/dev/null 2>&1
    cp "$RELOAD_STAGED_DIR/client.pem" "$RELOAD_STAGED_DIR/ca.pem"

    ./juicefs format --storage minio \
        --bucket "http://127.0.0.1:9000/$RELOAD_OLD_BUCKET" \
        --access-key minioadmin --secret-key minioadmin \
        "$META_URL" myjfs
    ./juicefs mount -d "$META_URL" /jfs --heartbeat 1s --cache-size 0 \
        --no-usage-report --log "$RELOAD_MOUNT_LOG"

    for i in $(seq 1 20); do
        is_mounted /jfs && break
        if [[ "$i" -eq 20 ]]; then
            cat "$RELOAD_MOUNT_LOG"
            echo "<FATAL>: mount did not become ready"
            exit 1
        fi
        sleep 1
    done

    dd if=/dev/urandom of=/jfs/before-reload bs=1M count=1 status=none
    sync
    for i in $(seq 1 20); do
        if ./mc ls --recursive "myminio/$RELOAD_OLD_BUCKET" | grep -q .; then
            break
        fi
        if [[ "$i" -eq 20 ]]; then
            echo "<FATAL>: baseline write did not reach the original MinIO bucket"
            exit 1
        fi
        sleep 1
    done

    local pending_bucket="http://127.0.0.1:9000/$RELOAD_NEW_BUCKET?ca-certs=$RELOAD_LIVE_DIR/ca.pem&ssl-cert=$RELOAD_LIVE_DIR/client.pem&ssl-key=$RELOAD_LIVE_DIR/client.key"
    ./juicefs config "$META_URL" --force --yes --bucket "$pending_bucket"

    for i in $(seq 1 10); do
        if grep -Fq "error loading certificate and key file" "$RELOAD_MOUNT_LOG"; then
            echo "observed reload createStorage failure"
            break
        fi
        if [[ "$i" -eq 10 ]]; then
            cat "$RELOAD_MOUNT_LOG"
            echo "<FATAL>: reload did not hit the createStorage error path"
            exit 1
        fi
        sleep 1
    done

    cp "$RELOAD_STAGED_DIR/ca.pem" "$RELOAD_LIVE_DIR/ca.pem"
    cp "$RELOAD_STAGED_DIR/client.pem" "$RELOAD_LIVE_DIR/client.pem"
    cp "$RELOAD_STAGED_DIR/client.key" "$RELOAD_LIVE_DIR/client.key"
    echo "restored the exact storage configuration without publishing another format"

    for i in $(seq 1 8); do
        dd if=/dev/urandom of="/jfs/retry-probe-$i" bs=1M count=1 status=none
        sync
        if ./mc ls --recursive "myminio/$RELOAD_NEW_BUCKET" | grep -q .; then
            echo "mount switched to the recovered MinIO bucket after $i heartbeat probes"
            return
        fi
        echo "heartbeat probe $i still used the old storage"
        sleep 1
    done

    echo "<FATAL>: mount never retried the failed storage reload"
    echo "old bucket objects:"
    ./mc ls --recursive "myminio/$RELOAD_OLD_BUCKET" || true
    echo "new bucket objects:"
    ./mc ls --recursive "myminio/$RELOAD_NEW_BUCKET" || true
    cat "$RELOAD_MOUNT_LOG"
    exit 1
}
          

source .github/scripts/common/run_test.sh && run_test $@
