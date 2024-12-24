#!/bin/bash

# 参数检查
if [ "$#" -ne 3 ]; then
  echo "Usage: $0 <coverage_file> <upload_path> <token>"
  exit 1
fi

COVERAGE_FILE=$1
UPLOAD_PATH=$2
TOKEN=$3
attempt=1
max_attempts=3

while [ $attempt -le $max_attempts ]; do
  response=$(curl -w '%{http_code}' -s -o /dev/null --form "file=@${COVERAGE_FILE}" "https://juicefs.com/upload-file-u80sdvuke/${UPLOAD_PATH}?token=${TOKEN}")
  if [ "$response" -eq 200 ]; then
    echo "Coverage Report: https://i.juicefs.io/ci-coverage/${UPLOAD_PATH}"
    break
  else
    echo "Upload attempt $attempt failed with status code $response. Retrying..."
    attempt=$((attempt + 1))
    sleep 5  # 等待5秒钟后重试
  fi
done

if [ "$response" -ne 200 ]; then
  echo "Upload failed after $max_attempts attempts with status code $response"
  exit 1
fi