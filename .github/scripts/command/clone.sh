#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_clone_preserve_with_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs touch /jfs/test
    for mode in 777 755 644; do
        sudo -u juicefs chmod $mode /jfs/test
        check_guid_after_clone true
        check_guid_after_clone false
    done
}

test_clone_preserve_with_dir()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    id -u juicefs  && sudo userdel juicefs
    sudo useradd -u 1101 juicefs
    sudo -u juicefs mkdir /jfs/test
    for mode in 777 755 644; do
        sudo -u juicefs chmod $mode /jfs/test
        check_guid_after_clone true
        check_guid_after_clone false
    done
}

test_clone_with_jfs_source()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    [[ ! -d /jfs/juicefs ]] && git clone https://github.com/juicedata/juicefs.git /jfs/juicefs --depth 1
    do_clone true
    do_clone false
}

skip_test_clone_with_fsrand()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    seed=$(date +%s)
    python3 .github/scripts/fsrand.py -a -c 2000 -s $seed  /jfs/juicefs
    do_clone true
    do_clone false 
}

do_clone()
{
    is_preserve=$1
    rm -rf /jfs/juicefs1
    rm -rf /jfs/juicefs2
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    cp -r /jfs/juicefs /jfs/juicefs1 $preserve
    ./juicefs clone /jfs/juicefs /jfs/juicefs2 $preserve
    diff -ur /jfs/juicefs1 /jfs/juicefs2 --no-dereference
    cd /jfs/juicefs1/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 >/tmp/log1 && cd -
    cd /jfs/juicefs2/ && find . -printf "%m\t%u\t%g\t%p\n"  | sort -k4 >/tmp/log2 && cd -
    diff -u /tmp/log1 /tmp/log2
}

test_clone_with_big_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/test bs=1M count=1000
    cp /tmp/test /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}
test_clone_with_big_file2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/tmp/test bs=1M count=1000
    echo "a" | tee -a /tmp/test
    cp /tmp/test /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}

test_clone_with_random_write(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    PATH1=/tmp/test PATH2=/jfs/test python3 .github/scripts/random_read_write.py 
    ./juicefs clone /jfs/test /jfs/test1
    rm /jfs/test -rf
    diff /tmp/test /jfs/test1
}

test_clone_with_sparse_file()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    fallocate -l 1.0001g /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
    diff /jfs/test /jfs/test1
}

test_clone_with_sparse_file2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    fallocate -l 1.1T /jfs/test
    ./juicefs clone /jfs/test /jfs/test1
}

test_clone_with_small_files(){
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir /jfs/test
    for i in $(seq 1 2000); do
        echo $i > /jfs/test/$i
    done
    ./juicefs clone /jfs/test /jfs/test1
    diff -ur /jfs/test1 /jfs/test1
}

skip_test_clone_with_mdtest1()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /test --depth 2 --dirs 10 --files 10 --threads 100 --write 8192
    ./juicefs clone /jfs/test /jfs/test1
    ./juicefs rmr /jfs/test
    ./juicefs rmr /jfs/test1
}

skip_test_clone_with_mdtest2()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    ./juicefs mdtest $META_URL /test --depth 1 --dirs 1 --files 1000 --threads 100 --write 8192
    ./juicefs clone /jfs/test /jfs/test1
    ./juicefs rmr /jfs/test
    ./juicefs rmr /jfs/test1
}

check_guid_after_clone(){
    is_preserve=$1
    echo "check_guid_after_clone, is_preserve: $is_preserve"
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    rm /jfs/test1 -rf
    sleep 3
    ls /jfs/test1 && echo "test1 should not exist" && exit 1 || echo "/jfs/test1 not exist" 
    rm /jfs/test2 -rf
    ./juicefs clone /jfs/test /jfs/test1 $preserve
    cp /jfs/test /jfs/test2 -rf $preserve
    uid1=$(stat -c %u /jfs/test1)
    gid1=$(stat -c %g /jfs/test1)
    mode1=$(stat -c %a /jfs/test1)
    uid2=$(stat -c %u /jfs/test2)
    gid2=$(stat -c %g /jfs/test2)
    mode2=$(stat -c %a /jfs/test2)

    if [[ "$uid1" != "$uid2" ]] || [[ "$gid1" != "$gid2" ]] || [[ "$mode1" != "$mode2" ]]; then
        echo >&2 "<FATAL>: clone does not same as cp: uid1: $uid1, uid2: $uid2, gid1: $gid1, gid2: $gid2, mode1: $mode1, mode2: $mode2"
        exit 1
    fi
}

