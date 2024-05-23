#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=redis
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)
wget https://dl.min.io/client/mc/release/linux-amd64/archive/mc.RELEASE.2021-04-22T17-40-00Z -O mc
chmod +x mc
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=admin123
export MINIO_REFRESH_IAM_INTERVAL=10s

prepare_test()
{
    umount_jfs /tmp/jfs $META_URL
    kill_gateway 9001
    kill_gateway 9002
    python3 .github/scripts/flush_meta.py $META_URL
    rm -rf /var/jfs/myjfs || true
    rm -rf /var/jfsCache/myjfs || true
}

kill_gateway() {
    port=$1
    lsof -i:$port || true
    lsof -t -i :$port | xargs -r kill -9 || true
}

trap 'kill_gateway 9001; kill_gateway 9002' EXIT

start_two_gateway()
{
    prepare_test
    ./juicefs format $META_URL myjfs  --trash-days 0
    ./juicefs mount -d $META_URL /tmp/jfs
    export MINIO_ROOT_USER=admin
    export MINIO_ROOT_PASSWORD=admin123
    ./juicefs gateway $META_URL 127.0.0.1:9001 --multi-buckets --keep-etag --object-tag -background
    sleep 1
    ./juicefs gateway $META_URL 127.0.0.1:9002 --multi-buckets --keep-etag --object-tag -background 
    sleep 2
    ./mc alias set gateway1 http://127.0.0.1:9001 admin admin123
    ./mc alias set gateway2 http://127.0.0.1:9002 admin admin123
}

test_user_management()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    sleep 12
    user=$(./mc admin user list gateway2 | grep user1) || true
    if [ -z "$user" ]
    then
      echo "user synchronization error"
      exit 1
    fi
    ./mc mb gateway1/test1
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "By default, the user has no read and write permission"
      exit 1
    fi
    ./mc admin policy set gateway1 readwrite user=user1
    if ./mc cp mc gateway1_user1/test1/file1
    then 
      echo "readwrite policy can read and write objects" 
    else
      echo "set readwrite policy fail"
      exit 1
    fi
    ./mc cp gateway2/test1/file1 .
    compare_md5sum file1 mc  
    ./mc admin user disable gateway1 user1
    ./mc admin user remove gateway2 user1
    sleep 12
    user=$(./mc admin user list gateway1 | grep user1) || true
    if [ ! -z "$user" ]
    then
      echo "remove user user1 fail"
      echo $user
      exit 1
    fi
}

test_group_management()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin user add gateway1 user2 admin123
    ./mc admin user add gateway1 user3 admin123
    ./mc admin group add gateway1 testcents user1 user2 user3
    result=$(./mc admin group info gateway1 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
    ./mc admin policy set gateway1 readwrite group=testcents
    sleep 5
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    ./mc mb gateway1/test1
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "readwrite policy can read write"
    else
      echo "the readwrite group has no read and write permission"
      exit 1
    fi
    ./mc admin policy set gateway1 readonly group=testcents
    sleep 5
    if ./mc cp mc gateway1_user1/test1/file1
    then
      echo "readonly group policy can not write"
      exit 1
    else
      echo "the readonly group has no write permission"
    fi

    ./mc admin group remove gateway1 testcents user1 user2 user3 
    ./mc admin group remove gateway1 testcents
}

test_mult_gateways_set_group()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin user add gateway1 user2 admin123
    ./mc admin user add gateway1 user3 admin123
    ./mc admin group add gateway1 testcents user1 user2 user3
    ./mc admin group disable gateway2 testcents
    sleep 12
    result=$(./mc admin group info gateway2 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
    ./mc admin group enable gateway1 testcents
    ./mc admin user add gateway1 user4 admin123
    ./mc admin group add gateway1 testcents user4
    sleep 1
    ./mc admin group disable gateway2 testcents
    sleep 12
    result=$(./mc admin group info gateway2 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3,user4" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
}

test_user_svcacct_add()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin policy set gateway1 consoleAdmin user=user1
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    ./mc admin user svcacct add gateway1_user1 user1 --access-key 12345678 --secret-key 12345678
    ./mc admin user svcacct info gateway1_user1 12345678
    ./mc admin user svcacct set gateway1_user1 12345678 --secret-key 123456789
    ./mc alias set svcacct1 http://127.0.0.1:9001 12345678 123456789
    ./mc mb svcacct1/test1
    if ./mc cp mc svcacct1/test1/file1
    then
      echo "svcacct user consoleAdmin policy can read write"
    else
      echo "the svcacct user has no read and write permission"
      exit 1
    fi
    ./mc admin user svcacct disable gateway1_user1 12345678
    ./mc admin user svcacct rm gateway1_user1 12345678
}

test_user_admin_svcacct_add()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin policy set gateway1 readwrite user=user1
    ./mc admin user svcacct add gateway1 user1 --access-key 12345678 --secret-key 12345678
    ./mc admin user svcacct info gateway1 12345678
    ./mc admin user svcacct set gateway1 12345678 --secret-key 12345678910
    ./mc alias set svcacct1 http://127.0.0.1:9001 12345678 12345678910
    ./mc mb svcacct1/test1
    if ./mc cp mc svcacct1/test1/file1
    then
      echo "amdin user can do svcacct "
    else
      echo "the svcacct user has no read and write permission"
      exit 1
    fi
    ./mc admin user svcacct disable gateway1 12345678
    ./mc admin user svcacct rm gateway1 12345678
}

test_user_sts()
{
    prepare_test
    start_two_gateway
    ./mc admin user add gateway1 user1 admin123
    ./mc admin policy set gateway1 consoleAdmin user=user1
    ./mc alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    git clone https://github.com/juicedata/minio.git -b gateway-1.1
    ./mc mb gateway1_user1/test1
    ./mc cp mc gateway1_user1/test1/mc
    cd minio
    go run docs/sts/assume-role.go -sts-ep http://127.0.0.1:9001 -u user1 -p admin123 -b test1 -d
    go run docs/sts/assume-role.go -sts-ep http://127.0.0.1:9001 -u user1 -p admin123 -b test1
    cd -
    ./mc admin user remove gateway1 user1     
}


test_change_credentials()
{
    prepare_test
    start_two_gateway
    ./mc mb gateway1/test1
    ./mc cp mc gateway1/test1/file1
    lsof -i :9001 | awk 'NR!=1 {print $2}' | xargs -r kill -9 || true
    lsof -i :9002 | awk 'NR!=1 {print $2}' | xargs -r kill -9 || true
    export MINIO_ROOT_USER=newadmin
    export MINIO_ROOT_PASSWORD=newadmin123
    export MINIO_ROOT_USER_OLD=admin
    export MINIO_ROOT_PASSWORD_OLD=admin123
    ./juicefs gateway $META_URL 127.0.0.1:9001 --multi-buckets --keep-etag --object-tag -background
    ./juicefs gateway $META_URL 127.0.0.1:9002 --multi-buckets --keep-etag --object-tag -background
    sleep 5
    ./mc alias set gateway1 http://127.0.0.1:9001 newadmin newadmin123
    ./mc alias set gateway2 http://127.0.0.1:9002 newadmin newadmin123
    ./mc cp gateway1/test1/file1 file1
    ./mc cp gateway2/test1/file1 file2
    compare_md5sum file1 mc
    compare_md5sum file2 mc  
}

source .github/scripts/common/run_test.sh && run_test $@

