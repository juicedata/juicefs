#!/usr/bin/env bash

set -e
set -o pipefail

HADOOP_VERSION="2.7.7"
SPARK_VERSION="2.4.0"
EXAMPLES_JAR="spark-examples_2.11-2.4.0.jar"

SPARK_DIST="spark-${SPARK_VERSION}-bin-without-hadoop"
SPARK_HOME="/opt/${SPARK_DIST}"
HADOOP_DIST="hadoop-${HADOOP_VERSION}"
HADOOP_HOME="/opt/${HADOOP_DIST}"

curl -o "${HADOOP_HOME}.tar.gz" "https://archive.apache.org/dist/hadoop/common/hadoop-${HADOOP_VERSION}/${HADOOP_DIST}.tar.gz"
tar -xf "${HADOOP_HOME}.tar.gz" -C /opt

export _JAVA_OPTIONS="-Djava.library.path=$(pwd)/../mount/libjfs"
export HADOOP_CLASSPATH="$(pwd)/target/juicefs-hadoop-0.1-SNAPSHOT.jar"
"${HADOOP_HOME}/bin/hadoop" --config "$(pwd)/conf" jar "${HADOOP_HOME}/share/hadoop/mapreduce/hadoop-mapreduce-examples-${HADOOP_VERSION}.jar" grep hello output 'dfs[a-z.]+'

curl -o "${SPARK_HOME}.tgz" "https://archive.apache.org/dist/spark/spark-${SPARK_VERSION}/${SPARK_DIST}.tgz"
tar -xf "${SPARK_HOME}.tgz" -C /opt

echo "export SPARK_DIST_CLASSPATH=$(${HADOOP_HOME}/bin/hadoop classpath)" > "${SPARK_HOME}/conf/spark-env.sh"
echo "export HADOOP_CONF_DIR=$(pwd)/conf" >> "${SPARK_HOME}/conf/spark-env.sh"
cp "${SPARK_HOME}/examples/jars/${EXAMPLES_JAR}" /jfs/

"${SPARK_HOME}/bin/spark-submit" --class org.apache.spark.examples.JavaWordCount --master "local" "jfs:///${EXAMPLES_JAR}" "jfs:///hello"
