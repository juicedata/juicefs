
import subprocess

try:
    __import__("MySQLdb")
except ImportError:
    subprocess.check_call(["pip", "install", "mysqlclient"])

import os
from sys import argv
import MySQLdb
from datetime import datetime
import argparse

# CREATE TABLE `benchmark` (
#   `name` varchar(128) DEFAULT NULL,
#   `result` float DEFAULT NULL,
#   `product_name` varchar(32) DEFAULT NULL,
#   `product_version` varchar(128) DEFAULT NULL,
#   `meta` varchar(32) DEFAULT NULL,
#   `storage` varchar(32) DEFAULT NULL,
#   `github_ref_name` varchar(64) DEFAULT NULL,
#   `github_run_id` varchar(1024) DEFAULT NULL,
#   `github_sha` varchar(1024) DEFAULT NULL,
#   `github_runner` varchar(255) DEFAULT NULL,
#   `created_date` datetime DEFAULT NULL
# )

def add_perf_record(name, result, product_version,  meta, storage, extra):
    result = float(result)
    passowrd = os.environ['MYSQL_PASSWORD']
    if not passowrd:
        print('<WARNING>: MYSQL_PASSWORD is empty')
        return 
    github_ref_name = os.environ.get('GITHUB_REF_NAME')
    print(f'github_ref_name is: {github_ref_name}')
    github_run_id = os.environ.get('GITHUB_RUN_ID')
    print(f'github_run_id is: {github_run_id}')
    github_sha = os.environ.get('GITHUB_SHA')
    print(f'github_sha is: {github_sha}')
    github_runner = os.environ.get('RUNNER_NAME')
    print(f'github_runner is: {github_runner}')
    created_date = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    product_name = 'juicefs'
    db = MySQLdb.connect(host="8.210.231.144", user="juicedata", passwd=passowrd, db="test_result")
    c = db.cursor()
    c.execute("insert into benchmark(name, result, product_name, product_version, meta, storage, github_ref_name, github_run_id, github_sha, github_runner, created_date, extra) \
        values(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)", (name, result, product_name, product_version, meta, storage, github_ref_name, github_run_id, github_sha, github_runner, created_date, extra))
    db.commit()
    c.close()
    db.close()

if __name__ == "__main__":
    args = argparse.ArgumentParser()
    args.add_argument("-n", "--name", required=True, help="the name of performance test")
    args.add_argument("-r", "--result", required=True, help="the result value of performance test")
    args.add_argument("-v", "--version", required=True, help="the version of juicefs")
    args.add_argument("-m", "--meta", required=True, help="meta for juicefs")
    args.add_argument("-s", "--storage", required=True, help="storage for juicefs")
    args.add_argument("-e", "--extra", required=False, help="extra info")
    
    args = vars(args.parse_args())
    
    add_perf_record(args['name'], args['result'], args['version'], args['meta'], args.get('storage', ''), args.get('extra', ''))


