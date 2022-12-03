
import os
import MySQLdb

def query_report(repo, run_id):
    passowrd = os.environ['MYSQL_PASSWORD']
    db = MySQLdb.connect(host="8.210.231.144", user="juicedata", passwd=passowrd, db="mutate")
    db.query(f"""SELECT job_name, github_job_url, passed, failed, compile_error, out_of_coverage, skip_by_comment, others FROM report 
        WHERE github_repo="{repo}" AND github_run_id={run_id}""")
    r=db.store_result()
    for i in range(r.num_rows()):
        row = r.fetch_row()
        print(row)
    db.close()

if __name__ == "__main__":
    repo = os.environ.get('GITHUB_REPOSITORY')
    run_id = os.environ.get('GITHUB_RUN_ID')
    if os.environ.get('REPO'): 
        repo = os.environ.get('REPO')
    if os.environ.get('RUN_ID'): 
        run_id = os.environ.get('RUN_ID')
    query_report(repo, run_id)
