#!/bin/bash

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