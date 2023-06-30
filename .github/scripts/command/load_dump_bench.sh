#!/bin/bash -ex

# python3 -c "import mysqlclient" || pip install mysqlclient
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_load_dump_with_small_dir(){
  prepare_test
  create_database $META_URL
  echo meta_url is: $META_URL
  mount_point=/tmp/juicefs-load-test
  wget -q https://s.juicefs.com/static/bench/2M_emtpy_files.dump.gz
  gzip -dk  2M_emtpy_files.dump.gz
  load_file=2M_emtpy_files.dump
  start=`date +%s`
  ./juicefs load $META_URL $load_file
  end=`date +%s`
  runtime=$((end-start))
  version=$(./juicefs -V|cut -b 17- | sed 's/:/-/g')
  python3 .github/scripts/db.py --name load_small_dir --result $runtime --version $version --meta ${{matrix.meta}} --storage file
  echo "load cost $runtime seconds"
  start=`date +%s`
  ./juicefs dump $META_URL dump.json
  end=`date +%s`
  runtime=$((end-start))
  echo "dump cost $runtime seconds"
  python3 .github/scripts/db.py --name dump_small_dir --result $runtime --version $version --meta ${{matrix.meta}} --storage file
  ./juicefs mount $META_URL $mount_point -d --no-usage-report
  inode=$(df -i $mount_point | grep JuiceFS |awk -F" " '{print $3}')
  if [ "$inode" -ne "2233313" ]; then 
    echo "<FATAL>: inode error: $inode"
    exit 1
  fi
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
  mount_point=/tmp/juicefs-load-test
  wget -q https://s.juicefs.com/static/bench/1M_files_in_one_dir.dump.gz
  gzip -dk  1M_files_in_one_dir.dump.gz
  load_file=1M_files_in_one_dir.dump
  start=`date +%s`
  ./juicefs load $META_URL $load_file
  end=`date +%s`
  runtime=$((end-start))
  echo "load cost $runtime seconds"
  version=$(./juicefs -V|cut -b 17- | sed 's/:/-/g')
  python3 .github/scripts/db.py --name load_big_dir --result $runtime --version $version --meta ${{matrix.meta}} --storage file
  start=`date +%s`
  if [ "$with_subdir" = true ] ; then
    ./juicefs dump $META_URL dump.json --subdir test
  else
    ./juicefs dump $META_URL dump.json
  fi
  end=`date +%s`
  runtime=$((end-start))
  echo "dump cost $runtime seconds"
  python3 .github/scripts/db.py --name dump_big_dir --result $runtime --version $version --meta ${{matrix.meta}} --storage file
  ./juicefs mount $META_URL $mount_point -d --no-usage-report
  df -i $mount_point
  inode=$(df -i $mount_point | grep JuiceFS |awk -F" " '{print $3}')
  echo "inode: $inode"
  if [ "$inode" -ne "1000003" ]; then 
    echo "<FATAL>: inode error: $inode"
    exit 1
  fi
}

test_list_with_big_dir(){
  mount_point=/tmp/juicefs-load-test
  start=`date +%s`
  file_count=$(ls -l $mount_point/test/test-dir.0-0/mdtest_tree.0/ | wc -l)
  echo "file_count: $file_count"
  end=`date +%s`
  runtime=$((end-start))
  echo "list cost $runtime seconds"
  export MYSQL_PASSWORD=${{secrets.MYSQL_PASSWORD_FOR_JUICEDATA}} 
  version=$(./juicefs -V|cut -b 17- | sed 's/:/-/g')
  python3 .github/scripts/db.py --name list_big_dir --result $runtime --version $version --meta ${{matrix.meta}} --storage file
  if [ "$file_count" -ne "1000001" ]; then 
    echo "<FATAL>: file_count error: $file_count"
    exit 1
  fi
}

prepare_test()
{
  umount_jfs /jfs $META_URL
  ls -l /jfs/.config && exit 1 || true
  python3 .github/scripts/flush_meta.py $META_URL
  rm -rf /var/jfs/myjfs || true
}

source .github/scripts/common/run_test.sh && run_test $@

          