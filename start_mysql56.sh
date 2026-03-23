#!/bin/bash
# run-mysql56.sh - 启动MySQL 5.6容器用于测试

docker run -d --name juicefs-mysql \
  -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=root123 \
  -e MYSQL_USER=test \
  -e MYSQL_PASSWORD=test123 \
  -e MYSQL_DATABASE=testdb \
  -v mysql-data:/var/lib/mysql \
  mysql:5.6
