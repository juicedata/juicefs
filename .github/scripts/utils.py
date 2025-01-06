import subprocess
try:
    __import__("minio")
except ImportError:
    subprocess.check_call(["pip", "install", "minio"])
import json
import os
from posixpath import expanduser
import shutil
import subprocess
import sys
import time
from minio import Minio

def flush_meta(meta_url:str):
    print(f'start flush meta: {meta_url}')
    if meta_url.startswith('sqlite3://'):
        path = meta_url[len('sqlite3://'):]
        if os.path.isfile(path):
            os.remove(path)
            print(f'remove meta file {path} succeed')
    elif meta_url.startswith('badger://'):
        path = meta_url[len('badger://'):]
        if os.path.isdir(path):
            shutil.rmtree(path)
            print(f'remove badger dir {path} succeed')
    elif meta_url.startswith('redis://') or meta_url.startswith('tikv://'):
        default_port = {"redis": 6379, "tikv": 2379}
        protocol = meta_url.split("://")[0]
        host_port= meta_url.split("://")[1].split('/')[0]
        if ':' in host_port:
            host = host_port.split(':')[0]
            port = host_port.split(':')[1]
        else:
            host = host_port
            port = default_port[protocol]
        db = meta_url.split("://")[1].split('/')[1]
        assert db
        print(f'flushing {protocol}://{host}:{port}/{db}')
        if protocol == 'redis':
            run_cmd(f'redis-cli -h {host} -p {port} -n {db} flushdb')
        elif protocol == 'tikv':
            # TODO: should only flush the specified db
            run_cmd(f'echo "delall --yes" |tcli -pd {host}:{port}')
        else:
            raise Exception(f'{protocol} not supported')
        print(f'flush {protocol}://{host}:{port}/{db} succeed')
    elif meta_url.startswith('mysql://'):
        create_mysql_db(meta_url)
    elif meta_url.startswith('postgres://'): 
        create_postgres_db(meta_url)
    elif meta_url.startswith('fdb://'):
        # fdb:///home/runner/fdb.cluster?prefix=jfs2
        prefix = meta_url.split('?prefix=')[1] if '?prefix=' in meta_url else ""
        cluster_file = meta_url.split('fdb://')[1].split('?')[0]
        print(f'flushing fdb: cluster_file: {cluster_file}, prefix: {prefix}')
        run_cmd(f'echo "writemode on; clearrange {prefix} {prefix}\\xff" | fdbcli -C {cluster_file}')
        print(f'flush fdb succeed')
    else:
        raise Exception(f'{meta_url} not supported')
    print('flush meta succeed')

def create_mysql_db(meta_url):
    db_name = meta_url[8:].split('@')[1].split('/')[1]
    user = meta_url[8:].split('@')[0].split(':')[0]
    password = meta_url[8:].split('@')[0].split(':')[1]
    if password: 
        password = f'-p{password}'
    host_port= meta_url[8:].split('@')[1].split('/')[0].replace('(', '').replace(')', '')
    if ':' in host_port:
        host = host_port.split(':')[0]
        port = host_port.split(':')[1]
    else:
        host = host_port
        port = '3306'
    run_cmd(f'mysql -u{user} {password} -h {host} -P {port} -e "drop database if exists {db_name}; create database {db_name};"')

def create_postgres_db(meta_url):
    os.environ['PGPASSWORD'] = 'postgres'
    db_name = meta_url[8:].split('@')[1].split('/')[1]
    if '?' in db_name:
        db_name = db_name.split('?')[0]
    run_cmd(f'printf "\set AUTOCOMMIT on\ndrop database if exists {db_name}; create database {db_name}; " |  psql -U postgres -h localhost')

def clear_storage(storage, bucket, volume):
    print('start clear storage')
    if storage == 'file':
        storage_dir = os.path.join(bucket, volume) 
        if os.path.exists(storage_dir):
            try:
                shutil.rmtree(storage_dir)
                print(f'remove cache dir {storage_dir} succeed')
            except OSError as e:
                print("Error: %s : %s" % (storage_dir, e.strerror))
    elif storage == 'minio':
        from urllib.parse import urlparse
        url = urlparse(bucket)
        c = Minio('localhost:9000', access_key='minioadmin', secret_key='minioadmin', secure=False)
        bucket_name = url.path[1:]
        while c.bucket_exists(bucket_name) and list(c.list_objects(bucket_name)) :
            print(f'try to remove bucket {url.path[1:]}')
            result = run_cmd(f'mc rm --recursive --force  myminio/{bucket_name}')
            if result != 0:
                raise Exception(f'remove {bucket_name} failed')
            if c.bucket_exists(url.path[1:]) and list(c.list_objects(bucket_name)):
                time.sleep(1)
        print(f'remove bucket {bucket_name} succeed')
        if c.bucket_exists(bucket_name):
            assert not list(c.list_objects(bucket_name))
    elif storage == 'mysql':
        db_name = bucket.split('/')[-1]
        run_cmd(f'mysql -uroot -proot -h localhost -P 3306 -e "drop database if exists {db_name};create database {db_name};"')
    elif storage == 'postgres':
        db_name = bucket.split('/')[1]
        if '?' in db_name:
            db_name = db_name.split('?')[0]
        run_cmd(f'printf "\set AUTOCOMMIT on\ndrop database if exists {db_name}; create database {db_name}; " |  psql -U postgres -h localhost')
    print('clear storage succeed')


