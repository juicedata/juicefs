#!/bin/bash
set -e
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
    umount /jfs || true
    umount /jfs2 || true
    rm -rf test.db test2.db
    [[ -d /var/jfs1/myjfs ]] && rm -rf /var/jfs1/myjfs
    ./juicefs format sqlite3://test.db myjfs --bucket /var/jfs1
    ./juicefs mount -d sqlite3://test.db /jfs
    python3 .github/scripts/fsrand.py -c 1000 /jfs/fsrand -v -a
    ./juicefs dump   sqlite3://test.db $dump_file

    [[ -d /var/jfs1/myjfs ]] && rm -rf /var/jfs2/myjfs
    # ./juicefs format sqlite3://test2.db myjfs --bucket /var/jfs2
    ./juicefs load   sqlite3://test2.db $dump_file   
    ./juicefs mount -d sqlite3://test2.db /jfs2

    diff -ur /jfs/fsrand /jfs2/fsrand --no-dereference
    ./juicefs umount /jfs
    ./juicefs umount /jfs2

    uuid=$(./juicefs status sqlite3://test.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test.db $uuid

    uuid=$(./juicefs status sqlite3://test2.db | grep UUID | cut -d '"' -f 4) 
    ./juicefs destroy --force sqlite3://test2.db $uuid
}

function_names=$(sed -nE '/^test_[^ ()]+ *\(\)/ { s/^\s*//; s/ *\(\).*//; p; }' "$0")
for func in ${function_names}; do
    echo Start Test: $func
    "${func}"
    echo Finish Test: $func succeeded
done