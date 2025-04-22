#!/bin/bash -e

source .github/scripts/common/common_win.sh
[[ -z "$META_URL" ]] && META=redis://127.0.0.1:6379/1


check_debug_file(){
   files=("system-info.log" "juicefs.log" "config.txt" "stats.txt" "stats.5s.txt")
   debug_dir="debug"
   if [ ! -d "$debug_dir" ]; then
    echo "error:no debug dir"
    exit 1
   fi
   all_files_exist=true
   for file in "${files[@]}"; do
     exist=`find "$debug_dir" -name $file | wc -l`
     if [ "$exist" == 0 ]; then
        echo "no $file"
        all_files_exist=false
     fi
   done
   if [ "$all_files_exist" = true ]; then
    echo "pass"
   else
    exit 1
   fi
}

test_debug_juicefs(){
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z:
    dd if=/dev/urandom of=/z/bigfile bs=1M count=1024
    ./juicefs.exe debug z:
    check_debug_file
    find debug -print | sed -e 's;[^/]*/;|____;g;s;____|; |;g'
    ./juicefs.exe rmr /z/bigfile
}

test_debug_abnormal_juicefs(){
    rm -rf debug | true
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs 
    ./juicefs.exe mount -d $META_URL z:
    dd if=/dev/urandom of=/z/bigfile bs=1M count=1024
    killall -9 redis-server | true
    ./juicefs.exe debug z:
    check_debug_file
    ./juicefs.exe rmr /z/bigfile
}

source .github/scripts/common/run_test.sh && run_test $@