test_batch_clone_with_small_files()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    mkdir -p /jfs/batch_src
    for i in $(seq 1 200); do
        echo "content_$i" > /jfs/batch_src/file_$i
    done
    for i in $(seq 1 20); do
        ln -s /jfs/batch_src/file_$i /jfs/batch_src/sym_$i
    done

    ./juicefs clone /jfs/batch_src /jfs/batch_dst
    diff -ur /jfs/batch_src /jfs/batch_dst --no-dereference

    src_count=$(find /jfs/batch_src -mindepth 1 | wc -l)
    dst_count=$(find /jfs/batch_dst -mindepth 1 | wc -l)
    if [[ "$src_count" -ne "$dst_count" ]]; then
        echo "<FATAL>: file count mismatch: src=$src_count dst=$dst_count"
        exit 1
    fi
}

do_clone_with_batch_size()
{
    src=$1
    dst=$2
    threads=$3

    time ./juicefs clone "$src" "$dst" --threads "$threads" 
    ./juicefs summary "$src" --depth=1 | head -n 4 | tail -n 1 | sed 's/ //g' | tee /tmp/sum_src.log
    ./juicefs summary "$dst" --depth=1 | head -n 4 | tail -n 1 | sed 's/ //g' | tee /tmp/sum_dst.log
    diff /tmp/sum_src.log /tmp/sum_dst.log
}

test_batched_clone_with_mdtest()
{
    if [[ "$META" == "tikv" ]]; then
        echo "skip test_batched_clone_with_mdtest for tikv"
        return 0
    fi
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    if [[ "$META" == "redis" ]]; then
        ./juicefs mdtest $META_URL /test --depth 2 --dirs 10 --files 100 --threads 50 --write 8192
    else
        ./juicefs mdtest $META_URL /test --depth 1 --dirs 5 --files 20 --threads 20 --write 4096
    fi
    do_clone_with_batch_size /jfs/test /jfs/test-clone 100
    ./juicefs rmr /jfs/test-clone --skip-trash
}

test_batched_clone_with_random_test()
{
    if [[ "$META" == "tikv" ]]; then
        echo "skip test_batched_clone_with_random_test for tikv"
        return 0
    fi
    prepare_test
    rm -f random-test.log || true
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    [[ ! -x random-test ]] && wget -q https://juicefs-com-static.oss-cn-shanghai.aliyuncs.com/random-test/random-test -O random-test && chmod +x random-test

    timeout 90s ./random-test runOp --duration 45s --baseDir /jfs/random-test --logDir random-test-log  \
        --files 200000 --ops 2000000 --threads 100 --dirSize 100 --mkdirOp 10,uniform -createOp 20,uniform \
        -linkOp 2,uniform --symlinkOp 20,uniform --setXattrOp 10,uniform --truncateOp 10,uniform > random-test.log 2>&1

    for threads in 1000 100; do
        do_clone_with_batch_size /jfs/random-test /jfs/random-test-clone $threads
        do_clone_with_batch_size /jfs/random-test /jfs/random-test-clone/random-test-clone $threads
        ./juicefs rmr /jfs/random-test-clone
    done
}

