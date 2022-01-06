#!/bin/bash
set -e
sudo apt-get update
sudo apt-get install openjdk-8-jdk -y
HADOOP_VERSION="2.10.1"
wget https://dlcdn.apache.org/hadoop/core/stable2/hadoop-2.10.1.tar.gz
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

KERBEROS=${KERBEROS-"false"}
AES=${AES-"false"}
if [ "$DATA_TRANSFER_PROTECTION" = "privacy" ]; then
  KERBEROS="true"
  ENCRYPT_DATA_TRANSFER="true"
  ENCRYPT_DATA_TRANSFER_ALG="rc4"
  if [ "$AES" = "true" ]; then
      ENCRYPT_DATA_TRANSFER_CIPHER="AES/CTR/NoPadding"
  fi
else
  ENCRYPT_DATA_TRANSFER="false"
fi


sudo apt-get update

CONF_AUTHENTICATION="simple"
if [ $KERBEROS = "true" ]; then
  CONF_AUTHENTICATION="kerberos"

  HOSTNAME=$(hostname)

  KERBEROS_REALM="EXAMPLE.COM"
  KERBEROS_PRINCIPLE="administrator"
  KERBEROS_PASSWORD="password1234"

  sudo tee /etc/krb5.conf << EOF
[libdefaults]
    default_realm = $KERBEROS_REALM
    dns_lookup_realm = false
    dns_lookup_kdc = false
[realms]
    $KERBEROS_REALM = {
        kdc = localhost
        admin_server = localhost
    }
[logging]
    default = FILE:/var/log/krb5libs.log
    kdc = FILE:/var/log/krb5kdc.log
    admin_server = FILE:/var/log/kadmind.log
[domain_realm]
    .localhost = $KERBEROS_REALM
    localhost = $KERBEROS_REALM
EOF

  sudo mkdir /etc/krb5kdc
  sudo printf '*/*@%s\t*' "$KERBEROS_REALM" | sudo tee /etc/krb5kdc/kadm5.acl

  sudo apt-get install -y krb5-user krb5-kdc krb5-admin-server

  printf "$KERBEROS_PASSWORD\n$KERBEROS_PASSWORD" | sudo kdb5_util -r "$KERBEROS_REALM" create -s
  for p in nn dn travis gohdfs1 gohdfs2; do
    sudo kadmin.local -q "addprinc -randkey $p/$HOSTNAME@$KERBEROS_REALM"
    sudo kadmin.local -q "addprinc -randkey $p/localhost@$KERBEROS_REALM"
    sudo kadmin.local -q "xst -k /tmp/$p.keytab $p/$HOSTNAME@$KERBEROS_REALM"
    sudo kadmin.local -q "xst -k /tmp/$p.keytab $p/localhost@$KERBEROS_REALM"
    sudo chmod +rx /tmp/$p.keytab
  done

  sudo service krb5-kdc restart
  sudo service krb5-admin-server restart

  kinit -kt /tmp/travis.keytab "travis/localhost@$KERBEROS_REALM"

  # The go tests need ccache files for these principles in a specific place.
  for p in travis gohdfs1 gohdfs2; do
    kinit -kt "/tmp/$p.keytab" -c "/tmp/krb5cc_gohdfs_$p" "$p/localhost@$KERBEROS_REALM"
  done
fi

sudo tee ~/app/hadoop-${HADOOP_VERSION}/etc/hadoop/core-site.xml <<EOF
<configuration>
  <property>
    <name>fs.defaultFS</name>
    <value>hdfs://localhost:9100</value>
  </property>
  <property>
    <name>hadoop.security.authentication</name>
    <value>$CONF_AUTHENTICATION</value>
  </property>
  <property>
    <name>hadoop.security.authorization</name>
    <value>$KERBEROS</value>
  </property>
  <property>
    <name>dfs.namenode.keytab.file</name>
    <value>/tmp/nn.keytab</value>
  </property>
  <property>
    <name>dfs.namenode.kerberos.principal</name>
    <value>nn/localhost@$KERBEROS_REALM</value>
  </property>
  <property>
    <name>dfs.web.authentication.kerberos.principal</name>
    <value>nn/localhost@$KERBEROS_REALM</value>
  </property>
  <property>
    <name>dfs.datanode.keytab.file</name>
    <value>/tmp/dn.keytab</value>
  </property>
  <property>
    <name>dfs.datanode.kerberos.principal</name>
    <value>dn/localhost@$KERBEROS_REALM</value>
  </property>
  <property>
    <name>hadoop.rpc.protection</name>
    <value>$RPC_PROTECTION</value>
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
    <name>dfs.namenode.name.dir</name>
    <value>${HOME}/apps/tmp/name</value>
  </property>
  <property>
    <name>dfs.datanode.data.dir</name>
    <value>${HOME}/apps/tmp//data</value>
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
    <name>dfs.block.access.token.enable</name>
    <value>$KERBEROS</value>
  </property>
  <property>
    <name>ignore.secure.ports.for.testing</name>
    <value>true</value>
  </property>
  <property>
    <name>dfs.data.transfer.protection</name>
    <value>$DATA_TRANSFER_PROTECTION</value>
  </property>
  <property>
    <name>dfs.encrypt.data.transfer</name>
    <value>$ENCRYPT_DATA_TRANSFER</value>
  </property>
  <property>
    <name>dfs.encrypt.data.transfer.algorithm</name>
    <value>$ENCRYPT_DATA_TRANSFER_ALG</value>
  </property>
  <property>
    <name>dfs.encrypt.data.transfer.cipher.suites</name>
    <value>$ENCRYPT_DATA_TRANSFER_CIPHER</value>
  </property>
  <property>
      <name>dfs.replication</name>
      <value>1</value>
    </property>
</configuration>
EOF

sudo addgroup hadoop
sudo useradd hdfs
sudo mkdir -p /opt/hdfs/data /opt/hdfs/name
sudo chown -R hdfs:hdfs /opt/hdfs
sudo adduser travis hadoop

cd ~/app/hadoop-${HADOOP_VERSION}/bin
./hdfs namenode -format -nonInteractive

cd ~/app/hadoop-${HADOOP_VERSION}/sbin
./start-dfs.sh

jps

cd ~/app/hadoop-${HADOOP_VERSION}/bin
./hdfs dfs -put ~/app/hadoop-${HADOOP_VERSION}/etc/hadoop/hdfs-site.xml  /
./hdfs dfs -ls /

echo "hdfs started successfully"
