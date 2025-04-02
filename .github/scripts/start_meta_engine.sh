#!/bin/bash -e
retry() {
    local retries=5
    local delay=3
    for i in $(seq 1 $retries); do
        set +e
        ( set -e; "$@" )
        exit=$?
        set -e
        if [ $exit == 0 ]; then
            echo "run $@ succceed"
            return $exit
        elif [ $i ==  $retries ]; then
            echo "Retry failed after $i attempts."
            exit $exit
        else
            echo "Retry in $delay seconds..."
            sleep $delay
        fi
    done
}

install_tikv(){
    [[ ! -d tcli ]] && git clone https://github.com/c4pt0r/tcli
    make -C tcli && sudo cp tcli/bin/tcli /usr/local/bin
    # retry because of: https://github.com/pingcap/tiup/issues/2057
    user=$(whoami)
    echo user is $user
    if [[ "$user" == "root" ]]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sudo sh
        tiup=/root/.tiup/bin/tiup
    elif [[ "$user" == "runner" ]]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
        tiup=/home/runner/.tiup/bin/tiup
    else
        echo "Unknown user $user"
        exit 1
    fi
    echo tiup is $tiup
    $tiup playground --mode tikv-slim > tikv.log 2>&1  &
    pid=$!
    timeout=60
    count=0
    while true; do
        echo 'head -1' > /tmp/head.txt
        lsof -i:2379 && pgrep pd-server && tcli -pd 127.0.0.1:2379 < /tmp/head.txt && exit_code=0 || exit_code=$?
        if [ $exit_code -eq 0 ]; then
            echo "TiDB is running."
            exit 0
        fi
        sleep 1
        count=$((count+1))
        if [ $count -eq $timeout ]; then
            echo "TiDB failed to start within $timeout seconds."
            kill -9 $pid || true
            exit 1
        fi
    done
}

install_tidb(){
    user=$(whoami)
    echo user is $user
    if [[ "$user" == "root" ]]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sudo sh
        tiup=/root/.tiup/bin/tiup
    elif [[ "$user" == "runner" ]]; then
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
        tiup=/home/runner/.tiup/bin/tiup
    else
        echo "Unknown user $user"
        exit 1
    fi
    echo tiup is $tiup
    
    $tiup playground 5.4.0 > tidb.log 2>&1  &
    pid=$!
    timeout=60
    count=0
    while true; do
        lsof -i:4000 && pgrep pd-server && mysql -h127.0.0.1 -P4000 -uroot -e "select version();" && exit_code=0 || exit_code=$?
        if [ $exit_code -eq 0 ]; then
            echo "TiDB is running."
            exit 0
        fi
        sleep 1
        count=$((count+1))
        if [ $count -eq $timeout ]; then
            echo "TiDB failed to start within $timeout seconds."
            kill -9 $pid || true
            exit 1
        fi
    done
}

start_meta_engine(){
    meta=$1
    storage=$2
    if [ "$meta" == "mysql" ]; then
        sudo /etc/init.d/mysql start
    elif [ "$meta" == "redis" ]; then
        sudo .github/scripts/apt_install.sh  redis-tools redis-server
    elif [ "$meta" == "tikv" ]; then
        retry install_tikv
    elif [ "$meta" == "badger" ]; then
        sudo go get github.com/dgraph-io/badger/v3
    elif [ "$meta" == "mariadb" ]; then
        if lsof -i:3306; then
            echo "mariadb is already running"
        else
            docker run -p 127.0.0.1:3306:3306  --name mdb -e MARIADB_ROOT_PASSWORD=root -d mariadb:latest
            sleep 10
        fi
    elif [ "$meta" == "tidb" ]; then
        retry install_tidb
        mysql -h127.0.0.1 -P4000 -uroot -e "set global tidb_enable_noop_functions=1;"
    elif [ "$meta" == "etcd" ]; then
        sudo .github/scripts/apt_install.sh etcd
    elif [ "$meta" == "fdb" ]; then
        if lsof -i:4500; then
            echo "fdb is already running"
        else  
            docker run --name fdb --rm -d -p 4500:4500 foundationdb/foundationdb:6.3.23
            sleep 5
            docker exec fdb fdbcli --exec "configure new single memory"
            echo "docker:docker@127.0.0.1:4500" > /home/runner/fdb.cluster
            fdbcli -C /home/runner/fdb.cluster --exec "status"
        fi
    elif [ "$meta" == "ob" ]; then
        docker rm obstandalone --force || echo "remove obstandalone failed"
        docker run -p 2881:2881 --name obstandalone -e MINI_MODE=1 -d oceanbase/oceanbase-ce
        sleep 60
        mysql -h127.0.0.1 -P2881 -uroot -e "ALTER SYSTEM SET _ob_enable_prepared_statement=TRUE;"
    elif [ "$meta" == "postgres" ]; then
        echo "start postgres"
        lsof -i:5432 || true
        if lsof -i:5432; then
            echo "postgres is already running"
        else
            # default max_connections is 100.
            docker run --name postgresql \
                -e POSTGRES_USER=postgres \
                -e POSTGRES_PASSWORD=postgres \
                -p 5432:5432 \
                -v /tmp/postgresql:/var/lib/postgresql/data \
                -d postgres \
                -N 300
            sleep 10
            docker exec -i postgresql psql -U postgres -c "SHOW max_connections;"
        fi
    fi
    
    if [ "$storage" == "minio" ]; then
        if ! docker ps | grep "minio/minio"; then
            docker run -d -p 9000:9000 --name minio \
                -e "MINIO_ACCESS_KEY=minioadmin" \
                -e "MINIO_SECRET_KEY=minioadmin" \
                -v /tmp/data:/data \
                -v /tmp/config:/root/.minio \
                minio/minio server /data
            sleep 3s
        fi
        [ ! -x mc ] && wget -q https://dl.minio.io/client/mc/release/linux-amd64/mc && chmod +x mc
        ./mc config host add myminio http://127.0.0.1:9000 minioadmin minioadmin
    elif [ "$storage" == "gluster" ]; then
        dpkg -s glusterfs-server || .github/scripts/apt_install.sh glusterfs-server
        systemctl start glusterd.service
    elif [ "$meta" != "postgres" ] && [ "$storage" == "postgres" ]; then
        echo "start postgres"
        if lsof -i:5432; then
            echo "postgres is already running"
        else
            docker run --name postgresql \
                -e POSTGRES_USER=postgres \
                -e POSTGRES_PASSWORD=postgres \
                -p 5432:5432 \
                -v /tmp/data:/var/lib/postgresql/data \
                -d postgres
            sleep 10
        fi
    elif [ "$meta" != "mysql" ] && [ "$storage" == "mysql" ]; then
        echo "start mysql"
        sudo /etc/init.d/mysql start
    fi
}