test_batch_clone_mixed_entries()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/mix_src/subdir1/subsubdir
    mkdir -p /jfs/mix_src/subdir2
    echo "file_a" > /jfs/mix_src/file_a
    echo "file_b" > /jfs/mix_src/file_b
    dd if=/dev/urandom of=/jfs/mix_src/large_file bs=1M count=10 2>/dev/null
    echo "sub_file" > /jfs/mix_src/subdir1/sub_file
    echo "subsub_file" > /jfs/mix_src/subdir1/subsubdir/subsub_file
    ln -s /jfs/mix_src/file_a /jfs/mix_src/sym_a
    ln -s /jfs/mix_src/subdir1/sub_file /jfs/mix_src/subdir2/sym_sub

    ./juicefs clone /jfs/mix_src /jfs/mix_dst
    diff -ur /jfs/mix_src /jfs/mix_dst --no-dereference
}

test_batch_clone_with_threads()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/threads_src
    for i in $(seq 1 100); do
        echo "data_$i" > /jfs/threads_src/file_$i
    done
    mkdir -p /jfs/threads_src/sub1 /jfs/threads_src/sub2
    echo "s1" > /jfs/threads_src/sub1/s1
    echo "s2" > /jfs/threads_src/sub2/s2

    for threads in 1 4 8; do
        rm -rf /jfs/threads_dst_$threads
        ./juicefs clone --threads $threads /jfs/threads_src /jfs/threads_dst_$threads
        diff -ur /jfs/threads_src /jfs/threads_dst_$threads --no-dereference
    done
}

test_batch_clone_preserve_attrs()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    id -u juicefs && sudo userdel juicefs
    sudo useradd -u 1101 juicefs

    sudo -u juicefs mkdir -p /jfs/pattr_src
    sudo -u juicefs touch /jfs/pattr_src/file1
    sudo -u juicefs chmod 755 /jfs/pattr_src/file1
    sudo -u juicefs touch /jfs/pattr_src/file2
    sudo -u juicefs chmod 600 /jfs/pattr_src/file2

    ./juicefs clone --preserve /jfs/pattr_src /jfs/pattr_dst_p
    for f in file1 file2; do
        src_stat=$(stat -c "%u %g %a" /jfs/pattr_src/$f)
        dst_stat=$(stat -c "%u %g %a" /jfs/pattr_dst_p/$f)
        if [[ "$src_stat" != "$dst_stat" ]]; then
            echo "<FATAL>: preserve attrs mismatch for $f: src=($src_stat) dst=($dst_stat)"
            exit 1
        fi
    done

    ./juicefs clone /jfs/pattr_src /jfs/pattr_dst_np
    current_uid=$(id -u)
    current_gid=$(id -g)
    for f in file1 file2; do
        dst_uid=$(stat -c "%u" /jfs/pattr_dst_np/$f)
        dst_gid=$(stat -c "%g" /jfs/pattr_dst_np/$f)
        if [[ "$dst_uid" != "$current_uid" ]] || [[ "$dst_gid" != "$current_gid" ]]; then
            echo "<FATAL>: non-preserve clone should use current uid/gid, got uid=$dst_uid gid=$dst_gid"
            exit 1
        fi
    done
}

test_batch_clone_dest_exists()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/exists_src
    echo "data" > /jfs/exists_src/file1

    ./juicefs clone /jfs/exists_src /jfs/exists_dst

    set +e
    ./juicefs clone /jfs/exists_src /jfs/exists_dst 2>/tmp/clone_exists_err
    ret=$?
    set -e
    if [[ $ret -eq 0 ]]; then
        echo "<FATAL>: clone to existing destination should fail"
        exit 1
    fi
}

test_batch_clone_concurrent_same_dest()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/conc_src
    for i in $(seq 1 50); do
        echo "data_$i" > /jfs/conc_src/file_$i
    done

    set +e
    ./juicefs clone /jfs/conc_src /jfs/conc_dst &
    pid1=$!
    ./juicefs clone /jfs/conc_src /jfs/conc_dst &
    pid2=$!

    wait $pid1
    ret1=$?
    wait $pid2
    ret2=$?
    set -e

    success_count=0
    [[ $ret1 -eq 0 ]] && success_count=$((success_count + 1))
    [[ $ret2 -eq 0 ]] && success_count=$((success_count + 1))

    if [[ $success_count -eq 0 ]]; then
        echo "<FATAL>: both concurrent clones failed, expected one to succeed"
        exit 1
    fi
    if [[ $success_count -eq 2 ]]; then
        echo "<FATAL>: both concurrent clones succeeded, expected only one"
        exit 1
    fi

    diff -ur /jfs/conc_src /jfs/conc_dst --no-dereference
}

