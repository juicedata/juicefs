import json
import os
import shutil
import subprocess
import sys
import time
import unittest
from xmlrpc.client import boolean
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine
from hypothesis import assume, strategies as st
from packaging import version
from minio import Minio
from utils import flush_meta, clear_storage, clear_cache, run_jfs_cmd

class JuicefsMachine(RuleBasedStateMachine):
    MIN_CLIENT_VERSIONS = ['0.0.1', '0.0.17','1.0.0-beta1', '1.0.0-rc1']
    MAX_CLIENT_VERSIONS = ['1.1.0', '1.2.0', '2.0.0']
    # JFS_BIN = ['./juicefs-1.0.0-beta1', './juicefs-1.0.0-beta2', './juicefs-1.0.0-beta3', './juicefs-1.0.0-rc1', './juicefs-1.0.0-rc2','./juicefs-1.0.0-rc3','./juicefs']
    # juicefs_version = subprocess.check_output(['./juicefs', 'version']).decode().split()[-1].split('+')[0]
    # os.environ['NEW_JFS_BIN'] = f'./juicefs-{juicefs_version}'
    JFS_BINS = ['./'+os.environ.get('OLD_JFS_BIN'), './'+os.environ.get('NEW_JFS_BIN')]
    # JFS_BINS = ['./juicefs-1.0.0-rc2',  './juicefs-1.1.0-dev']
    META_URLS = [os.environ.get('META_URL')]
    STORAGES = [os.environ.get('STORAGE')]
    # META_URL = 'badger://abc.db'
    MOUNT_POINT = '/tmp/sync-test/'
    VOLUME_NAME = 'test-volume'

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print('\n__init__')
        self.formatted = False
        self.mounted = False
        self.meta_url = None
        self.formatted_by = ''
        os.system(f'mc alias set myminio http://localhost:9000 minioadmin minioadmin')
        if os.path.isfile('dump.json'):
            os.remove('dump.json')

    @rule(
        juicefs=st.sampled_from(JFS_BINS),
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
        assume (self.is_supported_version(juicefs))
        print('start config')
        options = [juicefs, 'config', self.meta_url]
        options.extend(['--trash-days', str(trash_days)])
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        assert version.parse(min_client_version) <= version.parse(max_client_version)
        options.extend(['--min-client-version', min_client_version])
        options.extend(['--max-client-version', max_client_version])
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        storage = json.loads(output.decode().replace("'", '"'))['Setting']['Storage']
        
        if change_bucket:
            if storage == 'file':
                options.extend(['--bucket', os.path.expanduser('~/.juicefs/local2')])
            else: 
                c = Minio('localhost:9000', access_key='minioadmin', secret_key='minioadmin', secure=False)
                if not c.bucket_exists('test-bucket2'):
                    os.system('mc mb myminio/test-bucket2')
                    # assert c.bucket_exists('test-bucket2')
                options.extend(['--bucket', 'http://localhost:9000/test-bucket2'])
        if change_aksk and storage == 'minio':
            output = subprocess.check_output('mc admin user list myminio'.split())
            if not output:
                os.system('mc admin user add myminio juicedata 12345678')
                os.system('mc admin policy set myminio consoleAdmin user=juicedata')
            options.extend(['--access-key', 'juicedata'])
            options.extend(['--secret-key', '12345678'])
        if encrypt_secret:
            options.append('--encrypt-secret')
        options.append('--force')
        run_jfs_cmd(options)
        print('config succeed')

    @rule(
          juicefs=st.sampled_from(JFS_BINS),
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
        assume (self.is_supported_version(juicefs))
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
                subprocess.check_call('openssl genrsa -out my-priv-key.pem -aes256  -passout pass:12345678 2048'.split())
            os.environ['JFS_RSA_PASSPHRASE'] = '12345678'
            options.extend(['--encrypt-rsa-key', 'my-priv-key.pem'])
            if os.system(f'{juicefs} format --help | grep encrypt-algo') == 0:
                options.extend(['--encrypt-algo', encrypt_algo])
        
        if storage == 'minio':
            bucket = 'http://localhost:9000/test-bucket'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'minioadmin'])
            options.extend(['--secret-key', 'minioadmin'])
        elif storage == 'file':
            bucket = os.path.expanduser('~/.juicefs/local/')
            options.extend(['--bucket', bucket])
        else:
            raise Exception(f'storage value error: {storage}')

        if not self.formatted:
            if os.path.exists(JuicefsMachine.MOUNT_POINT):
                os.system('umount %s'%JuicefsMachine.MOUNT_POINT)
                print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            clear_storage(storage, bucket, JuicefsMachine.VOLUME_NAME)
            flush_meta(meta_url)
        print(f'format options: {" ".join(options)}' )
        run_jfs_cmd(options)
        self.meta_url = meta_url
        self.formatted = True
        self.formatted_by = juicefs
        print('format succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted )
    def status(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start status')
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        print(f'status output: {output.decode()}')
        uuid = json.loads(output.decode().replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        if self.mounted:
            sessions = json.loads(output.decode().replace("'", '"'))['Sessions']
            assert len(sessions) != 0 
        print('status succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted )
    def mount(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start mount')
        run_jfs_cmd([juicefs, 'mount', '-d',  self.meta_url, JuicefsMachine.MOUNT_POINT])
        time.sleep(2)
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        print(f'status output: {output.decode()}')
        sessions = json.loads(output.decode().replace("'", '"'))['Sessions']
        assert len(sessions) != 0 
        self.mounted = True
        print('mount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
    force=st.booleans())
    @precondition(lambda self: self.mounted)
    def umount(self, juicefs, force):
        assume (self.is_supported_version(juicefs))
        print('start umount')
        options = [juicefs, 'umount', JuicefsMachine.MOUNT_POINT]
        if force:
            options.append('--force')
        run_jfs_cmd(options)
        self.mounted = False
        print('umount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and not self.mounted)
    def destroy(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start destroy')
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        uuid = json.loads(output.decode().replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        options = [juicefs, 'destroy', self.meta_url, uuid]
        options.append('--force')
        run_jfs_cmd(options)
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

    @rule(juicefs = st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted )
    def dump(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start dump')
        run_jfs_cmd([juicefs, 'dump', self.meta_url, 'dump.json'])
        print('dump succeed')

    @rule(juicefs = st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and os.path.exists('dump.json'))
    def load(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start load')
        if os.path.exists(JuicefsMachine.MOUNT_POINT):
            os.system('umount %s'%JuicefsMachine.MOUNT_POINT)
            print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            self.mounted = False
        flush_meta(self.meta_url)
        run_jfs_cmd([juicefs, 'load', self.meta_url, 'dump.json'])
        print('load succeed')
        options = [juicefs, 'config', self.meta_url]
        options.extend(['--access-key', 'minioadmin', '--secret-key', 'minioadmin', '--encrypt-secret'])
        run_jfs_cmd(options)
        os.remove('dump.json')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted)
    def fsck(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start fsck')
        run_jfs_cmd([juicefs, 'fsck', self.meta_url])
        print('fsck succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS),
     block_size=st.integers(min_value=1, max_value=32),
     big_file_size=st.integers(min_value=100, max_value=200),
     small_file_size=st.integers(min_value=1, max_value=256),
     small_file_count=st.integers(min_value=100, max_value=256), 
     threads=st.integers(min_value=1, max_value=100))
    @precondition(lambda self: self.mounted and False)
    def bench(self, juicefs, block_size, big_file_size, small_file_size, small_file_count, threads):
        assume (self.is_supported_version(juicefs))
        print('start bench')
        os.system(f'df | grep {JuicefsMachine.MOUNT_POINT}')
        options = [juicefs, 'bench', JuicefsMachine.MOUNT_POINT]
        options.extend(['--block-size', str(block_size)])
        options.extend(['--big-file-size', str(big_file_size)])
        options.extend(['--small-file-size', str(small_file_size)])
        options.extend(['--small-file-count', str(small_file_count)])
        options.extend(['--threads', str(threads)])
        output = run_jfs_cmd(options)
        summary = output.decode('utf8').split('\n')[2]
        expected = f'BlockSize: {block_size} MiB, BigFileSize: {big_file_size} MiB, SmallFileSize: {small_file_size} KiB, SmallFileCount: {small_file_count}, NumThreads: {threads}'
        assert summary == expected
        print('bench succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS),
        threads=st.integers(min_value=1, max_value=100), 
        background = st.booleans(), 
        from_file = st.booleans(),
        directory = st.booleans() )
    @precondition(lambda self: self.mounted)
    def warmup(self, juicefs, threads, background, from_file, directory):
        assume (self.is_supported_version(juicefs))
        print('start warmup')
        clear_cache()
        options = [juicefs, 'warmup']
        options.extend(['--threads', str(threads)])
        if background:
            options.append('--background')
        if from_file:
            path_list = [JuicefsMachine.MOUNT_POINT+'file1', JuicefsMachine.MOUNT_POINT+'file2', JuicefsMachine.MOUNT_POINT+'file3']
            for filepath in path_list:
                if not os.path.exists(filepath):
                    os.system(f'dd if=/dev/urandom of={filepath} bs=1048576 count=65')
            with open('file.list', 'w') as f:
                for path in path_list:
                    f.write(path+'\n')
            options.extend(['--file', 'file.list'])
        else:
            if directory:
                options.append(JuicefsMachine.MOUNT_POINT)
            else:
                os.system(f'dd if=/dev/urandom of={JuicefsMachine.MOUNT_POINT}/bigfile bs=1048576 count=512')
                options.append(JuicefsMachine.MOUNT_POINT+'/bigfile')
                
        run_jfs_cmd(options)
        # print(output)
        print('warmup succeed')
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up count: ')
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up bytes: ')

    @rule(
        juicefs = st.sampled_from(JFS_BINS), 
        compact=st.booleans(), 
        delete=st.booleans(),
        threads=st.integers(min_value=1, max_value=100) )
    @precondition(lambda self: self.formatted)
    def gc(self, juicefs, compact, delete, threads):
        assume (self.is_supported_version(juicefs))
        print('start gc')
        options = [juicefs, 'gc', self.meta_url]
        if compact:
            options.append('--compact')
        if delete:
            options.append('--delete')
        options.extend(['--threads', str(threads)])
        run_jfs_cmd(options)
        # print(output)
        print('gc succeed')

    @rule(juicefs = st.sampled_from(JFS_BINS),
        port=st.integers(min_value=9001, max_value=10000))
    @precondition(lambda self: self.formatted)
    def gateway(self, juicefs, port):
        assume (self.is_supported_version(juicefs))
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

    @rule(juicefs = st.sampled_from(JFS_BINS), 
        port=st.integers(min_value=10001, max_value=11000)) 
    @precondition(lambda self: self.formatted )
    def webdav(self, juicefs, port):
        assume (self.is_supported_version(juicefs))
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

    def is_supported_version(self, ver):
        return version.parse('-'.join(ver.split('-')[1:])) >=  version.parse('-'.join(self.formatted_by.split('-')[1:]))

TestJuiceFS = JuicefsMachine.TestCase

if __name__ == "__main__":
    unittest.main()