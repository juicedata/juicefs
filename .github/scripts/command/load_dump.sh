#!/bin/bash

test_dump_load()
{
    do_dump_load dump.json
}

test_dump_load_gzip()
{
    do_dump_load dump.json.gz
}

do_dump_load(){
    dump_file=$1
    ./juicefs format sqlite3://test.db myjfs 
    ./juicefs mount -d sqlite3://test.db /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs
    ./juicefs dump   sqlite3://test.db $dump_file

    ./juicefs format sqlite3://test2.db myjfs --bucket /var/jfs2
    ./juicefs load   sqlite3://test2.db $dump_file   
    ./juicefs mount -d sqlite3://test2.db /jfs2

    diff -ur /jfs /jfs2
    ./juicefs umount /jfs
    ./juicefs umount /jfs2

    uuid=$(./juicefs status sqlite3://test.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test.db $uuid

    uuid=$(./juicefs status sqlite3://test2.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test2.db $uuid
}