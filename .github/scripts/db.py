
import os
from sys import argv
import MySQLdb
from datetime import datetime
import argparse

def add_perf_record(name, result, product_version,  meta, storage):
    passowrd = os.environ['MYSQL_PASSWORD']
    github_ref_name = os.environ['GITHUB_REF_NAME']
    print(f'github_ref_name is: {github_ref_name}')
    github_run_id = os.environ['GITHUB_RUN_ID']
    print(f'github_run_id is: {github_run_id}')
    github_sha = os.environ['GITHUB_SHA')
    print(f'github_sha is: {github_sha}')
    github_runner = os.environ['RUNNER_NAME')
    print(f'github_runner is: {github_runner}')
    created_date = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    product_name = 'juicefs'
    db = MySQLdb.connect(host="8.210.231.144", user="juicedata", passwd=passowrd, db="test_result")
    c = db.cursor()
    c.execute("insert into benchmark(name, result, product_name, product_version, meta, storage, github_ref_name, github_run_id, github_sha, github_runner, created_date) \
        values(%s, %s)", (name, result, product_name, product_version, meta, storage, github_ref_name, github_run_id, github_sha, github_runner, created_date))

if __name__ == "__main__":
    args = argparse.ArgumentParser()
    args.add_argument("-n", "--name", required=True, help="the name of performace test")
    args.add_argument("-r", "--result", required=True, help="the result value of performace test")
    args.add_argument("-v", "--version", required=True, help="the version of juicefs")
    args.add_argument("-m", "--meta", required=True, help="meta for juicefs")
    args.add_argument("-s", "--storage", required=True, help="storage for juicefs")
    add_perf_record(args['name'], args['result'], args['version'], args['meta'], args['storage'])


