#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

MP="/tmp/jfs-raceinject"
BUCKET_DIR="/tmp/jfs-raceinject-bucket"
META_DB="$ROOT_DIR/raceinject-ci.db"
META_URL="sqlite3://$META_DB"
REPRO_BIN="$ROOT_DIR/race/repro"

cleanup() {
  sudo ./juicefs umount "$MP" --force >/dev/null 2>&1 || true
  pkill -f "juicefs mount .*${MP}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "[raceinject] building juicefs with raceinject tag"
go build -tags raceinject -o juicefs .

echo "[raceinject] compiling reproducer"
gcc -O2 -pthread -o "$REPRO_BIN" race/repro_reader_eof_race.c

rm -f "$META_DB"
sudo mkdir -p "$BUCKET_DIR" "$MP"
sudo chmod 777 "$BUCKET_DIR" "$MP"

echo "[raceinject] formatting volume"
sudo ./juicefs format "$META_URL" --trash-days 0 --bucket="$BUCKET_DIR" raceinject >/dev/null

echo "[raceinject] mounting volume"
sudo env JUICEFS_REPLY_ATTR_RACE_SLEEP=2s ./juicefs mount -d "$META_URL" "$MP" --no-usage-report

for i in $(seq 1 20); do
  if [[ -f "$MP/.accesslog" ]]; then
    break
  fi
  sleep 1
done

if [[ ! -f "$MP/.accesslog" ]]; then
  echo "[raceinject] mount readiness check failed: $MP/.accesslog not found"
  exit 1
fi

cd "$MP"
mkdir -p d1

echo "[raceinject] running reproducer"
set +e
REPRO_SLEEP_MS=2000 REPRO_PRECLOSE_MS=400 REPRO_ITERS=5 "$REPRO_BIN" 1
rc=$?
set -e

if [[ $rc -eq 1 ]]; then
  echo "[raceinject] race reproduced as expected on unfixed code"
  exit 0
elif [[ $rc -eq 0 ]]; then
  echo "[raceinject] race was not reproduced"
  exit 1
else
  echo "[raceinject] reproducer failed with exit code $rc"
  exit "$rc"
fi