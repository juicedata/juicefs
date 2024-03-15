#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_acl_with_kernel_check()
{
    prepare_test
    ./juicefs format $META_URL myjfs --enable-acl --trash-days 0
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/hypo/acl_test.py 
}

test_acl_with_user_space_check()
{
    prepare_test
    ./juicefs format $META_URL myjfs --enable-acl --trash-days 0
    ./juicefs mount -d $META_URL /jfs --no-default-permission
    python3 .github/scripts/hypo/acl_test.py 
}

test_modify_acl_config()
{
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs config $META_URL --enable-acl
    ./juicefs mount -d $META_URL /jfs
    python3 .github/scripts/hypo/acl_test.py
    ./juicefs config $META_URL --enable-acl False 
    ./juicefs config $META_URL | grep EnableACL | grep "true" || (echo "EnableACL should be true" && exit 1) 
}

source .github/scripts/common/run_test.sh && run_test $@

