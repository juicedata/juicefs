#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$STORAGE" ]] && STORAGE=gluster
source .github/scripts/start_meta_engine.sh
start_meta_engine $META $STORAGE
META_URL=$(get_meta_url $META)

test_write_file(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    echo abc > /jfs/abc
    cat /jfs/abc | grep abc
}

test_write_file2(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    echo def > /jfs/def
    cat /jfs/def | grep def
}

prepare_test(){
    umount_jfs /jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfsCache/myjfs
    if [[ "$STORAGE" == "minio" ]]; then
        (./mc rb myminio/myjfs > /dev/null 2>&1 --force || true) && ./mc mb myminio/myjfs
        ./juicefs format $META_URL myjfs --storage minio --bucket http://localhost:9000/myjfs \
            --access-key minioadmin --secret-key minioadmin
    elif [[ "$STORAGE" == "gluster" ]]; then
        if gluster volume info gv0 > /dev/null 2>&1; then
            gluster volume stop gv0 <<< y
            sleep 3s
            gluster volume delete gv0 <<< y
            echo "Volume gv0 is deleted"
        fi
        rm -rf /data/brick/gv0 && mkdir -p /data/brick/gv0
        ip=$(ifconfig eth0 | grep 'inet ' |  awk '{ print $2 }')
        echo ip is $ip
        gluster volume create gv0 $ip:/data/brick/gv0 force
        gluster volume start gv0
        gluster volume info
        ./juicefs format $META_URL myjfs --storage gluster --bucket $ip/gv0 
    fi
}

source .github/scripts/common/run_test.sh && run_test $@

