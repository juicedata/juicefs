#!/bin/bash -e

source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
[[ -z "$START_META" ]] && START_META=true
source .github/scripts/start_meta_engine.sh
META_URL=$(get_meta_url $META)
if [ "$START_META" = true ]; then
    start_meta_engine $META
fi

test_load_dump_with_small_dir(){
  do_load_dump_with_small_dir
}

test_load_dump_with_small_dir_in_binary(){
  do_load_dump_with_small_dir --binary
}

do_load_dump_with_small_dir(){
  option=$1
  prepare_test
  create_database $META_URL
  ./juicefs format $META_URL myjfs
  ./juicefs mount -d $META_URL /tmp/jfs
  ./juicefs mdtest /mdtest --depth=3 --dirs=10 --files=10 --threads=100 --write=8192
  df -i /tmp/jfs
  ./juicefs dump $META_URL dump.json $option --threads=50
  umount_jfs /tmp/jfs $META_URL
  python3 .github/scripts/flush_meta.py $META_URL
  create_database $META_URL
  if [[ "$options" == *"--binary"* ]]; then
    ./juicefs load dump.json $META_URL --binary
  else
    ./juicefs load dump.json $META_URL
  fi
  ./juicefs mount $META_URL /tmp/jfs -d
  df -i /tmp/jfs
}

test_load_dump_with_big_dir_subdir(){
  do_load_dump_with_big_dir true
}

test_load_dump_with_big_dir(){
  do_load_dump_with_big_dir false
}

do_load_dump_with_big_dir(){
  with_subdir=$1
  prepare_test
  create_database $META_URL
  echo meta_url is: $META_URL
  wget -q https://s.juicefs.com/static/bench/1M_files_in_one_dir.dump.gz
  gzip -dfk  1M_files_in_one_dir.dump.gz
  load_file=1M_files_in_one_dir.dump
  start=`date +%s`
  ./juicefs load $META_URL $load_file
  end=`date +%s`
  runtime=$((end-start))
  echo "load cost $runtime seconds"
  version=$(./juicefs -V|cut -b 17- | sed 's/:/-/g')
  .github/scripts/save_benchmark.sh --name load_big_dir --result $runtime --meta $META --storage file
  start=`date +%s`
  if [ "$with_subdir" = true ] ; then
    ./juicefs dump $META_URL dump.json --subdir test --fast
  else
    ./juicefs dump $META_URL dump.json --fast
  fi
  end=`date +%s`
  runtime=$((end-start))
  echo "dump cost $runtime seconds"
  .github/scripts/save_benchmark.sh --name dump_big_dir --result $runtime --meta $META --storage file
  ./juicefs mount $META_URL /jfs -d --no-usage-report
  df -i /jfs
  inode=$(df -i /jfs | grep JuiceFS |awk -F" " '{print $3}')
  echo "inode: $inode"
  if [ "$inode" -ne "1000003" ]; then 
    echo "<FATAL>: inode error: $inode"
    exit 1
  fi
}

test_list_with_big_dir(){
  start=`date +%s`
  file_count=$(ls -l /jfs/test/test-dir.0-0/mdtest_tree.0/ | wc -l)
  echo "file_count: $file_count"
  end=`date +%s`
  runtime=$((end-start))
  echo "list cost $runtime seconds"
  version=$(./juicefs -V|cut -b 17- | sed 's/:/-/g')
  .github/scripts/save_benchmark.sh --name list_big_dir --result $runtime --meta $META --storage file
  if [ "$file_count" -ne "1000001" ]; then 
    echo "<FATAL>: file_count error: $file_count"
    exit 1
  fi
}

prepare_test()
{
  umount_jfs /jfs $META_URL
  ls -l /jfs/.config && exit 1 || true
  ./juicefs status $META_URL && UUID=$(./juicefs status $META_URL | grep UUID | cut -d '"' -f 4) || echo "meta not exist"
  if [ -n "$UUID" ];then
    ./juicefs destroy --yes $META_URL $UUID
  fi
  # python3 .github/scripts/flush_meta.py $META_URL
  # rm -rf /var/jfs/myjfs || true
  # rm -rf /var/jfsCache/myjfs || true
}

source .github/scripts/common/run_test.sh && run_test $@

          