#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

pkg -s glusterfs-server || .github/scripts/apt_install.sh glusterfs-server
systemctl start glusterd.service

mkdir -p /data/brick/gv0
gluster volume create gv0 replica localhost:/data/brick/gv0
gluster volume start gv0
gluster volume info

./juicefs.gluster format $META_URL glusterfs-test --storage gluster --bucket localhost/gv0

skip_install_gluster_with_docker()
{
    cd .github/scripts/gluster
    docker compose up -d glusterfs1 glusterfs2 glusters3
    echo "Sleep 10 seconds to wait the glusterfs up"
    sleep 10
    docker compose exec glusterfs1 gluster peer probe glusterfs2
    docker compose exec glusterfs1 gluster volume create test-volume replica 2 transport tcp \
        glusterfs1:/data/glusterfs/test \
        glusterfs2:/data/glusterfs/test force
    docker compose exec glusterfs1 gluster volume start test-volume
    docker compose exec glusterfs1 setfacl -m u:1000:rwx /data/glusterfs/test
    docker compose exec glusterfs2 setfacl -m u:1000:rwx /data/glusterfs/test
    docker compose exec glusterfs1 cat /var/log/glusterfs/bricks/data-glusterfs-test.log
    ./juicefs.gluster format $META_URL gfs-test --storage gluster --bucket glusterfs1,glusterfs2/test-volume
}


source .github/scripts/common/run_test.sh && run_test $@

