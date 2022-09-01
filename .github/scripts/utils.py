import os
import shutil
import sys
from minio import Minio

def flush_meta(meta_url):
    print('start flush meta')
    if meta_url.startswith('sqlite3://'):
        path = meta_url[len('sqlite3://'):]
        if os.path.isfile(path):
            os.remove(path)
            print(f'remove meta file {path} succeed')
    elif meta_url.startswith('redis://'):
        os.system('redis-cli flushall')
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
        if c.bucket_exists(url.path[1:]):
            result = os.system(f'mc rm --recursive --force  myminio/{url.path[1:]}')
            if result != 0:
                raise Exception(f'remove {url.path[1:]} failed')
            print(f'remove {url.path[1:]} succeed')
            # assert not c.bucket_exists(url.path[1:])
    print('clear storage succeed')
    
def clear_cache(self):
    os.system('sudo rm -rf /var/jfsCache')
    if sys.platform.startswith('linux') :
        os.system('sudo bash -c  "echo 3> /proc/sys/vm/drop_caches"')