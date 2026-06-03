#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
AWS_BIN=${AWS_BIN:-aws}
ZEROFS_DIR=${ZEROFS_DIR:-}
ZEROFS_BIN=${ZEROFS_BIN:-}
JUICEFS_BIN=${JUICEFS_BIN:-}
ZEROFS_RUSTFLAGS=${ZEROFS_RUSTFLAGS:-${RUSTFLAGS:-}}

TMPDIR_SMOKE=$(mktemp -d "${TMPDIR:-/tmp}/juicefs-zerofs-smoke.XXXXXX")
GATEWAY_LOG="${TMPDIR_SMOKE}/gateway.log"
ZEROFS_LOG="${TMPDIR_SMOKE}/zerofs.log"
AWS_CONFIG_FILE="${TMPDIR_SMOKE}/aws-config"
AWS_SHARED_CREDENTIALS_FILE="${TMPDIR_SMOKE}/aws-credentials"
GATEWAY_PID=""
ZEROFS_PID=""

cleanup() {
    local status=$?

    if [[ -n "${ZEROFS_PID}" ]] && kill -0 "${ZEROFS_PID}" 2>/dev/null; then
        kill "${ZEROFS_PID}" 2>/dev/null || true
        wait "${ZEROFS_PID}" 2>/dev/null || true
    fi
    if [[ -n "${GATEWAY_PID}" ]] && kill -0 "${GATEWAY_PID}" 2>/dev/null; then
        kill "${GATEWAY_PID}" 2>/dev/null || true
        wait "${GATEWAY_PID}" 2>/dev/null || true
    fi

    if [[ ${status} -ne 0 ]]; then
        if [[ -f "${GATEWAY_LOG}" ]]; then
            echo "gateway log:" >&2
            cat "${GATEWAY_LOG}" >&2 || true
        fi
        if [[ -f "${ZEROFS_LOG}" ]]; then
            echo "zerofs log:" >&2
            cat "${ZEROFS_LOG}" >&2 || true
        fi
        echo "artifacts kept in ${TMPDIR_SMOKE}" >&2
        return
    fi

    rm -rf "${TMPDIR_SMOKE}"
}
trap cleanup EXIT INT TERM

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing required command: $1" >&2
        exit 1
    fi
}

