#!/bin/bash -e
source .github/scripts/common/common_win.sh


[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1

test_clone_with_jfs_source()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    ls /z
    [[ ! -d /z/juicefs ]] && git clone https://github.com/juicedata/juicefs.git /z/juicefs --depth 1
    ls /z/juicefs
    do_clone true
    echo "test clone without --preserve"
#    do_clone false
}

do_clone()
{
    is_preserve=$1
    cmd.exe /c "taskkill /F /IM git.exe 2>nul || ver>nul"
    cmd.exe /c "rmdir /s /q z:\juicefs1 2>nul || ver>nul"
    cmd.exe /c "rmdir /s /q z:\juicefs2 2>nul || ver>nul"
    sleep 1
    
    [[ "$is_preserve" == "true" ]] && preserve="--preserve" || preserve=""
    cp -r /z/juicefs /z/juicefs1 $preserve
    ./juicefs.exe clone /z/juicefs /z/juicefs2 $preserve
    diff -ur /z/juicefs1 /z/juicefs2 --no-dereference
 #   CURRENT_DIR=$(pwd)
 #   cmd.exe /c "dir /s /b /a z:\juicefs1" > "${CURRENT_DIR}/log1"
 #   cmd.exe /c "dir /s /b /a z:\juicefs2" > "${CURRENT_DIR}/log2"
 #   diff -u "${CURRENT_DIR}/log1" "${CURRENT_DIR}/log2"
 #   rm -f "${CURRENT_DIR}/log1" "${CURRENT_DIR}/log2"
}

test_clone_with_small_files(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z:
    mkdir /z/test
    for i in $(seq 1 2000); do
        echo $i > /z/test/$i
    done
    ./juicefs.exe clone /z/test /z/test1
    diff -ur /z/test1 /z/test1
}

source .github/scripts/common/run_test.sh && run_test $@

