#!/bin/bash

./juicefs format sqlite3://test.db myjfs
./juicefs mount -d sqlite3://test.db /jfs

test_clone()
{
    [[ -d /jfs/juicefs ]] && rm -rf /jfs/juicefs
    git clone https://github.com/juicedata/juicefs.git /jfs/juicefs
    [[ -d /jfs/juicefs1 ]] && rm -rf /jfs/juicefs1
    ./juicefs clone /jfs/juicefs/ /jfs/juicefs1/
    diff -ur /jfs/juicefs /jfs/juicefs1
    rm /jfs/juicefs1 -rf
    
}