pick_port() {
    python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

wait_for_command() {
    local timeout_secs=$1
    shift
    local deadline=$((SECONDS + timeout_secs))
    while (( SECONDS < deadline )); do
        if "$@" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_for_log() {
    local logfile=$1
    local pattern=$2
    local timeout_secs=$3
    local deadline=$((SECONDS + timeout_secs))
    while (( SECONDS < deadline )); do
        if grep -q "${pattern}" "${logfile}" 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}

require_command python3
require_command "${AWS_BIN}"

if [[ -z "${JUICEFS_BIN}" ]]; then
    require_command go
    JUICEFS_BIN="${TMPDIR_SMOKE}/juicefs"
    (cd "${ROOT_DIR}" && go build -o "${JUICEFS_BIN}" .)
fi

if [[ -z "${ZEROFS_BIN}" ]]; then
    require_command cargo
    if [[ -z "${ZEROFS_DIR}" ]]; then
        echo "set ZEROFS_DIR or ZEROFS_BIN to run the ZeroFS smoke test" >&2
        exit 1
    fi
    if [[ -f "${ZEROFS_DIR}/zerofs/Cargo.toml" ]]; then
        ZEROFS_MANIFEST="${ZEROFS_DIR}/zerofs/Cargo.toml"
        ZEROFS_BIN="${ZEROFS_DIR}/zerofs/target/debug/zerofs"
    elif [[ -f "${ZEROFS_DIR}/Cargo.toml" ]]; then
        ZEROFS_MANIFEST="${ZEROFS_DIR}/Cargo.toml"
        ZEROFS_BIN="$(dirname "${ZEROFS_MANIFEST}")/target/debug/zerofs"
    else
        echo "unable to find ZeroFS Cargo manifest under ${ZEROFS_DIR}" >&2
        exit 1
    fi
    if [[ "${ZEROFS_RUSTFLAGS}" != *tokio_unstable* ]]; then
        ZEROFS_RUSTFLAGS="${ZEROFS_RUSTFLAGS:+${ZEROFS_RUSTFLAGS} }--cfg tokio_unstable"
    fi
    RUSTFLAGS="${ZEROFS_RUSTFLAGS}" cargo build --manifest-path "${ZEROFS_MANIFEST}" --bin zerofs >/dev/null
fi

GATEWAY_PORT=${GATEWAY_PORT:-$(pick_port)}
ZEROFS_RPC_PORT=${ZEROFS_RPC_PORT:-$(pick_port)}
ENDPOINT="http://127.0.0.1:${GATEWAY_PORT}"
META_URL="sqlite3://${TMPDIR_SMOKE}/gateway.db"
BACKING_DIR="${TMPDIR_SMOKE}/gateway-data"
CACHE_DIR="${TMPDIR_SMOKE}/cache"
BUCKET="zerofs-smoke-$(date +%s)-$$"
PREFIX="data"
ZEROFS_CONFIG="${TMPDIR_SMOKE}/zerofs.toml"

mkdir -p "${BACKING_DIR}" "${CACHE_DIR}"

cat >"${AWS_SHARED_CREDENTIALS_FILE}" <<EOF
[default]
aws_access_key_id = testUser
aws_secret_access_key = testUserPassword
EOF

cat >"${AWS_CONFIG_FILE}" <<EOF
[default]
region = us-east-1
s3 =
    addressing_style = path
EOF

export AWS_CONFIG_FILE
export AWS_SHARED_CREDENTIALS_FILE
export AWS_EC2_METADATA_DISABLED=true
export MINIO_ROOT_USER=testUser
export MINIO_ROOT_PASSWORD=testUserPassword

"${JUICEFS_BIN}" format --force --bucket "${BACKING_DIR}" "${META_URL}" gateway-volume >/dev/null
"${JUICEFS_BIN}" gateway "${META_URL}" "127.0.0.1:${GATEWAY_PORT}" --multi-buckets --keep-etag --object-tag --no-usage-report >"${GATEWAY_LOG}" 2>&1 &
GATEWAY_PID=$!

if ! wait_for_command 30 "${AWS_BIN}" --endpoint-url "${ENDPOINT}" s3api list-buckets; then
    echo "juicefs S3 gateway did not become ready in time" >&2
    exit 1
fi

"${AWS_BIN}" --endpoint-url "${ENDPOINT}" s3api create-bucket --bucket "${BUCKET}" >/dev/null

cat >"${ZEROFS_CONFIG}" <<EOF
[cache]
dir = "${CACHE_DIR}"
disk_size_gb = 0.1
memory_size_gb = 0.1

[storage]
url = "s3://${BUCKET}/${PREFIX}"
encryption_password = "juicefs-zerofs-smoke-password"

[servers.rpc]
addresses = ["127.0.0.1:${ZEROFS_RPC_PORT}"]

[aws]
access_key_id = "testUser"
secret_access_key = "testUserPassword"
endpoint = "${ENDPOINT}"
default_region = "us-east-1"
allow_http = "true"

[telemetry]
enabled = false
EOF

"${ZEROFS_BIN}" run -c "${ZEROFS_CONFIG}" >"${ZEROFS_LOG}" 2>&1 &
ZEROFS_PID=$!

deadline=$((SECONDS + 60))
while (( SECONDS < deadline )); do
    if ! kill -0 "${ZEROFS_PID}" 2>/dev/null; then
        echo "ZeroFS exited before reaching ready state" >&2
        exit 1
    fi
    if grep -q "Storage provider compatibility check passed" "${ZEROFS_LOG}" 2>/dev/null &&
        grep -q "RPC server listening on" "${ZEROFS_LOG}" 2>/dev/null; then
        break
    fi
    sleep 1
done

if ! wait_for_log "${ZEROFS_LOG}" "Storage provider compatibility check passed" 1; then
    echo "ZeroFS did not confirm native conditional write support" >&2
    exit 1
fi
if ! wait_for_log "${ZEROFS_LOG}" "RPC server listening on" 1; then
    echo "ZeroFS did not start its RPC server" >&2
    exit 1
fi

"${AWS_BIN}" --endpoint-url "${ENDPOINT}" s3api head-object --bucket "${BUCKET}" --key "${PREFIX}/.zerofs_bucket_id" >/dev/null
"${AWS_BIN}" --endpoint-url "${ENDPOINT}" s3api head-object --bucket "${BUCKET}" --key "${PREFIX}/zerofs.key" >/dev/null

kill "${ZEROFS_PID}"
wait "${ZEROFS_PID}" 2>/dev/null || true
ZEROFS_PID=""

echo "ZeroFS smoke test passed against JuiceFS S3 gateway without conditional_put"
