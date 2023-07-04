#!/bin/bash
set -e 
retry(){
  local n=0
  local max=5
  local delay=5

  while true; do
    "$@" && break || {
      if [[ $n -lt $max ]]; then
        ((n++))
        echo "Command failed. Attempt $n/$max:"
        sleep $delay;
      else
        echo "The command has failed after $n attempts."
        return 1
      fi
    }
  done
}

install_tikv(){
  # retry because of: https://github.com/pingcap/tiup/issues/2057
  curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
  user=$(whoami)
  if [ "$user" == "root" ]; then
    tiup=/root/.tiup/bin/tiup
  elif [ "$user" == "runner" ]; then
    tiup=/home/runner/.tiup/bin/tiup
  else
    echo "Unknown user $user"
    exit 1
  fi
  
  $tiup playground --mode tikv-slim 2>&1 > tikv.log &
  pid=$!
  sleep 60
  echo 'head -1' > /tmp/head.txt
  lsof -i:2379 && pgrep pd-server && tcli -pd 127.0.0.1:2379 < /tmp/head.txt
  ret=$?
  if [ $ret -eq 0 ]; then
    echo "TiKV is running."
  else
    echo "TiKV failed to start."
    kill -9 $pid
  fi
  rm -rf /tmp/head.txt
  return $ret
}

install_tidb(){
  curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
  user=$(whoami)
  if [ "$user" == "root" ]; then
    tiup=/root/.tiup/bin/tiup
  elif [ "$user" == "runner" ]; then
    tiup=/home/runner/.tiup/bin/tiup
  else
    echo "Unknown user $user"
    exit 1
  fi
  
  $tiup playground 5.4.0 2>&1 > tidb.log &
  pid=$!
  sleep 60
  lsof -i:4000 && pgrep pd-server && mysql -h127.0.0.1 -P4000 -uroot -e "select version();"
  ret=$?
  if [ $ret -eq 0 ]; then
      echo "TiDB is running."
    else
      echo "TiDB failed to start."
      kill -9 $pid
  fi
  return $ret
}

start_meta_engine(){
    meta=$1
    storage=$2
    if [ "$meta" == "mysql" ]; then
        sudo /etc/init.d/mysql start
    elif [ "$meta" == "redis" ]; then
        sudo apt-get install -y redis-tools redis-server
    elif [ "$meta" == "tikv" ]; then
        [[ ! -d tcli ]] && git clone https://github.com/c4pt0r/tcli
        cd tcli && make
        sudo cp bin/tcli /usr/local/bin
        cd -
        retry install_tikv

    elif [ "$meta" == "badger" ]; then
        sudo go get github.com/dgraph-io/badger/v3
    elif [ "$meta" == "mariadb" ]; then
        docker run -p 127.0.0.1:3306:3306  --name mdb -e MARIADB_ROOT_PASSWORD=root -d mariadb:latest
        sleep 10
    elif [ "$meta" == "tidb" ]; then
        retry install_tidb
        mysql -h127.0.0.1 -P4000 -uroot -e "set global tidb_enable_noop_functions=1;"
    elif [ "$meta" == "etcd" ]; then
        sudo apt install etcd
    elif [ "$meta" == "fdb" ]; then
        docker run --name fdb --rm -d -p 4500:4500 foundationdb/foundationdb:6.3.23
        sleep 5
        docker exec fdb fdbcli --exec "configure new single memory"
        echo "docker:docker@127.0.0.1:4500" > /home/runner/fdb.cluster 
        fdbcli -C /home/runner/fdb.cluster --exec "status"
    elif [ "$meta" == "ob" ]; then
        docker rm obstandalone --force || echo "remove obstandalone failed"
        docker run -p 2881:2881 --name obstandalone -e MINI_MODE=1 -d oceanbase/oceanbase-ce
        sleep 60
        mysql -h127.0.0.1 -P2881 -uroot -e "ALTER SYSTEM SET _ob_enable_prepared_statement=TRUE;" 
    elif [ "$meta" == "postgres" ]; then
        echo "start postgres"
        docker run --name postgresql \
            -e POSTGRES_USER=postgres \
            -e POSTGRES_PASSWORD=postgres \
            -p 5432:5432 \
            -v /tmp/data:/var/lib/postgresql/data \
            -d postgres
        sleep 10
    fi

    if [ "$storage" == "minio" ]; then
        docker run -d -p 9000:9000 --name minio \
                    -e "MINIO_ACCESS_KEY=minioadmin" \
                    -e "MINIO_SECRET_KEY=minioadmin" \
                    -v /tmp/data:/data \
                    -v /tmp/config:/root/.minio \
                    minio/minio server /data
    elif [ "$meta" != "postgres" ] && [ "$storage" == "postgres" ]; then
        echo "start postgres"
        docker run --name postgresql \
            -e POSTGRES_USER=postgres \
            -e POSTGRES_PASSWORD=postgres \
            -p 5432:5432 \
            -v /tmp/data:/var/lib/postgresql/data \
            -d postgres
        sleep 10
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
        meta_url="etcd://localhost:2379/jfs"
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
