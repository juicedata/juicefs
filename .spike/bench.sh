#!/bin/bash
# mdtest-style metadata benchmark across engines.
# Workload: 31 dirs (serial), then 8 threads x 10 files x 31 dirs = 2480 creates.
set -e
export LD_LIBRARY_PATH=/opt/slatedb
export AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin AWS_REGION=us-east-1
export AWS_ENDPOINT=http://jfs-minio:9000 AWS_ALLOW_HTTP=true AWS_VIRTUAL_HOSTED_STYLE_REQUEST=false
B=/out/juicefs
mkdir -p /tmp/bench

# format always uses a durable URL (short-lived process; memory-durability writes
# can be lost if the process exits before the next WAL flush)
run() {
  local name=$1 fmt_url=$2 url=$3
  echo "===== $name ====="
  if ! $B format "$fmt_url" "bench-$name" --storage file --bucket "/tmp/bench/data-$name" \
      --trash-days 0 >/tmp/fmt.log 2>&1; then
    cat /tmp/fmt.log; exit 1
  fi
  $B mdtest "$url" /m --threads 8 --dirs 5 --depth 2 --files 10 2>&1 | grep -E "Created [0-9]+ (dirs|files)"
}

run badger        "badger:///tmp/bench/badger"  "badger:///tmp/bench/badger"
run sqlite3       "sqlite3:///tmp/bench/sq.db"  "sqlite3:///tmp/bench/sq.db"
run sl-mem        "slatedb:///tmp/bench/sl-mem" "slatedb:///tmp/bench/sl-mem?durability=memory"
run sl-dur-100ms  "slatedb:///tmp/bench/sl-dur" "slatedb:///tmp/bench/sl-dur"
run sl-dur-5ms    "slatedb:///tmp/bench/sl-tuned" 'slatedb:///tmp/bench/sl-tuned?settings={"flush_interval":"5ms"}'
run sl-minio-mem  "slatedb://s3://jfs-meta/bench-mem" "slatedb://s3://jfs-meta/bench-mem?durability=memory"
run sl-minio-dur  "slatedb://s3://jfs-meta/bench-dur" "slatedb://s3://jfs-meta/bench-dur"
echo BENCH_DONE
