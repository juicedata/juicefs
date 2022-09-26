#!/bin/bash


start_meta_engine(){
    meta=$1
    if [ "$meta" == "mysql" ]; then
        sudo /etc/init.d/mysql start
    elif [ "$meta" == "redis" ]; then
        sudo apt-get install -y redis-tools redis-server
    elif [ "$meta" == "tikv" ]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
        source /home/runner/.bash_profile
        source /home/runner/.profile
        tiup playground --mode tikv-slim &
        sleep 5
    elif [ "$meta" == "badgerdb" ]; then
        sudo go get github.com/dgraph-io/badger/v3
    elif [ "$meta" == "mariadb" ]; then
        docker run -p 127.0.0.1:3306:3306  --name mdb -e MARIADB_ROOT_PASSWORD=root -d mariadb:latest
        sleep 10
    elif [ "$meta" == "tidb" ]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
        source /home/runner/.profile
        tiup playground 5.4.0 &
        sleep 120
        mysql -h127.0.0.1 -P4000 -uroot -e "set global tidb_enable_noop_functions=1;"
    fi
}

get_meta_url(){
    meta=$1
    if [ "$meta" == "postgres" ]; then
        meta_url="postgres://postgres:postgres@127.0.0.1:5432/sync_test?sslmode=disable" 
    elif [ "$meta" == "mysql" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/sync_test"
    elif [ "$meta" == "redis" ]; then
        meta_url="redis://127.0.0.1:6379/1"
    elif [ "$meta" == "sqlite3" ]; then
        meta_url="sqlite3://sync-test.db"
    elif [ "$meta" == "tikv" ]; then
        meta_url="tikv://127.0.0.1:2379/load_test"
    elif [ "$meta" == "badgerdb" ]; then
        meta_url="badger://load_test"
    elif [ "$meta" == "mariadb" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/load_test"
    elif [ "$meta" == "tidb" ]; then
        meta_url="mysql://root:@(127.0.0.1:4000)/load_test"
    else
        echo "<FATAL>: meta $meta is not supported"
        meta_url=""
    fi
    return $meta_url
}