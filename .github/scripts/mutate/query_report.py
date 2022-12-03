
import os
import sys
import MySQLdb

def query_report(repo, run_id):
    passowrd = os.environ['MYSQL_PASSWORD']
    db = MySQLdb.connect(host="8.210.231.144", user="juicedata", passwd=passowrd, db="mutate")
    db.query(f"""SELECT job_name, github_job_url, passed, failed, compile_error, out_of_coverage, skip_by_comment, others FROM report 
        WHERE github_repo="{repo}" AND github_run_id={run_id}""")
    r=db.store_result()
    for i in range(r.num_rows()):
        row = r.fetch_row()[0]
        print(f'job name: {row[0]}, job url: {row[1]}, passed {row[2]}, failed {row[3]}')
    db.close()

if __name__ == "__main__":
    repo = os.environ.get('GITHUB_REPOSITORY')
    run_id = os.environ.get('GITHUB_RUN_ID')
    repo = 'juicedata/juicefs'
    run_id = '3608018336'
    print(f'repo is {repo}, run_id is {run_id}', file=sys.stderr)
    query_report(repo, run_id)