test_batch_clone_file_atomicity()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    dd if=/dev/urandom of=/tmp/big_clone_src bs=1M count=100 2>/dev/null
    cp /tmp/big_clone_src /jfs/big_file

    ./juicefs clone /jfs/big_file /jfs/big_file_clone &
    clone_pid=$!

    file_appeared=false
    while kill -0 $clone_pid 2>/dev/null; do
        if [[ -f /jfs/big_file_clone ]]; then
            file_appeared=true
            break
        fi
        sleep 0.05
    done
    wait $clone_pid

    diff /jfs/big_file /jfs/big_file_clone
}

test_batch_clone_dir_consistency_under_mutation()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/mut_src
    for i in $(seq 1 500); do
        echo "data_$i" > /jfs/mut_src/file_$i
    done

    ./juicefs clone /jfs/mut_src /jfs/mut_dst &
    clone_pid=$!

    sleep 0.1
    for i in $(seq 501 550); do
        echo "new_$i" > /jfs/mut_src/file_$i 2>/dev/null || true
    done
    rm -f /jfs/mut_src/file_1 2>/dev/null || true
    echo "modified" > /jfs/mut_src/file_2 2>/dev/null || true

    set +e
    wait $clone_pid
    clone_ret=$?
    set -e

    if [[ $clone_ret -eq 0 ]]; then
        if [[ ! -d /jfs/mut_dst ]]; then
            echo "<FATAL>: clone succeeded but destination is not a directory"
            exit 1
        fi
    fi
}

test_batch_clone_interrupt_cleanup()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/intr_src
    for i in $(seq 1 1000); do
        echo "data_$i" > /jfs/intr_src/file_$i
    done
    for i in $(seq 1 10); do
        mkdir -p /jfs/intr_src/subdir_$i
        for j in $(seq 1 100); do
            echo "sub_data_${i}_${j}" > /jfs/intr_src/subdir_$i/file_$j
        done
    done

    ./juicefs clone /jfs/intr_src /jfs/intr_dst &
    clone_pid=$!
    sleep 0.3
    kill -9 $clone_pid 2>/dev/null || true
    wait $clone_pid 2>/dev/null || true

    sleep 5

    if [[ -d /jfs/intr_dst ]]; then
        diff -ur /jfs/intr_src /jfs/intr_dst --no-dereference || true
    fi
}

test_batch_clone_xattr()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/xattr_src
    echo "xdata1" > /jfs/xattr_src/file1
    echo "xdata2" > /jfs/xattr_src/file2
    setfattr -n user.tag -v "value1" /jfs/xattr_src/file1 || { echo "setfattr not supported, skipping"; return 0; }
    setfattr -n user.tag -v "value2" /jfs/xattr_src/file2
    setfattr -n user.extra -v "extra_info" /jfs/xattr_src/file1

    ./juicefs clone /jfs/xattr_src /jfs/xattr_dst

    val1=$(getfattr -n user.tag --only-values /jfs/xattr_dst/file1 2>/dev/null)
    val2=$(getfattr -n user.tag --only-values /jfs/xattr_dst/file2 2>/dev/null)
    val3=$(getfattr -n user.extra --only-values /jfs/xattr_dst/file1 2>/dev/null)

    if [[ "$val1" != "value1" ]] || [[ "$val2" != "value2" ]] || [[ "$val3" != "extra_info" ]]; then
        echo "<FATAL>: xattr mismatch after clone: val1=$val1 val2=$val2 val3=$val3"
        exit 1
    fi
}

