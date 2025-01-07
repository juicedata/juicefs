#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
}

test_acl_with_kernel_check()
{
    prepare_test
    ./juicefs format $META_URL myjfs --enable-acl --trash-days 0
    ./juicefs mount -d $META_URL /tmp/jfs
    python3 .github/scripts/hypo/fs_acl_test.py 
}

test_acl_with_user_space_check()
{
    prepare_test
    ./juicefs format $META_URL myjfs --enable-acl --trash-days 0
    ./juicefs mount -d $META_URL /tmp/jfs --non-default-permission
    python3 .github/scripts/hypo/fs_acl_test.py 
}

test_modify_acl_config()
{
    prepare_test
    ./juicefs format $META_URL myjfs --trash-days 0
    ./juicefs mount -d $META_URL /tmp/jfs
    touch /tmp/jfs/test
    setfacl -m u:root:rw /tmp/jfs/test && echo "setfacl should failed" && exit 1
    ./juicefs config $META_URL --enable-acl=true
    ./juicefs mount -d $META_URL /tmp/jfs
    setfacl -m u:root:rw /tmp/jfs/test
    ./juicefs config $META_URL --enable-acl
    umount_jfs /tmp/jfs $META_URL
    ./juicefs mount -d $META_URL /tmp/jfs
    setfacl -m u:root:rw /tmp/jfs/test
    ./juicefs config $META_URL --enable-acl=false && echo "should not disable acl" && exit 1 || true 
    ./juicefs config $META_URL | grep EnableACL | grep "true" || (echo "EnableACL should be true" && exit 1) 
}

source .github/scripts/common/run_test.sh && run_test $@

