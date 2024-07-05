import subprocess
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from datetime import datetime
import json
import os
from pickle import FALSE
import platform
import shutil
import sys
from termios import TIOCPKT_DOSTOP
import time
import unittest
from xmlrpc.client import boolean
import hypothesis
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine
from hypothesis import Phase, Verbosity, assume, settings, strategies as st
from hypothesis import seed
from hypothesis.database import DirectoryBasedExampleDatabase
from packaging import version
import subprocess
try:
    __import__("minio")
except ImportError:
    subprocess.check_call(["pip", "install", "minio"])
from minio import Minio
import uuid
from utils import *
from cmptree import *
import random
SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
@seed(SEED)
class JuicefsMachine(RuleBasedStateMachine):
    MIN_CLIENT_VERSIONS = ['0.0.1', '0.0.17','1.0.0-beta1', '1.0.0-rc1']
    MAX_CLIENT_VERSIONS = ['1.2.0', '2.0.0']
    JFS_BINS = ['./'+os.environ.get('OLD_JFS_BIN'), './'+os.environ.get('NEW_JFS_BIN')]
    meta_dict = {'redis':'redis://localhost/1', 'mysql':'mysql://root:root@(127.0.0.1)/test', 'postgres':'postgres://postgres:postgres@127.0.0.1:5432/test?sslmode=disable', \
        'tikv':'tikv://127.0.0.1:2379', 'badger':'badger://badger-data', 'mariadb': 'mysql://root:root@(127.0.0.1)/test', \
            'sqlite3': 'sqlite3://test.db', 'fdb':'fdb:///home/runner/fdb.cluster?prefix=jfs'}
    META_URL = meta_dict[os.environ.get('META')]
    STORAGE = os.environ.get('STORAGE')
    MOUNT_POINT = '/tmp/sync-test/'
    VOLUME_NAME = 'test-volume'
    # valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        self.run_id = uuid.uuid4().hex
        print(f'\ninit with run_id: {self.run_id}')
        with open(os.path.expanduser('~/command.log'), 'a') as f:
            f.write(f'init with run_id: {self.run_id}\n')
        self.formatted = False
        self.mounted = False
        # mount at least once, see ref: https://github.com/juicedata/juicefs/issues/2717
        self.mounted_by = []
        self.formatted_by = ''
        self.dumped_by = ''
        if JuicefsMachine.META_URL.startswith('badger://'):
            # change url for each run
            JuicefsMachine.META_URL = f'badger://badger-{uuid.uuid4().hex}'
        if JuicefsMachine.STORAGE == 'minio':
            run_cmd(f'mc alias set myminio http://localhost:9000 minioadmin minioadmin')
        if os.path.isfile('dump.json'):
            os.remove('dump.json')
        os.environ['PGPASSWORD'] = 'postgres'

    @rule(
          juicefs=st.sampled_from(JFS_BINS),
          block_size=st.integers(min_value=1, max_value=4096*10), 
          capacity=st.integers(min_value=0, max_value=1024),
          inodes=st.integers(min_value=1024*1024, max_value=1024*1024*1024),
          compress=st.sampled_from(['lz4', 'zstd', 'none']),
          shards=st.integers(min_value=0, max_value=1),
          storage=st.just(STORAGE), 
          encrypt_rsa_key = st.booleans(), 
          encrypt_algo = st.sampled_from(['aes256gcm-rsa','chacha20-rsa']),
          trash_days=st.integers(min_value=0, max_value=10000), 
          hash_prefix=st.booleans(), 
          force = st.booleans(), 
          no_update = st.booleans()
          )
    def format(self, juicefs, block_size, capacity, inodes, compress, shards, storage, encrypt_rsa_key, encrypt_algo, trash_days, hash_prefix, force, no_update):
        assume (self.greater_than_version_formatted(juicefs))
        print('start format')
        options = [juicefs, 'format',  JuicefsMachine.META_URL, JuicefsMachine.VOLUME_NAME]
        if not self.formatted:
            options.extend(['--block-size', str(block_size)])
            options.extend(['--compress', compress])
            options.extend(['--shards', str(shards)])
            options.extend(['--storage', storage])
            if hash_prefix and run_cmd(f'{juicefs} format --help | grep hash-prefix') == 0:
                options.append('--hash-prefix')
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        if run_cmd(f'{juicefs} format --help | grep trash-days') == 0:
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
            if run_cmd(f'{juicefs} format --help | grep encrypt-algo') == 0:
                options.extend(['--encrypt-algo', encrypt_algo])
        
        if storage == 'minio':
            bucket = 'http://localhost:9000/testbucket'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'minioadmin'])
            options.extend(['--secret-key', 'minioadmin'])
            if self.formatted and version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
                # use the latest version to change secret-key because rc1 has a bug for secret-key
                options[0] = JuicefsMachine.JFS_BINS[1]
        elif storage == 'file':
            bucket = os.path.expanduser('~/.juicefs/local/')
            options.extend(['--bucket', bucket])
        elif storage == 'mysql':
            bucket = '(localhost:3306)/testbucket'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'root'])
            options.extend(['--secret-key', 'root'])
        elif storage == 'postgres':
            bucket = 'localhost:5432/testbucket?sslmode=disable'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'postgres'])
            options.extend(['--secret-key', 'postgres'])
        else:
            print(f'storage is {storage}')
            raise Exception(f'storage value error: {storage}')

        if not self.formatted:
            if os.path.exists(JuicefsMachine.MOUNT_POINT) and os.path.exists(JuicefsMachine.MOUNT_POINT+'.accesslog'):
                run_cmd('umount %s'%JuicefsMachine.MOUNT_POINT)
                print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            clear_storage(storage, bucket, JuicefsMachine.VOLUME_NAME)
            flush_meta(JuicefsMachine.META_URL)
        print(f'format options: {" ".join(options)}' )
        run_jfs_cmd(options)
        self.formatted = True
        self.formatted_by = juicefs
        print('format succeed')


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
        assume (self.greater_than_version_formatted(juicefs))
        assume(run_cmd(f'{juicefs} --help | grep config') == 0)
        print('start config')
        options = [juicefs, 'config', JuicefsMachine.META_URL]
        options.extend(['--trash-days', str(trash_days)])
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        assert version.parse(min_client_version) <= version.parse(max_client_version)
        if run_cmd(f'{juicefs} config --help | grep min-client-version') == 0:
            options.extend(['--min-client-version', min_client_version])
        if run_cmd(f'{juicefs} config --help | grep max-client-version') == 0:
            options.extend(['--max-client-version', max_client_version])
        storage = get_storage(juicefs, JuicefsMachine.META_URL)
        
        if change_bucket:
            if storage == 'file':
                options.extend(['--bucket', os.path.expanduser('~/.juicefs/local2')])
            elif storage == 'minio': 
                c = Minio('localhost:9000', access_key='minioadmin', secret_key='minioadmin', secure=False)
                if not c.bucket_exists('testbucket2'):
                    run_cmd('mc mb myminio/testbucket2')
                    # assert c.bucket_exists('testbucket2')
                options.extend(['--bucket', 'http://localhost:9000/testbucket2'])
            elif storage == 'mysql':
                create_mysql_db('mysql://root:root@(localhost:3306)/testbucket2')
                options.extend(['--bucket', '(localhost:3306)/testbucket2'])
            elif storage == 'postgres':
                create_postgres_db('postgres://postgres:postgres@localhost:5432/testbucket2?sslmode=disable')
                options.extend(['--bucket', 'localhost:5432/testbucket2?sslmode=disable'])
        if change_aksk and storage == 'minio':
            output = subprocess.check_output('mc admin user list myminio'.split())
            if not output:
                run_cmd('mc admin user add myminio juicedata 12345678')
                run_cmd('mc admin policy attach myminio consoleAdmin --user juicedata')
            options.extend(['--access-key', 'juicedata'])
            options.extend(['--secret-key', '12345678'])
            if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
                # use the latest version to set secret-key because rc1 has a bug for secret-key
                options[0] = JuicefsMachine.JFS_BINS[1]
        if encrypt_secret and run_cmd(f'{juicefs} config --help | grep encrypt-secret') == 0:
            # 0.17.5 store the secret without encrypt, ref: https://github.com/juicedata/juicefs/issues/2721
            #if version.parse('-'.join(juicefs.split('-')[1:])) > version.parse('0.17.5'):
            options.append('--encrypt-secret')
        options.append('--force')
        run_jfs_cmd(options)
        if change_bucket:
            # change bucket back to avoid fsck fail.
            if storage == 'file':
                run_jfs_cmd([juicefs, 'config', JuicefsMachine.META_URL, '--bucket', os.path.expanduser('~/.juicefs/local')])
            elif storage == 'minio':
                run_jfs_cmd([juicefs, 'config', JuicefsMachine.META_URL, '--bucket', 'http://localhost:9000/testbucket'])
            elif storage == 'mysql':
                run_jfs_cmd([juicefs, 'config', JuicefsMachine.META_URL, '--bucket', '(localhost:3306)/testbucket'])
            elif storage == 'postgres':
                run_jfs_cmd([juicefs, 'config', JuicefsMachine.META_URL, '--bucket', 'localhost:5432/testbucket?sslmode=disable'])
        self.formatted_by = juicefs
        print('config succeed')


    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted )
    def status(self, juicefs):
        assume (self.greater_than_version_formatted(juicefs))
        print('start status')
        output = subprocess.run([juicefs, 'status', JuicefsMachine.META_URL], check=True, stdout=subprocess.PIPE).stdout.decode()
        if 'get timestamp too slow' in output: 
            # remove the first line caust it is tikv log message
            output = '\n'.join(output.split('\n')[1:])
        print(f'status output: {output}')
        try:
            uuid = json.loads(output.replace("'", '"'))['Setting']['UUID']
        except:
            raise Exception(f'parse uuid failed, output: {output}')
        assert len(uuid) != 0
        if self.mounted and not is_readonly(JuicefsMachine.MOUNT_POINT) and self.greater_than_version_mounted(juicefs):
            sessions = json.loads(output.replace("'", '"'))['Sessions']
            assert len(sessions) != 0 
        print('status succeed')


    @rule(juicefs=st.sampled_from(JFS_BINS), 
        other_fuse_options=st.lists(st.sampled_from(['debug', 'allow_other', 'writeback_cache']), unique=True), 
        enable_xattr=st.booleans(),
        writeback=st.just(False),
        read_only=st.booleans(),
        no_bgjob=st.booleans(),
        sub_dir=st.sampled_from(['dir1', 'dir2']),
    )
    @precondition(lambda self: self.formatted  )
    def mount(self, juicefs, other_fuse_options, enable_xattr,
        writeback, read_only, no_bgjob, sub_dir):
        assume (self.greater_than_version_formatted(juicefs))
        if JuicefsMachine.META_URL.startswith('badger://'):
            assume(not self.mounted)
        retry = 3
        while os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog') and retry > 0:
            os.system(f'umount {JuicefsMachine.MOUNT_POINT}')
            retry = retry - 1 
            time.sleep(1)
        if os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'):
            print(f'FATAL: umount {JuicefsMachine.MOUNT_POINT} failed.')
        assume(not os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start mount')
        options = [juicefs, 'mount', '-d',  JuicefsMachine.META_URL, JuicefsMachine.MOUNT_POINT]
        if other_fuse_options:
            options.extend(['-o', ','.join(other_fuse_options)])
        if 'allow_other' in other_fuse_options:
            if os.path.exists('/etc/fuse.conf'):
                # subprocess.check_call(['sudo', 'bash',  '-c', '"echo user_allow_other >>/etc/fuse.conf"' ])
                os.system('sudo bash -c "echo user_allow_other >>/etc/fuse.conf"')
                print('add user_allow_other to /etc/fuse.conf succeed')
        if enable_xattr:
            options.append('--enable-xattr')
        if writeback:
            options.append('--writeback')
        upload_delay = str(upload_delay)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            upload_delay = upload_delay + 's'
        options.extend(['--upload-delay', str(upload_delay)])
        backup_meta = str(backup_meta)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            backup_meta = '1h0m0s'
        if run_cmd(f'{juicefs} mount --help | grep backup-meta') == 0:
            options.extend(['--backup-meta', backup_meta])
        if read_only:
            options.append('--read-only')
        if no_bgjob and run_cmd(f'{juicefs} mount --help | grep no-bgjob') == 0:
            options.append('--no-bgjob')

        print('TODO: subdir')
        # options.extend('--subdir', str(sub_dir))
        if os.path.exists(JuicefsMachine.MOUNT_POINT):
            run_cmd(f'stat {JuicefsMachine.MOUNT_POINT}')
        run_jfs_cmd(options)
        time.sleep(2)
        if platform.system() == 'Linux':
            inode = subprocess.check_output(f'stat -c %i {JuicefsMachine.MOUNT_POINT}'.split())
        elif platform.system() == 'Darwin':
            inode = subprocess.check_output(f'stat -f %i {JuicefsMachine.MOUNT_POINT}'.split())
        print(f'inode number: {inode}')
        assert(inode.decode()[:-1] == '1')
        output = subprocess.run([juicefs, 'status', JuicefsMachine.META_URL], check=True, stdout=subprocess.PIPE).stdout.decode()
        if 'get timestamp too slow' in output: 
            # remove the first line caust it is tikv log message
            output = '\n'.join(output.split('\n')[1:])
        print(f'status output: {output}')
        sessions = json.loads(output.replace("'", '"'))['Sessions']
        if not read_only: 
            assert len(sessions) != 0 
        self.mounted = True
        if not read_only:
            self.mounted_by.append(juicefs)
        print('mount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
        file_name=st.just('file_to_info'), 
        data = st.binary())
    @precondition(lambda self: self.formatted and self.mounted )
    def info(self, juicefs, file_name, data):
        assume (self.greater_than_version_formatted(juicefs))
        assume (self.greater_than_version_mounted(juicefs))
        assume(not is_readonly(f'{JuicefsMachine.MOUNT_POINT}'))
        assert(os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start info')
        path = JuicefsMachine.MOUNT_POINT+file_name
        write_data(JuicefsMachine.MOUNT_POINT, path, data)
        options = [juicefs, 'info', path]
        run_jfs_cmd(options)
        print('info succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
    file_name=st.just('file_to_rmr'))
    @precondition(lambda self: self.formatted and self.mounted )
    def rmr(self, juicefs, file_name):
        assume (self.greater_than_version_formatted(juicefs))
        assume (self.greater_than_version_mounted(juicefs))
        assume(not is_readonly(f'{JuicefsMachine.MOUNT_POINT}'))
        # ref: https://github.com/juicedata/juicefs/pull/2776
        assert(len(self.mounted_by) > 0)
        assume(version.parse('-'.join(self.mounted_by[-1].split('-')[1:])) >= version.parse('1.1.0-dev'))
        assume(version.parse('-'.join(juicefs.split('-')[1:])) >= version.parse('1.1.0-dev'))
        # TODO: should test upload delay.
        assume(get_upload_delay_seconds(JuicefsMachine.MOUNT_POINT) == 0)
        assert(os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start rmr')
        path = f'{JuicefsMachine.MOUNT_POINT}{file_name}'
        write_block(JuicefsMachine.MOUNT_POINT, path, 1048576, 3)
        os.system(f'ls -l {path}')
        assert(os.path.exists(path))
        run_cmd(f'stat {path}')
        options = [juicefs, 'rmr', path]
        run_jfs_cmd(options)
        # TODO: should uncomment the assert
        # assert(not os.path.exists(path))
        print('rmr succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
    force=st.booleans())
    @precondition(lambda self: self.mounted)
    def umount(self, juicefs, force):
        assume (self.greater_than_version_formatted(juicefs))
        print('start umount')
        options = [juicefs, 'umount', JuicefsMachine.MOUNT_POINT]
        # don't force umount because it may not unmounted succeed.
        # if force:
        #    options.append('--force')
        run_jfs_cmd(options)
        self.mounted = False
        print('umount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and not self.mounted)
    def destroy(self, juicefs):
        assume (self.greater_than_version_formatted(juicefs))
        assume(run_cmd(f'{juicefs} --help | grep destroy') == 0)
        print('start destroy')
        output = subprocess.run([juicefs, 'status', JuicefsMachine.META_URL], check=True, stdout=subprocess.PIPE).stdout.decode()
        if 'get timestamp too slow' in output: 
            # remove the first line caust it is tikv log message
            output = '\n'.join(output.split('\n')[1:]) 
        print(f'status output: {output}')
        uuid = json.loads(output.replace("'", '"'))['Setting']['UUID']
        print(f'uuid is: {uuid}')
        assert len(uuid) != 0
        options = [juicefs, 'destroy', JuicefsMachine.META_URL, uuid]
        options.append('--force')
        run_jfs_cmd(options)
        self.formatted = False
        self.mounted = False
        self.mounted_by = []
        self.formatted_by = ''
        print('destroy succeed')

    @rule(file_name=st.sampled_from(['myfile1', 'myfile2']), 
        data=st.binary() )
    @precondition(lambda self: self.mounted )
    def write_and_read(self, file_name, data):
        assume(not is_readonly(f'{JuicefsMachine.MOUNT_POINT}'))
        assert(os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start write and read')
        path = JuicefsMachine.MOUNT_POINT+file_name
        write_data(JuicefsMachine.MOUNT_POINT, path, data)
        with open(path, "rb") as f:
            result = f.read()
        assert str(result) == str(data)
        print('write and read succeed')
    
    @rule(juicefs = st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted )
    def dump(self, juicefs):
        assume (self.greater_than_version_formatted(juicefs))
        # check this because of: https://github.com/juicedata/juicefs/issues/2717
        assume(juicefs in self.mounted_by)
        print('start dump')
        run_jfs_cmd([juicefs, 'dump', JuicefsMachine.META_URL, 'dump.json'])
        self.dumped_by = juicefs
        print('dump succeed')

    @rule(juicefs = st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and os.path.exists('dump.json'))
    def load(self, juicefs):
        assume (self.greater_than_version_formatted(juicefs))
        assume (self.greater_than_version_dumped(juicefs))
        print('start load')
        if os.path.exists(JuicefsMachine.MOUNT_POINT) and os.path.exists(JuicefsMachine.MOUNT_POINT+'.accesslog'):
            run_cmd('umount %s'%JuicefsMachine.MOUNT_POINT)
            print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            self.mounted = False
        flush_meta(JuicefsMachine.META_URL)
        run_jfs_cmd([juicefs, 'load', JuicefsMachine.META_URL, 'dump.json'])
        print('load succeed')
        options = [juicefs, 'config', JuicefsMachine.META_URL]
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
            # use the latest version to change secret-key because rc1 has a bug for secret-key
            options[0] = JuicefsMachine.JFS_BINS[1]
        storage = get_storage(juicefs, JuicefsMachine.META_URL)
        if storage == 'minio':
            run_jfs_cmd([JuicefsMachine.JFS_BINS[1], 'config', JuicefsMachine.META_URL, '--access-key', 'minioadmin', '--secret-key', 'minioadmin'])
        elif storage == 'mysql':
            run_jfs_cmd([JuicefsMachine.JFS_BINS[1], 'config', JuicefsMachine.META_URL, '--access-key', 'root', '--secret-key', 'root'])
        elif storage == 'postgres':
            run_jfs_cmd([JuicefsMachine.JFS_BINS[1], 'config', JuicefsMachine.META_URL, '--access-key', 'postgres', '--secret-key', 'postgres'])
        
        os.remove('dump.json')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted)
    def fsck(self, juicefs):
        assume (self.greater_than_version_formatted(juicefs))
        assume(juicefs in self.mounted_by)
        print('start fsck')
        run_jfs_cmd([juicefs, 'fsck', JuicefsMachine.META_URL])
        print('fsck succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS),
        threads=st.integers(min_value=1, max_value=100), 
        background = st.booleans(), 
        from_file = st.booleans(),
        directory = st.booleans() )
    @precondition(lambda self: self.mounted)
    def warmup(self, juicefs, threads, background, from_file, directory):
        assume (self.greater_than_version_formatted(juicefs))
        assume (self.greater_than_version_mounted(juicefs))
        assume(not is_readonly(f'{JuicefsMachine.MOUNT_POINT}'))
        assert(os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
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
                    write_block(JuicefsMachine.MOUNT_POINT, filepath, 4096, 100)
            with open('file.list', 'w') as f:
                for path in path_list:
                    f.write(path+'\n')
            time.sleep(get_upload_delay_seconds(f'{JuicefsMachine.MOUNT_POINT}')+1)
            while(get_stage_blocks(JuicefsMachine.MOUNT_POINT) != 0):
                print('sleep for stage')
                time.sleep(1)
            options.extend(['--file', 'file.list'])
        else:
            if directory:
                options.append(JuicefsMachine.MOUNT_POINT)
            else:
                write_block(JuicefsMachine.MOUNT_POINT, f'{JuicefsMachine.MOUNT_POINT}/file_to_warmup', 1048576, 100)
                assert os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/file_to_warmup')
                options.append(f'{JuicefsMachine.MOUNT_POINT}/file_to_warmup')
                
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
        assume (self.greater_than_version_formatted(juicefs))
        assume(juicefs in self.mounted_by)
        print('start gc')
        options = [juicefs, 'gc', JuicefsMachine.META_URL]
        if compact:
            options.append('--compact')
        if delete:
            options.append('--delete')
        options.extend(['--threads', str(threads)])
        run_jfs_cmd(options)
        # print(output)
        print('gc succeed')


    @rule(juicefs=st.sampled_from(JFS_BINS), 
        writeback=st.just(False),
        read_only=st.booleans(),
        no_bgjob=st.booleans(),
        multi_buckets=st.booleans(), 
        keep_etag=st.booleans(),
        umask=st.sampled_from(['022', '755']), 
        sub_dir=st.sampled_from(['dir1', 'dir2']),
        port=st.integers(min_value=9001, max_value=10000)
    )
    @precondition(lambda self: self.formatted and False)
    def gateway(self, juicefs, writeback, read_only, no_bgjob, multi_buckets, keep_etag, umask, sub_dir, port):
        assume (self.greater_than_version_formatted(juicefs))
        assume(not is_port_in_use(port))
        if JuicefsMachine.META_URL.startswith('badger://'):
            assume(not self.mounted)
        print('start gateway')
        os.environ['MINIO_ROOT_USER'] = 'admin'
        os.environ['MINIO_ROOT_PASSWORD'] = '12345678'
        options = [juicefs, 'gateway', JuicefsMachine.META_URL, f'localhost:{port}']
        
        if writeback:
            options.append('--writeback')
        
        if read_only:
            options.append('--read-only')
        if no_bgjob and run_cmd(f'{juicefs} gateway --help | grep no-bgjob') == 0:
            options.append('--no-bgjob')
        if multi_buckets and run_cmd(f'{juicefs} gateway --help | grep multi-buckets') == 0:
            options.append('--multi-buckets')
        if keep_etag and run_cmd(f'{juicefs} gateway --help | grep keep-etag') == 0:
            options.append('--keep-etag')
        if run_cmd(f'{juicefs} gateway --help | grep umask') == 0:
            options.extend(['--umask', umask])
        print(f'TODO: subdir:{sub_dir}')

        proc=subprocess.Popen(options)
        time.sleep(2.0)
        subprocess.Popen.kill(proc)
        print('gateway succeed')


    @rule(juicefs = st.sampled_from(JFS_BINS), 
        port=st.integers(min_value=10001, max_value=11000)) 
    @precondition(lambda self: self.formatted and False)
    def webdav(self, juicefs, port):
        assume (self.greater_than_version_formatted(juicefs))
        assert version.parse('-'.join(juicefs.split('-')[1:])) >=  version.parse('-'.join(self.formatted_by.split('-')[1:]))
        assume (not is_port_in_use(port))
        if JuicefsMachine.META_URL.startswith('badger://'):
            assume(not self.mounted)
        print('start webdav')
        
        options = [juicefs, 'webdav', JuicefsMachine.META_URL, f'localhost:{port}']
        proc = subprocess.Popen(options)
        time.sleep(2.0)
        subprocess.Popen.kill(proc)
        print('webdav succeed')

    def greater_than_version_formatted(self, ver:str):
        print(f'ver is {ver}, formatted_by is {self.formatted_by}')
        if not self.formatted_by:
            return True
        return version.parse('-'.join(ver.split('-')[1:])) >=  version.parse('-'.join(self.formatted_by.split('-')[1:]))

    def greater_than_version_dumped(self, ver:str):
        if not self.dumped_by:
            return True
        return version.parse('-'.join(ver.split('-')[1:])) >=  version.parse('-'.join(self.dumped_by.split('-')[1:]))

    def greater_than_version_mounted(self, ver:str):
        for mounted_version in self.mounted_by:
            if version.parse('-'.join(ver.split('-')[1:])) <  version.parse('-'.join(mounted_version.split('-')[1:])):
                return False
        return True


if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    ci_db = DirectoryBasedExampleDatabase(".hypothesis/examples")    
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("schedule", max_examples=500, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=200, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    settings.register_profile("pull_request", max_examples=100, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=50, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    
    if os.environ.get('CI'):
        event_name = os.environ.get('GITHUB_EVENT_NAME')
        if event_name == 'schedule':
            profile = 'schedule'
        else:
            profile = 'pull_request'
    else:
        profile = os.environ.get('PROFILE', 'dev')
    print(f'profile is {profile}')
    settings.load_profile(profile)
    juicefs_machine = JuicefsMachine.TestCase()
    juicefs_machine.runTest()
