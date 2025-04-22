#!/bin/bash -e
source .github/scripts/common/common_win.sh

[[ -z "$META_URL" ]] && META_URL=redis://127.0.0.1:6379/1


wget https://dl.min.io/client/mc/release/windows-amd64/archive/mc.RELEASE.2021-04-22T17-40-00Z -O mc.exe
chmod +x mc.exe
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=admin123
export MINIO_REFRESH_IAM_INTERVAL=10s

prepare_test()
{
    kill_gateway 9001 || true
    kill_gateway 9002 || true
    prepare_win_test
}

kill_gateway() {
    port=$1
    for pid in $(netstat -ano | findstr ":$port" | findstr "LISTENING" | awk '{print $5}'); do
        taskkill //F //PID $pid
    done
}

trap 'kill_gateway 9001; kill_gateway 9002' EXIT

start_two_gateway()
{
    prepare_test
    ./juicefs.exe format $META_URL myjfs  --trash-days 0
    ./juicefs.exe mount -d $META_URL z:
    export MINIO_ROOT_USER=admin
    export MINIO_ROOT_PASSWORD=admin123
    nohup ./juicefs.exe gateway $META_URL 127.0.0.1:9001 --multi-buckets --keep-etag --object-tag --log=gateway1.log &
    sleep 1
    nohup ./juicefs.exe gateway $META_URL 127.0.0.1:9002 --multi-buckets --keep-etag --object-tag --log=gateway2.log &
    sleep 2
    ./mc.exe alias set gateway1 http://127.0.0.1:9001 admin admin123
    ./mc.exe alias set gateway2 http://127.0.0.1:9002 admin admin123
}