def clear_cache():
    run_cmd('sudo rm -rf /var/jfsCache')
    run_cmd(f'sudo rm -rf {os.path.expanduser("~/.juicefs/cache")}')
    if sys.platform.startswith('linux') :
        os.system('sudo bash -c  "echo 3> /proc/sys/vm/drop_caches"')

def is_readonly(filesystem):
    if not os.path.exists(f'{filesystem}/.config'):
        return False
    with open(f'{filesystem}/.config') as f:
        config = json.load(f)
        return config['Meta']['ReadOnly']

def get_upload_delay_seconds(filesystem):
    if not os.path.exists(f'{filesystem}/.config'):
        return 0
    with open(f'{filesystem}/.config') as f:
        config = json.load(f)
        return config['Chunk']['UploadDelay']/1000000000
    
def get_stage_blocks(filesystem):
    try:
        ps = subprocess.Popen(('cat', f'{filesystem}/.stats'), stdout=subprocess.PIPE)
        output = subprocess.check_output(('grep', 'juicefs_staging_blocks'), stdin=ps.stdout)
        ps.wait()
        return int(output.decode().split()[1])
    except subprocess.CalledProcessError:
        print('get_stage_blocks: no juicefs_staging_blocks find')
        return 0

def write_data(filesystem, path, data):
    with open(path, "wb") as f:
        f.write(data)
    retry = get_upload_delay_seconds(filesystem) + 10
    while get_stage_blocks(filesystem) != 0 and retry > 0:
        print('sleep for stage')
        retry = retry - 1
        time.sleep(1)
    # assert get_stage_blocks(filesystem) == 0

def write_block(filesystem, filepath, bs, count):
    run_cmd(f'dd if=/dev/urandom of={filepath} bs={bs} count={count}')
    retry = get_upload_delay_seconds(filesystem) + 10
    while get_stage_blocks(filesystem) != 0 and retry > 0:
        print('sleep for stage')
        retry = retry - 1
        time.sleep(1)
    # assert get_stage_blocks(filesystem) == 0

def mdtest(filesystem, meta_url):
    juicefs_new = './'+os.environ.get('NEW_JFS_BIN')
    cwd = os.getcwd()
    if not os.path.exists(f'{filesystem}/{juicefs_new}'):
        run_cmd(f'ln -s {cwd}/{juicefs_new} {filesystem}/{juicefs_new}')
    os.chdir(filesystem)
    run_jfs_cmd(f'{juicefs_new} mdtest {meta_url} mdtest --dirs 5 --depth 2 --files 5 --threads 5 --write 8192'.split())
    os.chdir(cwd)
    time.sleep(get_upload_delay_seconds(filesystem)+1)
    retry = 5
    while get_stage_blocks(filesystem) != 0 and retry > 0:
        print('sleep for stage')
        retry = retry - 1
        time.sleep(1)
    assert os.path.exists(filesystem+'mdtest')

def run_jfs_cmd( options):
    # options.append('--debug')
    print('run_jfs_cmd:'+' '.join(options))
    with open(os.path.expanduser('~/command.log'), 'a') as f:
        f.write(' '.join(options).replace('/home/runner', '~'))
        f.write('\n')
    try:
        output = subprocess.run(options, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        print(f'<FATAL>: subprocess run error, return code: {e.returncode} , error message: {e.output.decode()}')
        raise Exception('subprocess run error')
    print(f'run_jfs_cmd return code: {output.returncode}, output: {output.stdout.decode()}')
    print('run_jfs_cmd succeed')
    return output.stdout.decode()

def run_cmd(command):
    print('run_cmd:'+command)
    if '|' in command or '"' in command:
        return os.system(command)
    try:
        output = subprocess.run(command.split(), check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        print(f'<FATAL>: subprocess run error, return code: {e.returncode} , error message: {e.output.decode()}')
        return e.returncode
    if output.stdout:
        print(output.stdout.decode())
    print('run_cmd succeed')
    return output.returncode

def is_port_in_use(port: int) -> bool:
    import socket
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        return s.connect_ex(('localhost', port)) == 0

def get_storage(juicefs, meta_url):
    output = subprocess.run([juicefs, 'status', meta_url], check=True, stdout=subprocess.PIPE).stdout.decode()
    if 'get timestamp too slow' in output: 
        # remove the first line caust it is tikv log message
        output = '\n'.join(output.split('\n')[1:])
    print(f'status output: {output}')
    storage = json.loads(output.replace("'", '"'))['Setting']['Storage']
    return storage

if __name__ == "__main__":
    run_jfs_cmd(['./juicefs-1.1.0-dev', 'rmr', '/tmp/sync-test/file_to_rmr', '--debug'])