#!/bin/bash -e
prepare_win_test()
{
     net start redisredis || true
     ./juicefs.exe umount z: || true
     rm -rf C:\jfs\local/myjfs/  || true
     rm -rf C:\jfsCache\local/myjfs/ || true
     uuid=$(./juicefs.exe status $META_URL | grep UUID | cut -d '"' -f 4) || true
     ./juicefs.exe destroy --force $META_URL $uuid  || true
     redis-cli -h 127.0.0.1 -p 6379 -n 1 FLUSHDB
}

compare_md5sum(){
    file1=$1
    file2=$2
    md51=$(md5sum $file1 | awk '{print $1}')
    md52=$(md5sum $file2 | awk '{print $1}')
    # echo md51 is $md51, md52 is $md52
    if [ "$md51" != "$md52" ] ; then
        echo "md5 are different: md51 is $md51, md52 is $md52"
        exit 1
    fi
}