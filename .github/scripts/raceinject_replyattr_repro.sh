#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$ROOT_DIR"

MP="/tmp/jfs-raceinject"
BUCKET_DIR="/tmp/jfs-raceinject-bucket"
META_DB="$ROOT_DIR/raceinject-ci.db"
META_URL="sqlite3://$META_DB"
REPRO_SRC_FROM_SCRIPT="$SCRIPT_DIR/repro_reader_eof_race.c"
REPRO_SRC_IN_RACE="$ROOT_DIR/race/repro_reader_eof_race.c"
REPRO2_SRC_FROM_SCRIPT="$SCRIPT_DIR/repro_replyattr_stale_size_race.c"
REPRO2_SRC_IN_RACE="$ROOT_DIR/race/repro_replyattr_stale_size_race.c"
REPRO_BIN="$ROOT_DIR/race/repro_reader_eof"
REPRO2_BIN="$ROOT_DIR/race/repro_stale_size"

cleanup() {
  sudo ./juicefs umount "$MP" --force >/dev/null 2>&1 || true
  pkill -f "juicefs mount .*${MP}" >/dev/null 2>&1 || true
  rm -f "$REPRO_SRC_IN_RACE" "$REPRO2_SRC_IN_RACE" "$REPRO_BIN" "$REPRO2_BIN"
}
trap cleanup EXIT

echo "[raceinject] building juicefs with raceinject tag"
go build -tags raceinject -o juicefs .

if [[ ! -f "$REPRO_SRC_FROM_SCRIPT" ]]; then
  echo "[raceinject] missing reproducer source: $REPRO_SRC_FROM_SCRIPT"
  exit 1
fi

if [[ ! -f "$REPRO2_SRC_FROM_SCRIPT" ]]; then
  echo "[raceinject] missing reproducer source: $REPRO2_SRC_FROM_SCRIPT"
  exit 1
fi

mkdir -p "$ROOT_DIR/race"
cp -f "$REPRO_SRC_FROM_SCRIPT" "$REPRO_SRC_IN_RACE"
cp -f "$REPRO2_SRC_FROM_SCRIPT" "$REPRO2_SRC_IN_RACE"

echo "[raceinject] compiling reproducer #1 (stale size race)"
gcc -O2 -pthread -o "$REPRO2_BIN" "$REPRO2_SRC_IN_RACE"

echo "[raceinject] compiling reproducer #2 (reader eof race)"
gcc -O2 -pthread -o "$REPRO_BIN" "$REPRO_SRC_IN_RACE"

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

echo "[raceinject] running reproducer #1 (stale size race)"
set +e
REPRO_PRECLOSE_MS=400 REPRO_ITERS=8 "$REPRO2_BIN" 1
rc2=$?
set -e

if [[ $rc2 -eq 1 ]]; then
  echo "[raceinject] reproducer #2 hit old race (expected on unfixed code)"
  exit 1
elif [[ $rc2 -eq 0 ]]; then
  echo "[raceinject] reproducer #2 passed (race not reproduced)"
else
  echo "[raceinject] reproducer #2 failed with exit code $rc2"
  exit "$rc2"
fi

echo "[raceinject] running reproducer #2 (reader eof race)"
set +e
REPRO_SLEEP_MS=2000 REPRO_PRECLOSE_MS=400 REPRO_ITERS=5 "$REPRO_BIN" 1
rc=$?
set -e

if [[ $rc -eq 1 ]]; then
  echo "[raceinject] reproducer #1 hit old race (expected on unfixed code)"
  exit 1
elif [[ $rc -eq 0 ]]; then
  echo "[raceinject] reproducer #1 passed (race not reproduced)"
  exit 0
else
  echo "[raceinject] reproducer #1 failed with exit code $rc"
  exit "$rc"
fi