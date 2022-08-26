import json
import os
import shutil
import subprocess
import sys
import time
import unittest
from xmlrpc.client import boolean
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine
from hypothesis import strategies as st
from packaging import version
from minio import Minio

class JuicefsMachine(RuleBasedStateMachine):
    MIN_CLIENT_VERSIONS = ['0.0.1', '0.0.17','1.0.0']
    MAX_CLIENT_VERSIONS = ['1.1.0', '1.2.0', '2.0.0']
    JFS_BIN = ['./juicefs-1.0.0-beta1', './juicefs-1.0.0-beta2', './juicefs-1.0.0-beta3', './juicefs-1.0.0-rc1', './juicefs-1.0.0-rc2','./juicefs-1.0.0-rc3','./juicefs']
    JFS_BIN = ['./juicefs-1.0.0-rc1',  './juicefs']
    META_URLS = ['redis://localhost/1']
    STORAGES = ['minio']
    # META_URL = 'badger://abc.db'
    MOUNT_POINT = '/tmp/sync-test/'
    VOLUME_NAME = 'test-volume'

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print('__init__')
        self.formatted = False
        self.mounted = False
        self.meta_url = None
        print('\nINIT----------------------------------------------------------------------------------------\n')

    def flush_meta(self, meta_url):
        print('start flush meta')
        if os.path.exists(JuicefsMachine.MOUNT_POINT):
            os.system('umount %s'%JuicefsMachine.MOUNT_POINT)
            print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            self.mounted = False
        if meta_url.startswith('sqlite3://'):
            path = meta_url[len('sqlite3://'):]
            if os.path.isfile(path):
                os.remove(path)
                print(f'remove meta file {path} succeed')
        elif meta_url.startswith('redis://'):
            os.system('redis-cli flushall')
            print(f'flush redis succeed')
        print('flush meta succeed')
    def clear_storage(self, storage, bucket):
        print('start clear storage')
        if storage == 'file':
            storage_dir = os.path.join(bucket, JuicefsMachine.VOLUME_NAME) 
            if os.path.exists(storage_dir):
                try:
                    shutil.rmtree(storage_dir)
                    print(f'remove cache dir {storage_dir} succeed')
                except OSError as e:
                    print("Error: %s : %s" % (storage_dir, e.strerror))
        elif storage == 'minio':
            from urllib.parse import urlparse
            url = urlparse(bucket)
            c = Minio(url.netloc, access_key='minioadmin', secret_key='minioadmin', secure=False)
            if c.bucket_exists(url.path[1:]):
                # c.remove_bucket(url.path[1:])
                os.system(f'mc alias set myminio http://{url.netloc} minioadmin minioadmin')
                os.system(f'mc rm --recursive --force  myminio/{url.path[1:]}')
        print('clear storage succeed')
    def clear_cache(self):
        os.system('sudo rm -rf /var/jfsCache')
        if sys.platform.startswith('linux') :
            os.system('sudo bash -c  "echo 3> /proc/sys/vm/drop_caches"')

    @rule(
        juicefs=st.sampled_from(JFS_BIN),
        capacity=st.integers(min_value=0, max_value=1024), 
        inodes=st.integers(min_value=1024*1024, max_value=1024*1024*1024),
        change_bucket=st.booleans(), 
        change_aksk=st.booleans(), 
        encrypt_secret = st.booleans(), 
        trash_days =  st.integers(min_value=0, max_value=10000),
        min_client_version = st.sampled_from(MIN_CLIENT_VERSIONS), 
        max_client_version = st.sampled_from(MAX_CLIENT_VERSIONS), 
        force = st.booleans(),
    )
    @precondition(lambda self: self.formatted)
    def config(self, juicefs, capacity, inodes, change_bucket, change_aksk, encrypt_secret, trash_days, min_client_version, max_client_version, force):
        print('start config')
        options = [juicefs, 'config', self.meta_url]
        options.extend(['--trash-days', str(trash_days)])
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        assert version.parse(min_client_version) <= version.parse(max_client_version)
        options.extend(['--min-client-version', min_client_version])
        options.extend(['--max-client-version', max_client_version])
        output = self.exec_check_output([juicefs, 'status', self.meta_url])
        storage = json.loads(output.decode('utf8').replace("'", '"'))['Setting']['Storage']

        if change_bucket:
            options.extend(['--bucket', os.path.expanduser('~/.juicefs/local2')])
        if change_aksk and storage == 'minio':
            os.system(f'mc alias set myminio http://localhost:9000 minioadmin minioadmin')
            output = subprocess.check_output('mc admin user list myminio'.split())
            if not output:
                os.system('mc admin user add myminio juicedata 12345678')
                os.system('mc admin policy set myminio consoleAdmin user=juicedata')
            options.extend(['--access-key', 'juicedata'])
            options.extend(['--secret-key', '12345678'])
        if encrypt_secret:
            options.append('--encrypt-secret')
        options.append('--force')
        self.exec_check_call(options)
        print('config succeed')

    @rule(
          juicefs=st.sampled_from(JFS_BIN),
          block_size=st.integers(min_value=1, max_value=4096*10), 
          capacity=st.integers(min_value=0, max_value=1024),
          inodes=st.integers(min_value=1024*1024, max_value=1024*1024*1024),
          compress=st.sampled_from(['lz4', 'zstd', 'none']),
          shards=st.integers(min_value=0, max_value=1),
          storage=st.sampled_from(STORAGES), 
          encrypt_rsa_key = st.booleans(), 
          encrypt_algo = st.sampled_from(['aes256gcm-rsa','chacha20-rsa']),
          trash_days=st.integers(min_value=0, max_value=10000), 
          hash_prefix=st.booleans(), 
          force = st.booleans(), 
          no_update = st.booleans(),
          meta_url=st.sampled_from(META_URLS),
          )
    def format(self, juicefs, block_size, capacity, inodes, compress, shards, storage, encrypt_rsa_key, encrypt_algo, trash_days, hash_prefix, force, no_update, meta_url):
        print('start format')
        options = [juicefs, 'format',  meta_url, JuicefsMachine.VOLUME_NAME]
        if not self.formatted:
            options.extend(['--block-size', str(block_size)])
            options.extend(['--compress', compress])
            options.extend(['--shards', str(shards)])
            options.extend(['--storage', storage])
            if hash_prefix:
                options.append('--hash-prefix')
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        options.extend(['--trash-days', str(trash_days)])
        
        if force:
            options.append('--force')
        if no_update:
            options.append('--no-update')
        if encrypt_rsa_key:
            if not os.path.exists('my-priv-key.pem'):
                subprocess.check_call('openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048')
            os.environ['JFS_RSA_PASSPHRASE'] = '12345678'
            options.extend(['--encrypt-rsa-key', 'my-priv-key.pem'])
            options.extend(['--encrypt-algo', encrypt_algo])
        
        if storage == 'minio':
            bucket = 'http://127.0.0.1:9000/test-bucket'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'minioadmin'])
            options.extend(['--secret-key', 'minioadmin'])
        elif storage == 'file':
            bucket = os.path.expanduser('~/.juicefs/local/')
            options.extend(['--bucket', bucket])
        else:
            assert False

        if not self.formatted:
            self.clear_storage(storage, bucket)
            self.flush_meta(meta_url)
        print(f'format options: {" ".join(options)}' )
        self.exec_check_call(options)
        self.meta_url = meta_url
        self.formatted = True
        print('format succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted )
    def status(self, juicefs):
        print('start status')
        output = self.exec_check_output([juicefs, 'status', self.meta_url])
        uuid = json.loads(output.decode('utf8').replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        if self.mounted:
            sessions = json.loads(output.decode('utf8').replace("'", '"'))['Sessions']
            assert len(sessions) != 0 
        print('status succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted )
    def mount(self, juicefs):
        print('start mount')
        self.exec_check_call([juicefs, 'mount', '-d',  self.meta_url, JuicefsMachine.MOUNT_POINT])
        time.sleep(1)
        self.mounted = True
        print('mount succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN), 
    force=st.booleans())
    @precondition(lambda self: self.mounted)
    def umount(self, juicefs, force):
        print('start umount')
        options = [juicefs, 'umount', JuicefsMachine.MOUNT_POINT]
        if force:
            options.append('--force')
        self.exec_check_call(options)
        self.mounted = False
        print('umount succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted and not self.mounted)
    def destroy(self, juicefs):
        print('start destroy')
        output = self.exec_check_output([juicefs, 'status', self.meta_url])
        uuid = json.loads(output.decode('utf8').replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        options = [juicefs, 'destroy', self.meta_url, uuid]
        options.append('--force')
        self.exec_check_call(options)
        self.formatted = False
        self.mounted = False
        print('destroy succeed')

    valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
    @rule(file_name=valid_file_name, data=st.binary() )
    @precondition(lambda self: self.mounted )
    def write_and_read(self, file_name, data):
        print('start write and read')
        path = JuicefsMachine.MOUNT_POINT+file_name
        with open(path, "wb") as f:
            f.write(data)
        with open(path, "rb") as f:
            result = f.read()
        assert str(result) == str(data)
        print('write and read succeed')

    @rule(juicefs = st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted )
    def dump(self, juicefs):
        print('start dump')
        self.exec_check_call([juicefs, 'dump', self.meta_url, 'dump.json'])
        print('dump succeed')

    @rule(juicefs = st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted and os.path.exists('dump.json'))
    def load(self, juicefs):
        print('start load')
        self.flush_meta(self.meta_url)
        self.exec_check_call([juicefs, 'load', self.meta_url, 'dump.json'])
        print('load succeed')
        options = [juicefs, 'config', self.meta_url]
        options.extend(['access-key', 'minioadmin', '--secret-key', 'minioadmin', '--encrypt-secret'])
        self.exec_check_call(options)
        os.remove('dump.json')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted)
    def fsck(self, juicefs):
        print('start fsck')
        self.exec_check_call([juicefs, 'fsck', self.meta_url])
        print('fsck succeed')

    def exec_check_call(self, options):
        options.append('--debug')
        print('exec:'+' '.join(options))
        result = subprocess.check_call(options)
        return result

    def exec_check_output(self, options):
        options.append('--debug')
        print('exec:'+' '.join(options))
        output = subprocess.check_output(options)
        print('succeed')
        return output

    @rule(juicefs=st.sampled_from(JFS_BIN),
     block_size=st.integers(min_value=1, max_value=32),
     big_file_size=st.integers(min_value=100, max_value=200),
     small_file_size=st.integers(min_value=1, max_value=256),
     small_file_count=st.integers(min_value=100, max_value=256), 
     threads=st.integers(min_value=1, max_value=100))
    @precondition(lambda self: self.mounted and False)
    def bench(self, juicefs, block_size, big_file_size, small_file_size, small_file_count, threads):
        print('start bench')
        os.system(f'df | grep {JuicefsMachine.MOUNT_POINT}')
        options = [juicefs, 'bench', JuicefsMachine.MOUNT_POINT]
        options.extend(['--block-size', str(block_size)])
        options.extend(['--big-file-size', str(big_file_size)])
        options.extend(['--small-file-size', str(small_file_size)])
        options.extend(['--small-file-count', str(small_file_count)])
        options.extend(['--threads', str(threads)])
        output = self.exec_check_output(options)
        summary = output.decode('utf8').split('\n')[2]
        expected = f'BlockSize: {block_size} MiB, BigFileSize: {big_file_size} MiB, SmallFileSize: {small_file_size} KiB, SmallFileCount: {small_file_count}, NumThreads: {threads}'
        assert summary == expected
        print('bench succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN),
        threads=st.integers(min_value=1, max_value=100), 
        background = st.booleans(), 
        from_file = st.booleans(),
        directory = st.booleans() )
    @precondition(lambda self: self.mounted)
    def warmup(self, juicefs, threads, background, from_file, directory):
        print('start warmup')
        self.clear_cache()
        options = [juicefs, 'warmup']
        options.extend(['--threads', threads])
        if background:
            options.append('--background')
        if from_file:
            file_list = [JuicefsMachine.MOUNT_POINT+'file1', JuicefsMachine.MOUNT_POINT+'file2', JuicefsMachine.MOUNT_POINT+'file3']
            for filepath in file_list:
                os.system(f'dd if=/dev/urandom of={filepath} iflag=fullblock,count_bytes bs=1M count=1G')
            with open('file.list', 'w') as f:
                f.writelines(file_list)
            options.extend(['--file', 'file.list'])
        else:
            if directory:
                options.append(JuicefsMachine.MOUNT_POINT)
            else:
                os.system(f'dd if=/dev/urandom of={JuicefsMachine.MOUNT_POINT}/bigfile iflag=fullblock,count_bytes bs=1M count=1G')
                options.append(JuicefsMachine.MOUNT_POINT+'/bigfile')
                
        output = self.exec_check_output(options)
        print(output)
        print('warmup succeed')
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up count: ')
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up bytes: ')

    @rule(
        juicefs = st.sampled_from(JFS_BIN), 
        compact=st.booleans(), 
        delete=st.booleans(),
        threads=st.integers(min_value=0, max_value=100) )
    @precondition(lambda self: self.formatted)
    def gc(self, juicefs, compact, delete, threads):
        print('start gc')
        options = [juicefs, 'gc', self.meta_url]
        if compact:
            options.append('--compact')
        if delete:
            options.append('--delete')
        options.extend(['--threads', str(threads)])
        output = self.exec_check_output(options)
        print(output)
        print('gc succeed')

    @rule(juicefs = st.sampled_from(JFS_BIN),
        port=st.integers(min_value=9001, max_value=10000))
    @precondition(lambda self: self.formatted)
    def gateway(self, juicefs, port):
        print('start gateway')
        if self.is_port_in_use(port):
            return
        os.environ['MINIO_ROOT_USER'] = 'admin'
        os.environ['MINIO_ROOT_PASSWORD'] = '12345678'
        options = [juicefs, 'gateway', self.meta_url, f'localhost:{port}']
        proc=subprocess.Popen(options)
        time.sleep(2.0)
        subprocess.Popen.kill(proc)
        print('gateway succeed')
    @rule(juicefs = st.sampled_from(JFS_BIN), 
        port=st.integers(min_value=10001, max_value=11000)) 
    @precondition(lambda self: self.formatted )
    def webdav(self, juicefs, port):
        print('start webdav')
        if self.is_port_in_use(port):
            return 
        options = [juicefs, 'webdav', self.meta_url, f'localhost:{port}']
        proc = subprocess.Popen(options)
        time.sleep(2.0)
        subprocess.Popen.kill(proc)
        print('webdav succeed')
    def is_port_in_use(self, port: int) -> bool:
        import socket
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            return s.connect_ex(('localhost', port)) == 0

TestJuiceFS = JuicefsMachine.TestCase

if __name__ == "__main__":
    unittest.main()