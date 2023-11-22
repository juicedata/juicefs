#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$STORAGE" ]] && STORAGE=gluster
source .github/scripts/start_meta_engine.sh
start_meta_engine $META $STORAGE
META_URL=$(get_meta_url $META)

test_with_big_file(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    dd if=/dev/urandom of=/tmp/bigfile bs=1M count=1024
    cp /tmp/bigfile /jfs/bigfile
    umount_jfs /jfs $META_URL
    rm -rf /var/jfsCache/myjfs
    ./juicefs mount $META_URL /jfs -d
    compare_md5sum /tmp/bigfile /jfs/bigfile
    rm -rf /tmp/bigfile
}

test_with_fio(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    dpkg -s fio || .github/scripts/apt_install.sh fio
    fio --name=randrw --ioengine=sync --time_based=1 --runtime=60 --group_reporting  \
        --bs=128k --filesize=128M --numjobs=1 --rw=randrw --verify=md5 --filename=/jfs/fio
}

test_random_read_write(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    PATH1=/tmp/test PATH2=/jfs/random_read_write python3 .github/scripts/random_read_write.py 
}

test_with_fsx(){
    prepare_test
    ./juicefs mount $META_URL /jfs -d
    dpkg -s libacl1-dev || sudo .github/scripts/apt_install.sh  libacl1-dev
    [ ! -d secfs.test ] && git clone https://github.com/billziss-gh/secfs.test.git
    make -C secfs.test >secfs.test-build-integration.log 2>&1
	secfs.test/tools/bin/fsx -d 180 -p 10000 -F 100000 /jfs/fsx
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