get_meta_url(){
    meta=$1
    if [ "$meta" == "postgres" ]; then
        meta_url="postgres://postgres:postgres@127.0.0.1:5432/test?sslmode=disable"
    elif [ "$meta" == "mysql" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/test"
    elif [ "$meta" == "redis" ]; then
        meta_url="redis://127.0.0.1:6379/1"
    elif [ "$meta" == "sqlite3" ]; then
        meta_url="sqlite3://test.db"
    elif [ "$meta" == "tikv" ]; then
        meta_url="tikv://127.0.0.1:2379/test"
    elif [ "$meta" == "badger" ]; then
        meta_url="badger:///tmp/test"
    elif [ "$meta" == "mariadb" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/test"
    elif [ "$meta" == "tidb" ]; then
        meta_url="mysql://root:@(127.0.0.1:4000)/test"
    elif [ "$meta" == "etcd" ]; then
        meta_url="etcd://localhost:2379/test"
    elif [ "$meta" == "fdb" ]; then
        meta_url="fdb:///home/runner/fdb.cluster?prefix=jfs"
    elif [ "$meta" == "ob" ]; then
        meta_url="mysql://root:@\\(127.0.0.1:2881\\)/test"
    else
        echo >&2 "<FATAL>: meta $meta is not supported"
        meta_url=""
        return 1
    fi
    echo $meta_url
    return 0
}

get_meta_url2(){
    meta=$1
    if [ "$meta" == "postgres" ]; then
        meta_url="postgres://postgres:postgres@127.0.0.1:5432/test2?sslmode=disable"
    elif [ "$meta" == "mysql" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/test2"
    elif [ "$meta" == "redis" ]; then
        meta_url="redis://127.0.0.1:6379/2"
    elif [ "$meta" == "sqlite3" ]; then
        meta_url="sqlite3://test2.db"
    elif [ "$meta" == "tikv" ]; then
        meta_url="tikv://127.0.0.1:2379/jfs2"
    elif [ "$meta" == "badger" ]; then
        meta_url="badger:///tmp/test2"
    elif [ "$meta" == "mariadb" ]; then
        meta_url="mysql://root:root@(127.0.0.1)/test2"
    elif [ "$meta" == "tidb" ]; then
        meta_url="mysql://root:@(127.0.0.1:4000)/test2"
    elif [ "$meta" == "etcd" ]; then
        meta_url="etcd://localhost:2379/test2"
    elif [ "$meta" == "fdb" ]; then
        meta_url="fdb:///home/runner/fdb.cluster?prefix=jfs2"
    elif [ "$meta" == "ob" ]; then
        meta_url="mysql://root:@\\(127.0.0.1:2881\\)/test2"
    else
        echo >&2 "<FATAL>: meta $meta is not supported"
        meta_url=""
        return 1
    fi
    echo $meta_url
    return 0
}

create_database(){
    meta_url=$1
    db_name=$(basename $meta_url | awk -F? '{print $1}')
    if [[ "$meta_url" == mysql* ]]; then
        user=$(echo $meta_url |  awk -F/ '{print $3}' | awk -F@ '{print $1}' | awk -F: '{print $1}')
        password=$(echo $meta_url |  awk -F/ '{print $3}' | awk -F@ '{print $1}' | awk -F: '{print $2}')
        test -n "$password" && password="-p$password" || password=""
        host=$(basename $(dirname $meta_url) | awk -F@ '{print $2}'| sed 's/(//g' | sed 's/)//g' | awk -F: '{print $1}')
        port=$(basename $(dirname $meta_url) | awk -F@ '{print $2}'| sed 's/(//g' | sed 's/)//g' | awk -F: '{print $2}')
        test -z "$port" && port="3306"
        echo user=$user, password=$password, host=$host, port=$port, db_name=$db_name
        if [ "$#" -eq 2 ]; then
            echo isolation_level=$2
            mysql -u$user $password -h $host -P $port -e "set global transaction isolation level $2;"
            mysql -u$user $password -h $host -P $port -e "show variables like '%isolation%;'"
        fi
        mysql -u$user $password -h $host -P $port -e "drop database if exists $db_name; create database $db_name;"
        elif [[ "$meta_url" == postgres* ]]; then
            export PGPASSWORD="postgres"
            printf "\set AUTOCOMMIT on\ndrop database if exists $db_name; create database $db_name; " |  psql -U postgres -h localhost
        if [ "$#" -eq 2 ]; then
            echo isolation_level=$2
            printf "\set AUTOCOMMIT on\nALTER DATABASE $db_name SET DEFAULT_TRANSACTION_ISOLATION TO '$2';" |  psql -U postgres -h localhost
        fi
    fi
}
