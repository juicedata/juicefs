#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META1" ]] && META1=sqlite3
[[ -z "$META2" ]] && META2=redis
source .github/scripts/start_meta_engine.sh
start_meta_engine $META1
start_meta_engine $META2
META_URL1=$(get_meta_url $META1)
META_URL2=$(get_meta_url $META2)
[[ -z "$SEED" ]] && SEED=$(date +%s)

# [[ -z "$SEED" ]] && SEED=1711594639
[[ -z "$BINARY" ]] && BINARY=false
[[ -z "$FAST" ]] && FAST=false

trap "echo random seed is $SEED" EXIT

if ! docker ps | grep -q minio; then
    docker run -d -p 9000:9000 --name minio \
            -e "MINIO_ACCESS_KEY=minioadmin" \
            -e "MINIO_SECRET_KEY=minioadmin" \
            -v /tmp/data:/data \
            -v /tmp/config:/root/.minio \
            minio/minio server /data
fi
[[ ! -f /usr/local/bin/mc ]] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc -O /usr/local/bin/mc && chmod +x /usr/local/bin/mc
sleep 3s
mc alias set myminio http://localhost:9000 minioadmin minioadmin
python3 -c "import xattr" || sudo pip install xattr

test_dump_load_with_random_test()
{
    prepare_test
    ./juicefs format $META_URL myjfs --enable-acl
    ./juicefs mount -d $META_URL /jfs 
    ./random-test runOp -baseDir /jfs/test -files 500000 -ops 5000000 -threads 50 -dirSize 100 -duration 30s -createOp 30,uniform -deleteOp 5,end --linkOp 10,uniform --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform    
    ./juicefs dump $META_URL dump.json $(get_dump_option)
    create_database $META_URL2
    ./juicefs load $META_URL2 dump.json $(get_load_option)
    ./juicefs dump $META_URL2 dump2.json $(get_dump_option)
    ./juicefs mount -d $META_URL2 /jfs2
    diff -ur /jfs/test /jfs2/test --no-dereference
    diff -ur /jfs/.trash /jfs2/.trash --no-dereference
    # compare_stat_acl_xattr /jfs/test /jfs2/test
    umount_jfs /jfs2 $META_URL2
    ./juicefs status $META_URL2 && UUID=$(./juicefs status $META_URL2 | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --yes $META_URL2 $UUID
}

test_dump_load_with_clone()
{
    prepare_test
    ./juicefs format $META_URL1 myjfs --trash-days 0 --enable-acl
    ./juicefs mount -d $META_URL1 /jfs --enable-xattr
    mkdir -p /jfs/test
    dd if=/dev/urandom of=/jfs/test/file1 bs=1M count=1024  
    ./juicefs clone /jfs/test/file1 /jfs/test/file2
    ./juicefs dump $META_URL1 dump.json $(get_dump_option)
    cat dump.json
    create_database $META_URL2
    ./juicefs load $META_URL2 dump.json $(get_load_option)
    ./juicefs dump $META_URL2 dump2.json $(get_dump_option)
    cat dump2.json
    if [[ "$BINARY" == "false" ]]; then
        compare_dump_json
    fi
    ./juicefs mount -d $META_URL2 /jfs2
    diff -ur /jfs/test /jfs2/test --no-dereference
    compare_stat_acl_xattr /jfs/test /jfs2/test
    umount_jfs /jfs2 $META_URL2
    ./juicefs status $META_URL2 && UUID=$(./juicefs status $META_URL2 | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --yes $META_URL2 $UUID
}

test_dump_load_with_fsrand()
{
    prepare_test
    ./juicefs format $META_URL1 myjfs --trash-days 0 --enable-acl
    ./juicefs mount -d $META_URL1 /jfs --enable-xattr
    rm -rf /tmp/test
    SEED=$SEED LOG_LEVEL=WARNING MAX_EXAMPLE=30 STEP_COUNT=20 PROFILE=generate ROOT_DIR1=/jfs/test ROOT_DIR2=/tmp/test python3 .github/scripts/hypo/fs.py || true    
    ./juicefs dump $META_URL1 dump.json $(get_dump_option)
    create_database $META_URL2
    ./juicefs load $META_URL2 dump.json $(get_load_option)
    ./juicefs dump $META_URL2 dump2.json $(get_dump_option)
    if [[ "$BINARY" == "false" ]]; then
        compare_dump_json
    fi
    ./juicefs mount -d $META_URL2 /jfs2
    diff -ur /jfs/test /jfs2/test --no-dereference
    compare_stat_acl_xattr /jfs/test /jfs2/test
    umount_jfs /jfs2 $META_URL2
    ./juicefs status $META_URL2 && UUID=$(./juicefs status $META_URL2 | grep UUID | cut -d '"' -f 4)
    ./juicefs destroy --yes $META_URL2 $UUID
}

compare_dump_json(){
    cp dump.json dump.json.bak
    cp dump2.json dump2.json.bak
    sed -i '/usedSpace/d' dump*.json.bak
    sed -i '/usedInodes/d' dump*.json.bak
    sed -i '/nextInodes/d' dump*.json.bak
    sed -i '/nextChunk/d' dump*.json.bak
    sed -i '/nextTrash/d' dump*.json.bak
    sed -i '/nextSession/d' dump*.json.bak
    sed -i 's/"inode":[0-9]\+/"inode":0/g' dump*.json.bak
    diff -ur dump.json.bak dump2.json.bak
    echo "compare_dump_json: dump json files are the same"
}

compare_stat_acl_xattr(){
    dir1=$1
    dir2=$2
    files1=($(find "$dir1" -type f -o -type d -exec stat -c "%n" {} + | sort))
    files2=($(find "$dir2" -type f -o -type d -exec stat -c "%n" {} + | sort))
    [[ ${#files1[@]} -ne ${#files2[@]} ]] && echo "compare_stat_acl: number of files differs" && exit 1
    for i in "${!files1[@]}"; do
        stat1=$(stat -c "%F %a %s %h %U %G" "${files1[$i]}")
        stat2=$(stat -c "%F %a %s %h %U %G" "${files2[$i]}")
        acl1=$(getfacl -p "${files1[$i]}" | tail -n +2)
        acl2=$(getfacl -p "${files2[$i]}" | tail -n +2)
        xattr1=$(getfattr -d -m . -e hex "${files1[$i]}" 2>/dev/null | tail -n +2 | sort)
        xattr2=$(getfattr -d -m . -e hex "${files2[$i]}" 2>/dev/null | tail -n +2 | sort)
        [[ "$stat1" != "$stat2" ]] && echo "compare_stat_acl: stat for ${files1[$i]} and ${files2[$i]} differs" && echo $stat1 && echo $stat2 && exit 1
        [[ "$acl1" != "$acl2" ]] && echo "compare_stat_acl: ACLs for ${files1[$i]} and ${files2[$i]} differs" && echo $acl1 && echo $acl2 && exit 1
        [[ "$xattr1" != "$xattr2" ]] && echo "compare_stat_acl: xattrs for ${files1[$i]} and ${files2[$i]} differs" && echo $xattr1 && echo $xattr2 && exit 1

    done
    echo "compare_stat_acl: ACLs and stats are the same"
}

get_dump_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    elif [[ "$FAST" == "true" ]]; then
        option="--fast"
    else
        option=""
    fi
    echo $option
}

get_load_option(){
    if [[ "$BINARY" == "true" ]]; then 
        option="--binary"
    else
        option=""
    fi
    echo $option
}

prepare_test(){
    umount_jfs /jfs $META_URL1
    umount_jfs /jfs2 $META_URL2
    python3 .github/scripts/flush_meta.py $META_URL1
    python3 .github/scripts/flush_meta.py $META_URL2
    rm -rf /var/jfs/myjfs || true
    mc rm --force --recursive myminio/test || true
}

source .github/scripts/common/run_test.sh && run_test $@
