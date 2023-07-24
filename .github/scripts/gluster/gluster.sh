#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

pkg -s glusterfs-server || .github/scripts/apt_install.sh glusterfs-server
systemctl start glusterd.service

mkdir -p /data/brick/gv0
ip=$(ifconfig eth0 | grep 'inet ' |  awk '{ print $2 }')
echo ip is $ip
gluster volume create gv0 $ip:/data/brick/gv0
gluster volume start gv0
gluster volume info

./juicefs format $META_URL glusterfs-test --storage gluster --bucket $ip/gv0
./juicefs mount $META_URL /jfs -d
echo abc > /jfs/abc
cat /jfs/abc | grep abc


source .github/scripts/common/run_test.sh && run_test $@

