#!/bin/sh

set -ex

UBUNTU_CODENAME=$(lsb_release -c | awk '{print $2}')

sudo tee /etc/apt/sources.list.d/cdh.list <<EOF
deb [arch=amd64] http://archive.cloudera.com/cdh5/ubuntu/$UBUNTU_CODENAME/amd64/cdh $UBUNTU_CODENAME-cdh5 contrib
EOF

sudo tee /etc/apt/preferences.d/cloudera.pref <<EOF
Package: *
Pin: release o=Cloudera, l=Cloudera
Pin-Priority: 501
EOF

sudo apt-get update

CONF_AUTHENTICATION="simple"

sudo mkdir -p /etc/hadoop/conf.gohdfs
sudo tee /etc/hadoop/conf.gohdfs/core-site.xml <<EOF
<configuration>
  <property>
    <name>fs.defaultFS</name>
    <value>hdfs://localhost:9000</value>
  </property>
  <property>
    <name>hadoop.security.authentication</name>
    <value>$CONF_AUTHENTICATION</value>
  </property>
</configuration>
EOF

sudo tee /etc/hadoop/conf.gohdfs/hdfs-site.xml <<EOF
<configuration>
  <property>
    <name>dfs.namenode.name.dir</name>
    <value>/opt/hdfs/name</value>
  </property>
  <property>
    <name>dfs.datanode.data.dir</name>
    <value>/opt/hdfs/data</value>
  </property>
  <property>
   <name>dfs.permissions.superusergroup</name>
   <value>hadoop</value>
  </property>
  <property>
    <name>dfs.safemode.extension</name>
    <value>0</value>
  </property>
  <property>
     <name>dfs.safemode.min.datanodes</name>
     <value>1</value>
  </property>
  <property>
    <name>ignore.secure.ports.for.testing</name>
    <value>true</value>
  </property>
</configuration>
EOF

sudo update-alternatives --install /etc/hadoop/conf hadoop-conf /etc/hadoop/conf.gohdfs 99
sudo apt-get install -y --allow-unauthenticated hadoop-hdfs-namenode hadoop-hdfs-datanode

sudo mkdir -p /opt/hdfs/data /opt/hdfs/name
sudo chown -R hdfs:hdfs /opt/hdfs
sudo -u hdfs hdfs namenode -format -nonInteractive

sudo adduser travis hadoop

sudo service hadoop-hdfs-datanode restart
sudo service hadoop-hdfs-namenode restart

hdfs dfsadmin -safemode wait


# Hadoop still requires java8.
export JAVA_HOME=/usr/lib/jvm/java-8-openjdk-amd64

HADOOP_FS=${HADOOP_FS-"hadoop fs"}
$HADOOP_FS -mkdir -p "/_test"
$HADOOP_FS -chmod 777 "/_test"

$HADOOP_FS -put ./testdata/foo.txt "/_test/foo.txt"
$HADOOP_FS -Ddfs.block.size=1048576 -put ./testdata/mobydick.txt "/_test/mobydick.txt"
