#!/bin/bash -ex

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$START_META" ]] && START_META=true
source .github/scripts/start_meta_engine.sh
META_URL=$(get_meta_url $META)
META_URL2=$(get_meta_url2 $META)
FILE_COUNT_IN_BIGDIR=100000

prepare_test_data(){
  umount_jfs /tmp/jfs $META_URL
  python3 .github/scripts/flush_meta.py $META_URL
  rm -rf /var/jfs/myjfs || true
  create_database $META_URL
  ./juicefs format $META_URL myjfs
  ./juicefs mount -d $META_URL /tmp/jfs
  threads=10
  ./juicefs mdtest $META_URL /bigdir --depth=1 --dirs=0 --files=$((FILE_COUNT_IN_BIGDIR/threads)) --threads=$threads --write=8192
  ./juicefs mdtest $META_URL /smalldir --depth=3 --dirs=10 --files=10 --threads=10 --write=8192
}

if [[ "$START_META" == "true" ]]; then  
  start_meta_engine $META
  prepare_test_data
fi

test_dump_load(){
  do_dump_load dump.json
}

test_dump_load_fast(){
  do_dump_load dump.json.gz --fast
}

test_dump_load_in_binary(){
  do_dump_load dump.bin --binary
}

do_dump_load(){
  dump_file=$1
  shift
  options=$@
  ./juicefs dump $META_URL $dump_file $options --threads=50
  # python3 .github/scripts/flush_meta.py $META_URL2
  create_database $META_URL2
  if [[ "$options" == *"--binary"* ]]; then
    ./juicefs load $META_URL2 $dump_file $options
  else
    ./juicefs load $META_URL2 $dump_file
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
  
  file_count=$(ls -l /tmp/jfs2/bigdir/test-dir.0-0/mdtest_tree.0/ | wc -l)
  file_count=$((file_count-1))
  if [[ "$file_count" -ne "$FILE_COUNT_IN_BIGDIR" ]]; then 
    echo "<FATAL>: file_count error: $file_count"
    exit 1
  fi

  ./juicefs rmr /tmp/jfs2/smalldir
  ls /tmp/jfs2/smalldir && echo "<FATAL>: ls should fail" && exit 1 || true
  umount_jfs /tmp/jfs2 $META_URL2
  ./juicefs status $META_URL2 && UUID=$(./juicefs status $META_URL2 | grep UUID | cut -d '"' -f 4)
  ./juicefs destroy --yes $META_URL2 $UUID
}


source .github/scripts/common/run_test.sh && run_test $@

          