import json
import os
from posixpath import expanduser
import shutil
import subprocess
import sys
import time
from minio import Minio


def flush_meta(meta_url):
    print('start flush meta')
    if meta_url.startswith('sqlite3://'):
        path = meta_url[len('sqlite3://'):]
        if os.path.isfile(path):
            os.remove(path)
            print(f'remove meta file {path} succeed')
    elif meta_url.startswith('redis://'):
        run_cmd('redis-cli flushall')
        print(f'flush redis succeed')
    print('flush meta succeed')

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
        return False
    with open(f'{filesystem}/.config') as f:
        config = json.load(f)
        return config['Meta']['UploadDelay']/1000000000
    
def run_jfs_cmd( options):
    options.append('--debug')
    print('run_jfs_cmd:'+' '.join(options))
    with open('command.log', 'a') as f:
        f.write(' '.join(options))
        f.write('\n')
    try:
        output = subprocess.run(options, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        print(f'<FATAL>: subprocess run error: {e.output.decode()}')
        raise Exception('subprocess run error')
    print(output.stdout.decode())
    print('run_jfs_cmd succeed')
    return output.stdout.decode()

def run_cmd(command):
    print('run_cmd:'+command)
    if '|' in command:
        return os.system(command)
    try:
        output = subprocess.run(command.split(), check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        print(f'FATAL: subprocess run error: {e.output.decode()}')
        return e.returncode
    if output.stdout:
        print(output.stdout.decode())
    print('run_cmd succeed')
    return output.returncode