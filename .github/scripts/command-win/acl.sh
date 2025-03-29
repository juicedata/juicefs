#!/bin/bash -e
source .github/scripts/common/common_win.sh

[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1

test_modify_acl_config()
{
    prepare_win_test
    ./juicefs.exe format $META_URL myjfs --trash-days 0
    ./juicefs.exe mount -d $META_URL z:
    touch z:test
    setfacl -m u:root:rw z:test && echo "setfacl should failed" && exit 1
    ./juicefs.exe config $META_URL --enable-acl=true
    ./juicefs.exe umount z:
    ./juicefs.exe mount -d $META_URL z:
    setfacl -m u:root:rw z:test
    ./juicefs.exe config $META_URL --enable-acl=false && echo "should not disable acl" && exit 1 || true 
    ./juicefs.exe config $META_URL | grep EnableACL | grep "true" || (echo "EnableACL should be true" && exit 1) 
}

source .github/scripts/common/run_test.sh && run_test $@

