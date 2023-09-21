#!/bin/bash

#  JuiceFS, Copyright 2021 Juicedata, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

test_dir=$1
if [ ! -d "$test_dir" ]; then
    mkdir "$test_dir"
fi

function cleanup() {
    code=$?
    if [ $code -eq 0 ]; then
      echo "ioctl test passed"
    else
      echo "ioctl test failed"
    fi
    trap - EXIT
    sudo chattr -R "=" "$test_dir"
    rm -rf "$test_dir"
    exit $code
}

function exec_should_failed() {
  eval "$1"
  if [ $? -eq 0 ]; then
      echo "$1 should fail"
      exit 1
  fi
}

function exec_should_success() {
  eval "$1"
  if [ $? -ne 0 ]; then
      echo "$1 should success"
      exit 1
  fi
}

a_test_dir="$test_dir"/a
sudo chattr -R "=" "${test_dir:?}"
sudo rm -rf "${test_dir:?}"/*
mkdir "$a_test_dir"

trap cleanup INT EXIT

{
  touch "$a_test_dir"/afile
  exec_should_failed 'sudo chattr "+u" $a_test_dir/afile'
  exec_should_success 'sudo chattr "+a" $a_test_dir/afile'
  exec_should_success '[[ "$(lsattr $a_test_dir/afile | awk -F " " "{print \$1}")" =~ "a" ]]'
  exec_should_failed "echo aa > $a_test_dir/afile"
  exec_should_failed "rm -rf $a_test_dir/afile"
  touch "$a_test_dir/tmpfile"
  exec_should_failed "mv -f $a_test_dir/tmpfile $a_test_dir/afile"
  exec_should_failed "mv -f $a_test_dir/afile $a_test_dir/tmpfile"
  exec_should_failed "ln $a_test_dir/afile $a_test_dir/linkfile"
  echo "12345" >> "$a_test_dir"/afile
  exec_should_success '[ "$(cat "$a_test_dir"/afile)" == "12345" ]'

  # FIXME: sudo chattr "+a" $a_test_dir/fallocatefile random failed
  touch "$a_test_dir"/fallocatefile
  exec_should_success 'sudo chattr "+a" $a_test_dir/fallocatefile'
  exec_should_success '[[ "$(lsattr $a_test_dir/fallocatefile | awk -F " " "{print \$1}")" =~ "a" ]]'
  exec_should_failed 'fallocate -l 1k -n $a_test_dir/fallocatefile'
}


{
  mkdir -p "$a_test_dir"/adir/child_dir1/child_dir2
  touch "$a_test_dir"/adir/file
  exec_should_success 'sudo chattr "+a" $a_test_dir/adir'
  exec_should_success '[[ "$(lsattr -d $a_test_dir/adir | awk -F " " "{print \$1}")" =~ "a" ]]'
  exec_should_failed 'rm -rf $a_test_dir/adir'
  exec_should_failed 'rm -rf $a_test_dir/adir/file'
  exec_should_success 'touch "$a_test_dir"/adir/child_dir1/child_file'
  exec_should_success 'rm -rf $a_test_dir/adir/child_dir1/child_dir2'
  exec_should_success 'rm -rf $a_test_dir/adir/child_dir1/child_file'
  exec_should_failed 'rm -rf $a_test_dir/adir/child_dir1'

  exec_should_success 'touch $a_test_dir/adir/tmpfile'
  exec_should_success 'echo 123 > $a_test_dir/adir/tmpfile'
  exec_should_success 'echo 123 >> $a_test_dir/adir/tmpfile'

  exec_should_failed 'mv -f $a_test_dir/adir/tmpfile $a_test_dir/adir/file'
  exec_should_failed 'mv -f $a_test_dir/adir/file $a_test_dir/adir/tmpfile'
  touch "$a_test_dir"/tfile
  exec_should_success 'mv -f $a_test_dir/tfile $a_test_dir/adir/file2'
}


i_test_dir="$test_dir"/i
sudo chattr -R "=" "${i_test_dir:?}"
sudo rm -rf "${i_test_dir:?}"/*
mkdir "$i_test_dir"

{
  touch "$i_test_dir"/ifile
  exec_should_success 'sudo chattr "+i" "$i_test_dir"/ifile'
  exec_should_success '[[ "$(lsattr $i_test_dir/ifile | awk -F " " "{print \$1}")" =~ "i" ]]'

  exec_should_failed "echo aa > $i_test_dir/ifile"
  exec_should_failed "echo aa >> $i_test_dir/ifile"
  exec_should_failed "rm -rf $i_test_dir/ifile"
  touch "$i_test_dir/tmpfile"
  exec_should_failed "mv -f $i_test_dir/tmpfile $i_test_dir/ifile"
  exec_should_failed "mv -f $i_test_dir/ifile $a_test_dir/tmpfile"
  exec_should_failed "ln $i_test_dir/ifile $i_test_dir/linkfile"

  touch "$i_test_dir"/fallocatefile
  exec_should_success 'sudo chattr "+i" $i_test_dir/fallocatefile'
  exec_should_success '[[ "$(lsattr $i_test_dir/fallocatefile | awk -F " " "{print \$1}")" =~ "i" ]]'
  exec_should_failed 'fallocate -l 1k -n $i_test_dir/fallocatefile'
}

{
  mkdir -p "$i_test_dir"/idir/child_dir1/child_dir2
  touch "$i_test_dir"/idir/file

  exec_should_success 'sudo chattr "+i" $i_test_dir/idir'
  exec_should_success '[[ "$(lsattr -d $i_test_dir/idir | awk -F " " "{print \$1}")" =~ "i" ]]'
  exec_should_success 'touch "$i_test_dir"/idir/child_dir1/child_file'
  exec_should_success 'rm -rf $i_test_dir/idir/child_dir1/child_dir2'
  exec_should_success 'rm -rf $i_test_dir/idir/child_dir1/child_file'
  exec_should_failed 'rm -rf $i_test_dir/idir'
  exec_should_failed 'rm -rf $i_test_dir/idir/file'
  exec_should_failed 'rm -rf $i_test_dir/idir/child_dir1'

  exec_should_failed 'touch $i_test_dir/idir/tmpfile'
  exec_should_success 'echo 123 > $i_test_dir/idir/file'
  exec_should_success 'echo 123 >> $i_test_dir/idir/file'

  exec_should_failed 'mv -f $i_test_dir/idir/tmpfile $i_test_dir/idir/file'
  exec_should_failed 'mv -f $i_test_dir/idir/file $i_test_dir/idir/tmpfile'
  touch "$i_test_dir"/tfile
  exec_should_failed 'mv -f $i_test_dir/tfile $i_test_dir/idir/file2'
}
