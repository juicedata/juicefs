
import json
import os
from sys import argv
import MySQLdb
from datetime import datetime
import argparse
# CREATE DATABASE mutate
# CREATE TABLE `report` (
#   `github_repo` varchar(128) DEFAULT NULL,
#   `github_ref_name` varchar(64) DEFAULT NULL,
#   `github_sha` varchar(128) DEFAULT NULL,
#   `github_run_id` varchar(64) DEFAULT NULL,
#   `github_job_url` varchar(1024) DEFAULT NULL,
#   `created_date` datetime DEFAULT NULL,
#   `job_name` varchar(64) DEFAULT NULL,
#   `passed` int,
#   `failed` int,
#   `compile_error` int, 
#   `out_of_coverage` int, 
#   `skip_by_comment` int,
#   `others` int
# )

def save_report(job_name, report):
    passowrd = os.environ['MYSQL_PASSWORD']
    github_repo = os.environ.get('GITHUB_REPOSITORY')
    print(f'github_repo is: {github_repo}')
    github_ref_name = os.environ.get('GITHUB_REF_NAME')
    print(f'github_ref_name is: {github_ref_name}')
    github_sha = os.environ.get('GITHUB_SHA')
    print(f'github_sha is: {github_sha}')
    github_run_id = os.environ.get('GITHUB_RUN_ID')
    print(f'github_run_id is: {github_run_id}')
    github_job_url = os.environ.get('JOB_URL')
    print(f'github_job_url is: {github_job_url}')
    created_date = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    db = MySQLdb.connect(host="8.210.231.144", user="juicedata", passwd=passowrd, db="mutate")
    c = db.cursor()
    c.execute(f"insert into report(github_repo, github_ref_name,  github_sha, github_run_id, github_job_url, created_date, job_name, passed, failed, compile_error, out_of_coverage, skip_by_comment, others) \
        values(%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)", (github_repo, github_ref_name, github_sha, github_run_id, github_job_url, created_date, job_name, report['passed'], report['failed'], report['compile_error'], report['out_of_coverage'], report['skip_by_comment'], report['others']))
    db.commit()
    c.close()
    db.close()
    print(f'save report for {job_name} succeed')

if __name__ == "__main__":
    job_name = os.environ.get('JOB_NAME')
    stat_result_file = os.environ.get('STAT_RESULT_FILE')
    print(f'save report for {job_name}, stat result file is {stat_result_file}')
    with open(stat_result_file) as f:
        report = json.load(f)
        save_report(job_name, report)

