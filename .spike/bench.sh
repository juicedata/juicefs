#!/bin/bash
# mdtest-style metadata benchmark across engines.
# Workload: 31 dirs (serial), then 8 threads x 10 files x 31 dirs = 2480 creates.
#
# Usage: bench.sh [run-id]
# Each run-id uses its own volumes, so the script can be run repeatedly against
# the same host and object store to take the minimum across runs.
set -e
export LD_LIBRARY_PATH=/opt/slatedb
export AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin AWS_REGION=us-east-1
export AWS_ENDPOINT=http://jfs-minio:9000 AWS_ALLOW_HTTP=true AWS_VIRTUAL_HOSTED_STYLE_REQUEST=false
B=/out/juicefs
ID=${1:-1}
mkdir -p /tmp/bench

# format always uses a durable URL (short-lived process; memory-durability writes
# can be lost if the process exits before the next WAL flush)
run() {
  local name=$1 fmt_url=$2 url=$3
  echo "===== $name (run $ID) ====="
  if ! $B format "$fmt_url" "bench-$name" --storage file --bucket "/tmp/bench/data-$name-$ID" \
      --trash-days 0 >/tmp/fmt.log 2>&1; then
    cat /tmp/fmt.log; exit 1
  fi
  $B mdtest "$url" /m --threads 8 --dirs 5 --depth 2 --files 10 2>&1 | grep -E "Created [0-9]+ (dirs|files)"
}

run badger        "badger:///tmp/bench/badger-$ID"  "badger:///tmp/bench/badger-$ID"
run sqlite3       "sqlite3:///tmp/bench/sq-$ID.db"  "sqlite3:///tmp/bench/sq-$ID.db"
run sl-mem        "slatedb:///tmp/bench/sl-mem-$ID" "slatedb:///tmp/bench/sl-mem-$ID?durability=memory"
run sl-dur-100ms  "slatedb:///tmp/bench/sl-dur-$ID" "slatedb:///tmp/bench/sl-dur-$ID"
run sl-dur-5ms    "slatedb:///tmp/bench/sl-tuned-$ID" 'slatedb:///tmp/bench/sl-tuned-'"$ID"'?settings={"flush_interval":"5ms"}'
run sl-minio-mem  "slatedb://s3://jfs-meta/bench-mem-$ID" "slatedb://s3://jfs-meta/bench-mem-$ID?durability=memory"
run sl-minio-dur  "slatedb://s3://jfs-meta/bench-dur-$ID" "slatedb://s3://jfs-meta/bench-dur-$ID"
echo BENCH_DONE
