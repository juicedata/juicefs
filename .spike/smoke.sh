#!/bin/bash
# FUSE mount smoke test: slatedb meta (file:// store), file data storage
set -e
export LD_LIBRARY_PATH=/opt/slatedb
B=/out/juicefs
# format with durable commits (short-lived process; memory-durability writes can
# be lost if the process exits before the next WAL flush); mount with memory durability
META_FMT="slatedb:///tmp/jfs-meta"
META="slatedb:///tmp/jfs-meta?durability=memory"
MP=/mnt/jfs

echo "== format =="
$B format "$META_FMT" smoke --storage file --bucket /tmp/jfs-data --trash-days 0 2>&1 | tail -2

echo "== mount =="
$B mount -d "$META" $MP --no-usage-report || { tail -50 /root/.juicefs/juicefs.log 2>/dev/null; exit 1; }
mount | grep " $MP " || { tail -50 /root/.juicefs/juicefs.log 2>/dev/null; exit 1; }

echo "== file ops =="
echo hello > $MP/hello.txt
mkdir -p $MP/dir/sub
cp $MP/hello.txt $MP/dir/sub/copy.txt
dd if=/dev/urandom of=$MP/big bs=1M count=8 status=none
MD5_1=$(md5sum $MP/big | cut -d' ' -f1)
mv $MP/big $MP/dir/big
ln -s dir/sub/copy.txt $MP/link
[ "$(cat $MP/link)" = "hello" ]
touch $MP/dir/sub/x && rm $MP/dir/sub/x
ls -lR $MP > /dev/null
df -h $MP | tail -1

echo "== unmount, remount, verify persistence =="
$B umount $MP
sleep 1
$B mount -d "$META" $MP --no-usage-report
[ "$(cat $MP/hello.txt)" = "hello" ]
MD5_2=$(md5sum $MP/dir/big | cut -d' ' -f1)
[ "$MD5_1" = "$MD5_2" ] || { echo "MD5 MISMATCH $MD5_1 != $MD5_2"; exit 1; }
[ "$(cat $MP/link)" = "hello" ]
rm -rf $MP/dir
[ ! -e $MP/dir ]
$B umount $MP
echo "SMOKE_OK (8MiB md5=$MD5_1 survived remount)"
