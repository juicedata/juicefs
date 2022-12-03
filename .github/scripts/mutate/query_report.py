
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
        passed = int(row[2])
        failed = int(row[3])
        if passed+failed != 0:
            score = row[2]/(row[2]+row[3])
        else:
            score = 0
        print(f'{row[0]}: score:{score:.2f} failed:{row[3]}, passed:{row[2]}, compile error:{row[4]}, out of coverage:{row[5]}, skip by comment:{row[6]}, others:{row[7]}')
        print(f'Job detail: {row[1]}\n')
    db.close()

if __name__ == "__main__":
    repo = os.environ.get('GITHUB_REPOSITORY')
    run_id = os.environ.get('GITHUB_RUN_ID')
    # repo = 'juicedata/juicefs'
    # run_id = '3608212346'
    print(f'repo is {repo}, run_id is {run_id}', file=sys.stderr)
    query_report(repo, run_id)
