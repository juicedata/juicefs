from fileinput import filename
import json
import os
import random
import shutil
import subprocess
import sys
import time
import unittest
from xmlrpc.client import boolean
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine
from hypothesis import strategies as st
from hypothesis.strategies import integers, lists
from hypothesis import given

class JuicefsMachine(RuleBasedStateMachine):
    CLIENT_VERSIONS = ['1.0.0', '0.0.17','0.5.0']
    JFS_BIN = ['juicefs-1.0.0-beta1', 'juicefs-1.0.0-beta2', 'juicefs-1.0.0-beta3', './juicefs-latest']
    JFS_BIN = ['./juicefs-1.0.0', './juicefs-latest']
    META_URL_SQLITE = 'sqlite3:///Users/chengzhou/Documents/juicefs2/abc.db'
    META_URL_REDIS = 'redis://localhost/1'
    # META_URL = 'badger://abc.db'
    MOUNT_POINT = '/tmp/sync-test/'
    VOLUME_NAME = 'test-volume'
    def flush_meta(self, meta_url):
        if meta_url.startswith('sqlite3://'):
            path = meta_url[len('sqlite3://'):]
        if os.path.isfile(path):
            os.remove(path)
            print(f'remove meta file {path} succeed')

    def clear_cache(self):
        cache_dir = os.path.expanduser('~/.juicefs/local/%s/'%JuicefsMachine.VOLUME_NAME)
        if os.path.exists(cache_dir):
            try:
                shutil.rmtree(cache_dir)
                print("remove cache dir {cache_dir} succeed")
            except OSError as e:
                print("Error: %s : %s" % (cache_dir, e.strerror))
        if sys.platform.startswith('linux') :
            os.system('echo 3> /proc/sys/vm/drop_caches')

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print('__init__')
        self.formatted = False
        self.mounted = False
        self.meta_url = None
        
        if os.path.exists(JuicefsMachine.MOUNT_POINT):
            os.system('umount %s'%JuicefsMachine.MOUNT_POINT)
            print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')

        self.clear_cache()

    @rule(
        juicefs=st.sampled_from(JFS_BIN),
        capacity=st.integers(min_value=0, max_value=1024), 
        inodes=st.integers(min_value=1024*1024, max_value=1024*1024*1024),
        change_bucket=st.booleans(), 
        change_aksk=st.booleans(), 
        encrypt_secret = st.booleans(), 
        trash_days =  st.integers(min_value=0, max_value=10000),
        min_client_version = st.sampled_from(CLIENT_VERSIONS), 
        max_client_version = st.sampled_from(CLIENT_VERSIONS), 
        force = st.booleans(),
    )
    @precondition(lambda self: self.formatted)
    def config(self, juicefs, capacity, inodes, change_bucket, change_aksk, encrypt_secret, trash_days, min_client_version, max_client_version, force):
        options = [juicefs, 'config', self.meta_url]
        options.extend(['--trash-days', trash_days])
        options.extend(['--capacity', capacity])
        options.extend(['--inodes', inodes])
        options.extend(['--min-client-version', min_client_version])
        options.extend(['--max-client-version', max_client_version])
        if change_bucket:
            options.extend(['--bucket', os.path.expanduser('~/.juicefs/local2')])
        if change_aksk:
            options.extend(['--access-key', 'ak'])
            options.extend(['--secret-key', 'sk'])
        if encrypt_secret:
            options.append('--encryt-secret')
        if force:
            options.append('--force')
        subprocess.check_call()
        print('config succeed')

    @rule(
          juicefs=st.sampled_from(JFS_BIN),
          block_size=st.integers(min_value=1, max_value=4096*10), 
          capacity=st.integers(min_value=0, max_value=1024),
          inodes=st.integers(min_value=1024*1024, max_value=1024*1024*1024),
          compress=st.sampled_from(['lz4', 'zstd', 'none']),
          shards=st.integers(min_value=0, max_value=100),
          storage=st.sampled_from(['file']), 
          encrypt_rsa_key = st.sampled_from([None]), 
          encrypt_algo = st.sampled_from(['aes256gcm-rsa','chacha20-rsa']),
          trash_days=st.integers(min_value=0, max_value=10000), 
          hash_prefix=st.booleans(), 
          force = st.booleans(), 
          no_update = st.booleans(),
          meta_url=st.sampled_from([META_URL_SQLITE]),
          )
    def format(self, juicefs, block_size, capacity, inodes, compress, shards, storage, encrypt_rsa_key, encrypt_algo, trash_days, hash_prefix, force, no_update, meta_url):
        options = [juicefs, 'format',  meta_url, JuicefsMachine.VOLUME_NAME, '--capacity', str(capacity), '--inodes', str(inodes), '--shards', str(shards),'--trash-days', str(trash_days)]
        if not self.formatted:
            options.extend(['--block-size', str(block_size)])
            options.extend(['--compress', compress])
        if hash_prefix:
            options.append('--hash-prefix')
        if force:
            options.append('--force')
        if no_update:
            options.append('--no-update')
        if encrypt_rsa_key:
            # TODO: pass real rsa key
            options.extend(['--encrypt-rsa-key', encrypt_rsa_key])
            options.extend(['--encrypt-algo', encrypt_algo])
        if storage == 'minio':
            options.extend(['--bucket', 'http://127.0.0.1:9000/test-bucket'])
            options.extend(['--access-key', 'minioadmin'])
            options.extend(['--secret-key', 'minioadmin'])
        if not self.formatted:
            self.flush_meta(meta_url)
        print(f'format options: {" ".join(options)}' )
        subprocess.check_call(options)
        self.meta_url = meta_url
        self.formatted = True
        print('format succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted )
    def status(self, juicefs):
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        uuid = json.loads(output.decode('utf8').replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        if self.mounted:
            sessions = json.loads(output.decode('utf8').replace("'", '"'))['Sessions']
            assert len(sessions) != 0 
        print('status succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted )
    def mount(self, juicefs):
        subprocess.check_call([juicefs, 'mount', '-d',  self.meta_url, JuicefsMachine.MOUNT_POINT])
        time.sleep(1)
        self.mounted = True
        print('mount succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN), 
    force=st.booleans())
    @precondition(lambda self: self.mounted)
    def umount(self, juicefs, force):
        options = [juicefs, 'umount', JuicefsMachine.MOUNT_POINT]
        if force:
            options.append('--force')
        subprocess.check_call(options)
        self.mounted = False
        print('umount succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN), 
    force=st.booleans())
    @precondition(lambda self: self.formatted and not self.mounted)
    def destroy(self, juicefs, force):
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        uuid = json.loads(output.decode('utf8').replace("'", '"'))['Setting']['UUID']
        assert len(uuid) != 0
        options = [juicefs, 'destroy', self.meta_url, uuid]
        if force:
            options.append('--force')
        subprocess.check_call(options)
        self.formatted = False
        self.mounted = False
        print('destroy succeed')

    valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
    @rule(file_name=valid_file_name, data=st.binary() )
    @precondition(lambda self: self.mounted )
    def write(self, file_name, data):
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
        subprocess.check_call([juicefs, 'dump', self.meta_url, 'dump.json'])
        print('dump succeed')

    @rule(juicefs = st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted and os.path.exists('dump.json'))
    def load(self, juicefs):
        print('start to load')
        self.flush_meta(self.meta_url)
        subprocess.check_call([juicefs, 'load', self.meta_url, 'dump.json'])
        print('load succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted)
    def fsck(self, juicefs):
        subprocess.check_call([juicefs, 'fsck', self.meta_url])
        print('fsck succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN),
     block_size=st.integers(min_value=1, max_value=32),
     big_file_size=st.integers(min_value=100, max_value=1024),
     small_file_size=st.integers(min_value=1, max_value=1024),
     small_file_count=st.integers(min_value=100, max_value=1024), 
     threads=st.integers(min_value=1, max_value=100))
    @precondition(lambda self: self.mounted)
    def bench(self, juicefs, block_size, big_file_size, small_file_size, small_file_count, threads):
        options = [juicefs, 'bench', JuicefsMachine.MOUNT_POINT]
        options.extend(['--block-size', str(block_size)])
        options.extend(['--big-file-size', str(big_file_size)])
        options.extend(['--small-file-size', str(small_file_size)])
        options.extend(['--small-file-count', str(small_file_count)])
        options.extend(['--threads', str(threads)])
        output = subprocess.check_output(options)
        summary = output.decode('utf8').split('\n')[2]
        expected = f'BlockSize: {block_size} MiB, BigFileSize: {big_file_size} MiB, SmallFileSize: {small_file_size} KiB, SmallFileCount: {small_file_count}, NumThreads: {threads}'
        assert summary == expected
        print('config succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN),
        threads=st.integers(min_value=1, max_value=100), 
        backgroud = st.booleans(), 
        from_file = st.booleans(),
        directory = st.booleans() )
    @precondition(lambda self: self.mounted)
    def warmup(self, juicefs, threads, background, from_file, directory):
        self.clear_cache()
        options = [juicefs, 'warmup']
        options.extend(['--threads', threads])
        if background:
            options.append('--backgroud')
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
                
        output = subprocess.check_output(options)
        print(output)
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up count: ')
        # assert output.decode('utf8').split('\n')[0].startswith('Warming up bytes: ')

    @rule(
    juicefs = st.sampled_from(JFS_BIN), 
    compact=st.booleans(), 
    delete=st.booleans(),
    threads=st.integers(min_value=1, max_value=100) )
    @precondition(lambda self: self.formatted)
    def gc(self, juicefs, compact, delete, threads):
        options = [juicefs, 'gc']
        if compact:
            options.append('--compact')
        if delete:
            options.append('--delete')
        options.extend(['threads', threads])
        output = subprocess.check_output(options)
        print(output)

    @rule(juicefs = st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted)
    def gateway(self, juicefs):
        os.system('export MINIO_ROOT_USER=admin')
        os.system('export MINIO_ROOT_USER=123456')
        options = [juicefs, 'gateway', self.meta_url, 'localhost:9000']
        subprocess.check_call(options)

    @rule(juicefs = st.sampled_from(JFS_BIN)) 
    def webdav(self, juicefs):
        options = [juicefs, 'webdav', self.meta_url, 'localhost:9007']
        subprocess.check_call(options)

TestJuiceFS = JuicefsMachine.TestCase

if __name__ == "__main__":
    unittest.main()