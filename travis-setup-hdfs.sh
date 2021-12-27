#!/bin/bash

#  JuiceFS, Copyright (C) 2021 Juicedata, Inc.
#
#  This program is free software: you can use, redistribute, and/or modify
#  it under the terms of the GNU Affero General Public License, version 3
#  or later ("AGPL"), as published by the Free Software Foundation.
#
#  This program is distributed in the hope that it will be useful, but WITHOUT
#  ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
#  FITNESS FOR A PARTICULAR PURPOSE.
#
#  You should have received a copy of the GNU Affero General Public License
#  along with this program. If not, see <http://www.gnu.org/licenses/>.
#

set -e
sudo apt-get update
sudo apt-get install openjdk-8-jdk -y

HADOOP_VERSION="2.10.1"
wget https://dlcdn.apache.org/hadoop/core/stable2/hadoop-2.10.1.tar.gz
mkdir ~/app
tar -zxvf hadoop-${HADOOP_VERSION}.tar.gz -C ~/app

sudo tee -a ~/.bashrc <<EOF
export JAVA_HOME=/usr/lib/jvm/java-8-openjdk-amd64
export JRE_HOME=\${JAVA_HOME}/jre
export CLASSPATH=.:\${JAVA_HOME}/lib:\${JRE_HOME}/lib
export PATH=\${PATH}:\${JAVA_HOME}/bin

export HADOOP_HOME=~/app/hadoop-${HADOOP_VERSION}
export HADOOP_CONF_DIR=\${HADOOP_HOME}/etc/hadoop
export PATH=\$PATH:\${HADOOP_HOME}/bin:\${HADOOP_HOME}/sbin
EOF

source ~/.bashrc
echo $HADOOP_HOME
echo $HADOOP_CONF_DIR
echo $PATH

ssh-keygen -t rsa -N '' -f ~/.ssh/id_rsa -q
cat ~/.ssh/id_rsa.pub  >> ~/.ssh/authorized_keys
chmod 700 ~/.ssh
chmod 600 ~/.ssh/authorized_keys
echo "StrictHostKeyChecking no" >> ~/.ssh/config

sed -i 's/${JAVA_HOME}/\/usr\/lib\/jvm\/java-8-openjdk-amd64/g' ~/app/hadoop-${HADOOP_VERSION}/etc/hadoop/hadoop-env.sh

sudo tee ~/app/hadoop-${HADOOP_VERSION}/etc/hadoop/core-site.xml <<EOF
    <configuration>
        <property>
            <name>fs.defaultFS</name>
            <value>hdfs://localhost:8020</value>
        </property>

        <property>
            <name>hadoop.tmp.dir</name>
            <value>${HOME}/apps/tmp</value>
        </property>
    </configuration>
EOF

sudo tee ~/app/hadoop-${HADOOP_VERSION}/etc/hadoop/hdfs-site.xml <<EOF
    <configuration>
        <property>
            <name>dfs.replication</name>
            <value>1</value>
        </property>
    </configuration>
EOF

cd ~/app/hadoop-${HADOOP_VERSION}/bin
./hdfs namenode -format
cd ~/app/hadoop-${HADOOP_VERSION}/sbin
./start-dfs.sh

jps

echo "hdfs started successfully"
