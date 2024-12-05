#!/bin/bash -e
source .github/scripts/start_meta_engine.sh
[ -z "$TEST" ] && echo "TEST is not set" && exit 1

# check port is ready until 60s, sleep 1s for each query
check_port() {
    port=$1
    echo "check for port:" $port
    for i in {1..30}; do
        sudo lsof -i :$port && echo "port is available: $port after $i sec" && return 0 ||
            (echo "port is not available after $i" && sleep 1)
    done
    echo "service not ready on: $port" && exit 1
}

install_mysql() {
    sudo service mysql start
    sudo mysql -uroot -proot -e "use mysql;alter user 'root'@'localhost' identified with mysql_native_password by '';"
    sudo mysql -e "create database dev;"
    sudo mysql -e "create database dev2;"
    check_port 3306
}

install_postgres() {
    sudo service postgresql start
    sudo chmod 777 /etc/postgresql/*/main/pg_hba.conf
    sudo sed -i "s?local.*all.*postgres.*peer?local   all             postgres                                trust?" /etc/postgresql/*/main/pg_hba.conf
    sudo sed -i "s?host.*all.*all.*32.*scram-sha-256?host    all             all             127.0.0.1/32            trust?" /etc/postgresql/*/main/pg_hba.conf
    sudo sed -i "s?host.*all.*all.*128.*scram-sha-256?host    all             all             ::1/128                 trust?" /etc/postgresql/*/main/pg_hba.conf
    cat /etc/postgresql/*/main/pg_hba.conf
    sudo service postgresql restart
    psql -c "create user runner superuser;" -U postgres
    sudo service postgresql restart
    psql -c 'create database test;' -U postgres
}

install_etcd() {
    docker run -d \
        -p 3379:2379 \
        -p 3380:2380 \
        --name etcd_3_5_7 \
        quay.io/coreos/etcd:v3.5.7 \
        /usr/local/bin/etcd --data-dir=/etcd-data --name node1 \
        --listen-client-urls http://0.0.0.0:2379 \
        --advertise-client-urls http://0.0.0.0:3379 \
        --listen-peer-urls http://0.0.0.0:2380 \
        --initial-advertise-peer-urls http://0.0.0.0:2380 \
        --initial-cluster node1=http://0.0.0.0:2380
    check_port 3379
    check_port 3380
}

install_keydb() {
    echo "deb https://download.keydb.dev/open-source-dist $(lsb_release -sc) main" | sudo tee /etc/apt/sources.list.d/keydb.list
    sudo wget -O /etc/apt/trusted.gpg.d/keydb.gpg https://download.keydb.dev/open-source-dist/keyring.gpg
    sudo .github/scripts/apt_install.sh keydb
    keydb-server --storage-provider flash /tmp/ --port 6378 --bind 127.0.0.1 --daemonize yes
    keydb-server --port 6377 --bind 127.0.0.1 --daemonize yes
    check_port 6377
    check_port 6378
}

install_minio() {
    docker run -d -p 9000:9000 -p 9001:9001 -e "MINIO_ROOT_USER=testUser" -e "MINIO_ROOT_PASSWORD=testUserPassword" quay.io/minio/minio:RELEASE.2022-01-25T19-56-04Z server /data --console-address ":9001"
    go install github.com/minio/mc@RELEASE.2022-01-07T06-01-38Z && mc config host add local http://127.0.0.1:9000 testUser testUserPassword && mc mb local/testbucket
}

install_fdb() {
    wget -O /home/travis/.m2/foundationdb-clients_6.3.23-1_amd64.deb https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-clients_6.3.23-1_amd64.deb
    wget -O /home/travis/.m2/foundationdb-server_6.3.23-1_amd64.deb https://github.com/apple/foundationdb/releases/download/6.3.23/foundationdb-server_6.3.23-1_amd64.deb
    sudo dpkg -i /home/travis/.m2/foundationdb-clients_6.3.23-1_amd64.deb /home/travis/.m2/foundationdb-server_6.3.23-1_amd64.deb
    check_port 4500
}

install_gluster() {
    sudo systemctl start glusterd.service
    mkdir -p /tmp/gluster/gv0
    sudo hostname jfstest
    sudo gluster volume create gv0 jfstest:/tmp/gluster/gv0 force
    sudo gluster volume start gv0
    sudo gluster volume info gv0
}

install_litmus() {
    wget -O /home/travis/.m2/litmus-0.13.tar.gz http://www.webdav.org/neon/litmus/litmus-0.13.tar.gz
    tar -zxvf /home/travis/.m2/litmus-0.13.tar.gz -C /home/travis/.m2/
    cd /home/travis/.m2/litmus-0.13/ && ./configure && make && cd -
}

install_webdav() {
    wget -O /home/travis/.m2/rclone-v1.57.0-linux-amd64.zip --no-check-certificate https://downloads.rclone.org/v1.57.0/rclone-v1.57.0-linux-amd64.zip
    unzip /home/travis/.m2/rclone-v1.57.0-linux-amd64.zip -d /home/travis/.m2/
    nohup /home/travis/.m2/rclone-v1.57.0-linux-amd64/rclone serve webdav local --addr 127.0.0.1:9007 >>rclone.log 2>&1 &
}

prepare_db() {
    case "$TEST" in
    "test.meta.core")
        retry install_tikv
        install_mysql
        ;;
    "test.meta.non-core")
        install_postgres
        install_etcd
        install_keydb
        ;;
    "test.cmd")
        install_minio
        install_litmus
        ;;
    "test.fdb")
        install_fdb
        ;;
    "test.pkg")
        install_mysql
        retry install_tikv
        install_minio
        install_gluster
        install_webdav
        docker run -d --name sftp -p 2222:22 juicedata/ci-sftp
        install_etcd
        .github/scripts/setup-hdfs.sh
        ;;
    *)
        echo "Test: $TEST is not valid" && exit 1
        ;;
    esac
}

prepare_db
