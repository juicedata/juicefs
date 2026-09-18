#!/bin/bash
# FUSE mount smoke test against real MinIO: slatedb meta on s3://jfs-meta (durable
# commits, default), juicefs data on minio://jfs-data
set -e
export LD_LIBRARY_PATH=/opt/slatedb
export AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin AWS_REGION=us-east-1
export AWS_ENDPOINT=http://jfs-minio:9000 AWS_ALLOW_HTTP=true AWS_VIRTUAL_HOSTED_STYLE_REQUEST=false
B=/out/juicefs
META="slatedb://s3://jfs-meta/db1"
MP=/mnt/jfs

echo "== format =="
$B format "$META" miniosmoke --storage minio --bucket http://jfs-minio:9000/jfs-data \
  --access-key minioadmin --secret-key minioadmin --trash-days 0 2>&1 | tail -2

echo "== mount (durable commits) =="
$B mount -d "$META" $MP --no-usage-report || { tail -50 /root/.juicefs/juicefs.log 2>/dev/null; exit 1; }
mount | grep " $MP " || { tail -50 /root/.juicefs/juicefs.log 2>/dev/null; exit 1; }

echo "== file ops =="
echo hello-minio > $MP/hello.txt
dd if=/dev/urandom of=$MP/blob bs=1M count=4 status=none
MD5_1=$(md5sum $MP/blob | cut -d' ' -f1)
mkdir $MP/d1

echo "== 50 serial mkdir through FUSE, each a durable commit =="
START=$(date +%s%N)
for i in $(seq 1 50); do mkdir $MP/d1/dir$i; done
END=$(date +%s%N)
TOTAL_MS=$(( (END-START)/1000000 ))
echo "RESULT: 50 mkdirs in ${TOTAL_MS} ms => $((TOTAL_MS/50)) ms/op avg"

echo "== unmount, remount, verify persistence =="
$B umount $MP
sleep 1
$B mount -d "$META" $MP --no-usage-report
[ "$(cat $MP/hello.txt)" = "hello-minio" ]
[ "$(md5sum $MP/blob | cut -d' ' -f1)" = "$MD5_1" ] || { echo "MD5 MISMATCH"; exit 1; }
[ -d $MP/d1/dir50 ]
$B umount $MP
echo "MINIO_SMOKE_OK"
