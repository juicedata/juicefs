#!/bin/bash -ex

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
META_URL=$(get_meta_url $META)
start_meta_engine $META
FILE_COUNT=100000

test_dump_load_small_dir(){
  do_dump_load small_dir
}

test_dump_load_small_dir_in_binary(){
  do_dump_load small_dir --binary
}

test_dump_load_big_dir(){
  do_dump_load big_dir
}

test_dump_load_big_dir_in_binary(){
  do_dump_load big_dir --binary
}

do_dump_load(){
  dir_type=$1
  shift
  options=$@
  prepare_test
  create_database $META_URL
  ./juicefs format $META_URL myjfs
  ./juicefs mount -d $META_URL /tmp/jfs
  if [[ "$dir_type" == "bigdir" ]]; then
    threads=100
    ./juicefs mdtest $META_URL /mdtest --depth=1 --dirs=0 --files=$((FILE_COUNT/threads)) --threads=$threads --write=8192
  else
    ./juicefs mdtest $META_URL /mdtest --depth=2 --dirs=2 --files=10 --threads=10 --write=8192
  fi
  iused1=$(df -i /tmp/jfs | tail -1 | awk  '{print $3}')
  summary1=$(./juicefs summary /tmp/jfs/ --csv | head -n +2 | tail -n 1)
  ./juicefs dump $META_URL dump.json $options --threads=50
  umount_jfs /tmp/jfs $META_URL
  python3 .github/scripts/flush_meta.py $META_URL
  create_database $META_URL
  if [[ "$options" == *"--binary"* ]]; then
    ./juicefs load $META_URL dump.json $options
  else
    ./juicefs load $META_URL dump.json
  fi
  ./juicefs mount $META_URL /tmp/jfs -d
  iused2=$(df -i /tmp/jfs | tail -1 | awk  '{print $3}')
  summary2=$(./juicefs summary /tmp/jfs/ --csv | head -n +2 | tail -n 1)
  [[ "$iused1" == "$iused2" ]] || (echo "<FATAL>: iused error: $iused1 $iused2" && exit 1)
  [[ "$summary1" == "$summary2" ]] || (echo "<FATAL>: summary error: $summary1 $summary2" && exit 1)
  
  if [[ "$dir_type" == "bigdir" ]]; then
    file_count=$(ls -l /tmp/jfs/mdtest/test-dir.0-0/mdtest_tree.0/ | wc -l)
    if [[ "$file_count" -ne "$((FILE_COUNT+1))" ]]; then 
      echo "<FATAL>: file_count error: $file_count"
      exit 1
    fi
  fi

  ./juicefs rmr /tmp/jfs/mdtest
  ls /tmp/jfs/mdtest && echo "<FATAL>: ls should fail" && exit 1 || true
}

prepare_test(){
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
}


source .github/scripts/common/run_test.sh && run_test $@

          