test_user_management()
{
    prepare_test
    start_two_gateway
    ./mc.exe admin user add gateway1 user1 admin123
    sleep 12
    user=$(./mc.exe admin user list gateway2 | grep user1) || true
    if [ -z "$user" ]
    then
      echo "user synchronization error"
      exit 1
    fi
    ./mc.exe mb gateway1/test1
    ./mc.exe alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    if ./mc.exe cp mc.exe gateway1_user1/test1/file1
    then
      echo "By default, the user has no read and write permission"
      exit 1
    fi
    ./mc.exe admin policy set gateway1 readwrite user=user1
    if ./mc.exe cp mc.exe gateway1_user1/test1/file1
    then 
      echo "readwrite policy can read and write objects" 
    else
      echo "set readwrite policy fail"
      exit 1
    fi
    ./mc.exe cp gateway2/test1/file1 .
    compare_md5sum file1 mc.exe
    ./mc.exe admin user disable gateway1 user1
    ./mc.exe admin user remove gateway2 user1
    sleep 12
    user=$(./mc.exe admin user list gateway1 | grep user1) || true
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
    ./mc.exe admin user add gateway1 user1 admin123
    ./mc.exe admin user add gateway1 user2 admin123
    ./mc.exe admin user add gateway1 user3 admin123
    ./mc.exe admin group add gateway1 testcents user1 user2 user3
    result=$(./mc.exe admin group info gateway1 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
    ./mc.exe admin policy set gateway1 readwrite group=testcents
    sleep 5
    ./mc.exe alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    ./mc.exe mb gateway1/test1
    if ./mc.exe cp mc.exe gateway1_user1/test1/file1
    then
      echo "readwrite policy can read write"
    else
      echo "the readwrite group has no read and write permission"
      exit 1
    fi
    ./mc.exe admin policy set gateway1 readonly group=testcents
    sleep 5
    if ./mc.exe cp mc.exe gateway1_user1/test1/file1
    then
      echo "readonly group policy can not write"
      exit 1
    else
      echo "the readonly group has no write permission"
    fi

    ./mc.exe admin group remove gateway1 testcents user1 user2 user3 
    ./mc.exe admin group remove gateway1 testcents
}

test_mult_gateways_set_group()
{
    prepare_test
    start_two_gateway
    ./mc.exe admin user add gateway1 user1 admin123
    ./mc.exe admin user add gateway1 user2 admin123
    ./mc.exe admin user add gateway1 user3 admin123
    ./mc.exe admin group add gateway1 testcents user1 user2 user3
    ./mc.exe admin group disable gateway2 testcents
    sleep 12
    result=$(./mc.exe admin group info gateway2 testcents | grep Members |awk '{print $2}') || true
    if [ "$result" != "user1,user2,user3" ]
    then
      echo "error,result is '$result'"
      exit 1
    fi
    ./mc.exe admin group enable gateway1 testcents
    ./mc.exe admin user add gateway1 user4 admin123
    ./mc.exe admin group add gateway1 testcents user4
    sleep 1
    ./mc.exe admin group disable gateway2 testcents
    sleep 12
    result=$(./mc.exe admin group info gateway2 testcents | grep Members |awk '{print $2}') || true
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
    ./mc.exe admin user add gateway1 user1 admin123
    ./mc.exe admin policy set gateway1 consoleAdmin user=user1
    ./mc.exe alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    ./mc.exe admin user svcacct add gateway1_user1 user1 --access-key 12345678 --secret-key 12345678
    ./mc.exe admin user svcacct info gateway1_user1 12345678
    ./mc.exe admin user svcacct set gateway1_user1 12345678 --secret-key 123456789
    ./mc.exe alias set svcacct1 http://127.0.0.1:9001 12345678 123456789
    ./mc.exe mb svcacct1/test1
    if ./mc.exe cp mc.exe svcacct1/test1/file1
    then
      echo "svcacct user consoleAdmin policy can read write"
    else
      echo "the svcacct user has no read and write permission"
      exit 1
    fi
    ./mc.exe admin user svcacct disable gateway1_user1 12345678
    ./mc.exe admin user svcacct rm gateway1_user1 12345678
}

test_user_admin_svcacct_add()
{
    prepare_test
    start_two_gateway
    ./mc.exe admin user add gateway1 user1 admin123
    ./mc.exe admin policy set gateway1 readwrite user=user1
    ./mc.exe admin user svcacct add gateway1 user1 --access-key 12345678 --secret-key 12345678
    ./mc.exe admin user svcacct info gateway1 12345678
    ./mc.exe admin user svcacct set gateway1 12345678 --secret-key 12345678910
    ./mc.exe alias set svcacct1 http://127.0.0.1:9001 12345678 12345678910
    ./mc.exe mb svcacct1/test1
    if ./mc.exe cp mc.exe svcacct1/test1/file1
    then
      echo "amdin user can do svcacct "
    else
      echo "the svcacct user has no read and write permission"
      exit 1
    fi
    ./mc.exe admin user svcacct disable gateway1 12345678
    ./mc.exe admin user svcacct rm gateway1 12345678
}

test_user_sts()
{
    prepare_test
    start_two_gateway
    ./mc.exe admin user add gateway1 user1 admin123
    ./mc.exe admin policy set gateway1 consoleAdmin user=user1
    ./mc.exe alias set gateway1_user1 http://127.0.0.1:9001 user1 admin123
    git clone https://github.com/juicedata/minio.git -b gateway-1.1
    ./mc.exe mb gateway1_user1/test1
    ./mc.exe cp mc.exe gateway1_user1/test1/mc
    cd minio
    go run docs/sts/assume-role.go -sts-ep http://127.0.0.1:9001 -u user1 -p admin123 -b test1 -d
    go run docs/sts/assume-role.go -sts-ep http://127.0.0.1:9001 -u user1 -p admin123 -b test1
    cd -
    ./mc.exe admin user remove gateway1 user1     
}


test_change_credentials()
{
    prepare_test
    start_two_gateway
    ./mc.exe mb gateway1/test1
    ./mc.exe cp mc.exe gateway1/test1/file1
    kill_gateway 9001 || true
    kill_gateway 9002 || true
    export MINIO_ROOT_USER=newadmin
    export MINIO_ROOT_PASSWORD=newadmin123
    export MINIO_ROOT_USER_OLD=admin
    export MINIO_ROOT_PASSWORD_OLD=admin123
    nohup ./juicefs.exe gateway $META_URL 127.0.0.1:9001 --multi-buckets --keep-etag --object-tag --log=gateway1.log &
    nohup ./juicefs.exe gateway $META_URL 127.0.0.1:9002 --multi-buckets --keep-etag --object-tag --log=gateway2.log &
    sleep 5
    ./mc.exe alias set gateway1 http://127.0.0.1:9001 newadmin newadmin123
    ./mc.exe alias set gateway2 http://127.0.0.1:9002 newadmin newadmin123
    ./mc.exe cp gateway1/test1/file1 file1
    ./mc.exe cp gateway2/test1/file1 file2
    compare_md5sum file1 mc.exe
    compare_md5sum file2 mc.exe  
}

source .github/scripts/common/run_test.sh && run_test $@