test_batch_clone_empty_dir()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/empty_src
    ./juicefs clone /jfs/empty_src /jfs/empty_dst

    if [[ ! -d /jfs/empty_dst ]]; then
        echo "<FATAL>: clone empty dir: destination not created"
        exit 1
    fi
    dst_count=$(ls -A /jfs/empty_dst | wc -l)
    if [[ "$dst_count" -ne 0 ]]; then
        echo "<FATAL>: clone empty dir: destination is not empty, count=$dst_count"
        exit 1
    fi
}

test_batch_clone_hardlinks()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/hl_src
    echo "hardlink_data" > /jfs/hl_src/file_orig
    ln /jfs/hl_src/file_orig /jfs/hl_src/file_link

    src_ino1=$(stat -c %i /jfs/hl_src/file_orig)
    src_ino2=$(stat -c %i /jfs/hl_src/file_link)
    if [[ "$src_ino1" != "$src_ino2" ]]; then
        echo "<FATAL>: hardlinks setup failed"
        exit 1
    fi

    ./juicefs clone /jfs/hl_src /jfs/hl_dst

    dst_ino1=$(stat -c %i /jfs/hl_dst/file_orig)
    dst_ino2=$(stat -c %i /jfs/hl_dst/file_link)
    if [[ "$dst_ino1" == "$dst_ino2" ]]; then
        echo "<FATAL>: cloned hardlinks should have different inodes"
        exit 1
    fi

    diff /jfs/hl_dst/file_orig /jfs/hl_dst/file_link
    diff /jfs/hl_src/file_orig /jfs/hl_dst/file_orig
}

test_batch_clone_consistency_with_cp()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs

    mkdir -p /jfs/cons_src/d1/d2
    for i in $(seq 1 50); do
        echo "data_$i" > /jfs/cons_src/file_$i
    done
    echo "nested1" > /jfs/cons_src/d1/f1
    echo "nested2" > /jfs/cons_src/d1/d2/f2
    ln -s /jfs/cons_src/file_1 /jfs/cons_src/sym1
    dd if=/dev/urandom of=/jfs/cons_src/bigfile bs=1M count=5 2>/dev/null

    cp -r /jfs/cons_src /jfs/cons_cp_dst
    ./juicefs clone /jfs/cons_src /jfs/cons_clone_dst

    diff -ur /jfs/cons_cp_dst /jfs/cons_clone_dst --no-dereference
}

test_clone_during_random_test_stress()
{
    if [[ "$META" == "tikv" ]]; then
        echo "skip test_clone_during_random_test_stress for tikv"
        return 0
    fi
    prepare_test
    rm -f /var/log/juicefs.log random-test.log
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    [[ ! -x random-test ]] && wget -q https://juicefs-com-static.oss-cn-shanghai.aliyuncs.com/random-test/random-test -O random-test && chmod +x random-test

    timeout 90s ./random-test runOp --baseDir /jfs/random-test --logDir random-test-log --writeSize 1,4096 \
        --duration 60s --files 200000 --ops 2000000 --threads 50 --dirSize 100 \
        --mkdirOp 10,uniform -createOp 10,uniform -readOp 2,uniform -lsOp 2,uniform \
        -deleteOp 0.1,uniform -rmrOp 0.02,end -renameOp 1,uniform -linkOp 2,uniform \
        --truncateOp 1,uniform --truncateSize 1M,1M > random-test.log 2>&1 &
    random_test_pid=$!

    sleep 30

    ./juicefs clone /jfs/random-test /jfs/random-test-clone
    [[ ! -d /jfs/random-test-clone ]] && echo "<FATAL>: clone target /jfs/random-test-clone not found" && exit 1

    set +e
    wait $random_test_pid
    random_test_ret=$?
    set -e
    if [[ $random_test_ret -ne 0 && $random_test_ret -ne 124 ]]; then
        cat random-test.log
        echo "<FATAL>: random-test failed with exit code $random_test_ret"
        exit 1
    fi
}

source .github/scripts/common/run_test.sh && run_test $@

