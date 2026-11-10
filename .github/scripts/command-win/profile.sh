#!/bin/bash -e
source .github/scripts/common/common_win.sh


[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1


test_profile()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs
    ./juicefs.exe mount -d $META_URL z: --fuse-access-log c:/fuse.log --access-log c:/fsop.log --log c:/mount.log
    ./juicefs.exe mdtest $META_URL //d --depth 3 --dirs 3 --files 10 --threads 5 
    timeout 5s ./juicefs profile /z/.accesslog || EXIT_CODE=$?
    if [ "$EXIT_CODE" = "124" ]; then
        echo "juicefs profile success"
    else
        echo "juicefs profile failed"
        exit 1
    fi
}

source .github/scripts/common/run_test.sh && run_test $@
