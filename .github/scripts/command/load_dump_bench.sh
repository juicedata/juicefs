#!/bin/bash -ex

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$START_META" ]] && START_META=true
[[ -z "$BIGDIR" ]] && BIGDIR=true
source .github/scripts/start_meta_engine.sh
META_URL=$(get_meta_url $META)
META_URL2=$(get_meta_url2 $META)
FILE_COUNT=500000

prepare_test_data(){
  umount_jfs /tmp/jfs $META_URL
  python3 .github/scripts/flush_meta.py $META_URL
  rm -rf /var/jfs/myjfs || true
  create_database $META_URL
  ./juicefs format $META_URL myjfs
  ./juicefs mount -d $META_URL /tmp/jfs
  if [[ "$BIGDIR" == "true" ]]; then
    threads=100
    ./juicefs mdtest $META_URL /mdtest --depth=1 --dirs=0 --files=$((FILE_COUNT/threads)) --threads=$threads --write=8192
  else
    ./juicefs mdtest $META_URL /mdtest --depth=3 --dirs=10 --files=10 --threads=100 --write=8192
  fi
}

if [[ "$START_META" == "true" ]]; then  
  start_meta_engine $META
  prepare_test_data
fi

test_dump_load(){
  do_dump_load
}

test_dump_load_fast(){
  do_dump_load --fast
}

test_dump_load_in_binary(){
  do_dump_load --binary
}

do_dump_load(){
  options=$@
  ./juicefs dump $META_URL dump.json $options --threads=50
  umount_jfs /tmp/jfs2 $META_URL2
  python3 .github/scripts/flush_meta.py $META_URL2
  create_database $META_URL2
  if [[ "$options" == *"--binary"* ]]; then
    ./juicefs load $META_URL2 dump.json $options
  else
    ./juicefs load $META_URL2 dump.json
  fi
  ./juicefs mount $META_URL2 /tmp/jfs2 -d
  df -i /tmp/jfs /tmp/jfs2
  iused1=$(df -i /tmp/jfs | tail -1 | awk  '{print $3}')
  iused2=$(df -i /tmp/jfs2 | tail -1 | awk  '{print $3}')
  [[ "$iused1" == "$iused2" ]] || (echo "<FATAL>: iused error: $iused1 $iused2" && exit 1)
  ./juicefs summary /tmp/jfs/ --csv
  ./juicefs summary /tmp/jfs2/ --csv
  summary1=$(./juicefs summary /tmp/jfs/ --csv | head -n +2 | tail -n 1)
  summary2=$(./juicefs summary /tmp/jfs2/ --csv | head -n +2 | tail -n 1)
  [[ "$summary1" == "$summary2" ]] || (echo "<FATAL>: summary error: $summary1 $summary2" && exit 1)
  
  if [[ "$BIGDIR" == "true" ]]; then
    file_count=$(ls -l /tmp/jfs2/mdtest/test-dir.0-0/mdtest_tree.0/ | wc -l)
    if [[ "$file_count" -ne "$((FILE_COUNT+1))" ]]; then 
      echo "<FATAL>: file_count error: $file_count"
      exit 1
    fi
  fi

  ./juicefs rmr /tmp/jfs2/mdtest
  ls /tmp/jfs2/mdtest && echo "<FATAL>: ls should fail" && exit 1 || true
}


source .github/scripts/common/run_test.sh && run_test $@

          