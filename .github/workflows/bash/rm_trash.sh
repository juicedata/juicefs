#!/bin/bash
IFS=$'\n\n'

LIST=`cat $1`


for LINE in $LIST; do
      echo $LINE
      cd /tmp/juicefs-sync-test/.trash/$LINE
      pwd
      sudo /tmp/juicefs-sync-test/juicefs/juicefs rmr *
done