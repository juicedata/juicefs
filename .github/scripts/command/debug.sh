#!/bin/bash -e

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

check_debug_file(){
   files=("system-info.log" "juicefs.log" "config.txt" "stats.txt" "stats.5s.txt" "pprof")
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
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/jfs/bigfile bs=1M count=1024
    ./juicefs debug /jfs/
    check_debug_file
    ./juicefs rmr /jfs/bigfile
}

test_debug_abnormal_juicefs(){
    rm -rf debug | true
    ./juicefs format $META_URL myjfs 
    ./juicefs mount -d $META_URL /jfs
    dd if=/dev/urandom of=/jfs/bigfile bs=1M count=1024
    killall -9 redis-server | true
    ./juicefs debug /jfs/
#    check_debug_file
    ./juicefs rmr /jfs/bigfile
}

source .github/scripts/common/run_test.sh && run_test $@
