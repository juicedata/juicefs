#!/bin/bash

#  JuiceFS, Copyright 2021 Juicedata, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

set -e
sudo apt-get update
sudo apt-get install openjdk-8-jdk -y

HADOOP_VERSION="2.10.2"
wget -q https://dlcdn.apache.org/hadoop/common/hadoop-2.10.2/hadoop-2.10.2.tar.gz
mkdir ~/app
tar -zxf hadoop-${HADOOP_VERSION}.tar.gz -C ~/app

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

for i in {1..3} ; do
  ProcNumber=$( jps |grep -w DataNode|wc -l)
  if [ ${ProcNumber} -lt 1 ];then
    echo "current java process:"
    jps
    echo "The DataNode is not running, Retry for the $i time..."
    ./start-dfs.sh
  fi
done

echo "hello world" > /tmp/testfile
cd ~/app/hadoop-${HADOOP_VERSION}/bin
./hdfs dfs -put /tmp/testfile /
./hdfs dfs -rm /testfile
./hdfs dfs -chmod 777 /

echo "hdfs started successfully"
