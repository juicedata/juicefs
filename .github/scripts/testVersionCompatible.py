import json
import os
import platform
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
from utils import flush_meta, clear_storage, clear_cache, run_jfs_cmd, run_cmd, is_readonly, get_upload_delay_seconds, get_stage_blocks, write_data, write_block

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
    valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print('\n__init__')
        with open('command.log', 'a') as f:
            f.write('init------------------------------------\n')
        self.formatted = False
        self.mounted = False
        self.meta_url = None
        self.formatted_by = ''
        run_cmd(f'mc alias set myminio http://localhost:9000 minioadmin minioadmin')
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
        assume(run_cmd(f'{juicefs} --help | grep config') == 0)
        print('start config')
        options = [juicefs, 'config', self.meta_url]
        options.extend(['--trash-days', str(trash_days)])
        options.extend(['--capacity', str(capacity)])
        options.extend(['--inodes', str(inodes)])
        assert version.parse(min_client_version) <= version.parse(max_client_version)
        if run_cmd(f'{juicefs} config --help | grep --min-client-version') == 0:
            options.extend(['--min-client-version', min_client_version])
        if run_cmd(f'{juicefs} config --help | grep --max-client-version') == 0:
            options.extend(['--max-client-version', max_client_version])
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        storage = json.loads(output.decode().replace("'", '"'))['Setting']['Storage']
        
        if change_bucket:
            if storage == 'file':
                options.extend(['--bucket', os.path.expanduser('~/.juicefs/local2')])
            elif storage == 'minio': 
                c = Minio('localhost:9000', access_key='minioadmin', secret_key='minioadmin', secure=False)
                if not c.bucket_exists('test-bucket2'):
                    run_cmd('mc mb myminio/test-bucket2')
                    # assert c.bucket_exists('test-bucket2')
                options.extend(['--bucket', 'http://localhost:9000/test-bucket2'])
        if change_aksk and storage == 'minio':
            output = subprocess.check_output('mc admin user list myminio'.split())
            if not output:
                run_cmd('mc admin user add myminio juicedata 12345678')
                run_cmd('mc admin policy set myminio consoleAdmin user=juicedata')
            options.extend(['--access-key', 'juicedata'])
            options.extend(['--secret-key', '12345678'])
            if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
                # use the latest version to set secret-key because rc1 has a bug for secret-key
                options[0] = JuicefsMachine.JFS_BINS[1]
        if encrypt_secret and run_cmd(f'{juicefs} config --help | grep --encrypt-secret') == 0:
            # version.parse('-'.join(JuicefsMachine.JFS_BINS[1].split('-')[1:])) >= version.parse('1.0.0-rc2'):
            # rc1 has a bug on encrypt-secret 
            options.append('--encrypt-secret')
        options.append('--force')
        run_jfs_cmd(options)
        if change_bucket:
            # change bucket back to avoid fsck fail.
            if storage == 'file':
                run_jfs_cmd([juicefs, 'config', self.meta_url, '--bucket', os.path.expanduser('~/.juicefs/local')])
            elif storage == 'minio':
                run_jfs_cmd([juicefs, 'config', self.meta_url, '--bucket', 'http://localhost:9000/test-bucket'])
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
            bucket = 'http://localhost:9000/test-bucket'
            options.extend(['--bucket', bucket])
            options.extend(['--access-key', 'minioadmin'])
            options.extend(['--secret-key', 'minioadmin'])
            if self.formatted and version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
                # use the latest version to change secret-key because rc1 has a bug for secret-key
                options[0] = JuicefsMachine.JFS_BINS[1]
        elif storage == 'file':
            bucket = os.path.expanduser('~/.juicefs/local/')
            options.extend(['--bucket', bucket])
        else:
            raise Exception(f'storage value error: {storage}')

        if not self.formatted:
            if os.path.exists(JuicefsMachine.MOUNT_POINT) and os.path.exists(JuicefsMachine.MOUNT_POINT+'.accesslog'):
                run_cmd('umount %s'%JuicefsMachine.MOUNT_POINT)
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
        output = subprocess.check_output([juicefs, 'status', self.meta_url]).decode()
        try:
            uuid = json.loads(output.replace("'", '"'))['Setting']['UUID']
        except:
            raise Exception(f'parse uuid failed, output: {output}')
        assert len(uuid) != 0
        if self.mounted and not is_readonly(JuicefsMachine.MOUNT_POINT):
            sessions = json.loads(output.replace("'", '"'))['Sessions']
            assert len(sessions) != 0 
        print('status succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
        no_syslog=st.booleans(),
        other_fuse_options=st.lists(st.sampled_from(['debug', 'allow_other', 'writeback_cache']), unique=True), 
        enable_xattr=st.booleans(),
        attr_cache=st.integers(min_value=1, max_value=10), 
        entry_cache=st.integers(min_value=1, max_value=10), 
        dir_entry_cache=st.integers(min_value=1, max_value=10), 
        get_timeout=st.integers(min_value=30, max_value=60), 
        put_timeout=st.integers(min_value=30, max_value=60), 
        io_retries=st.integers(min_value=5, max_value=15), 
        max_uploads=st.integers(min_value=1, max_value=100), 
        max_deletes=st.integers(min_value=1, max_value=100), 
        buffer_size=st.integers(min_value=100, max_value=1000), 
        upload_limit=st.integers(min_value=0, max_value=1000), 
        download_limit=st.integers(min_value=0, max_value=1000), 
        prefetch=st.integers(min_value=0, max_value=100), 
        writeback=st.booleans(),
        upload_delay=st.sampled_from([0, 2]), 
        cache_dir=st.sampled_from(['cache1', 'cache2']),
        cache_size=st.integers(min_value=0, max_value=1024000), 
        free_space_ratio=st.floats(min_value=0.1, max_value=0.5), 
        cache_partial_only=st.booleans(),
        backup_meta=st.integers(min_value=30, max_value=59),
        heartbeat=st.integers(min_value=5, max_value=12), 
        read_only=st.booleans(),
        no_bgjob=st.booleans(),
        open_cache=st.integers(min_value=0, max_value=100),
        sub_dir=valid_file_name,
        metrics=st.sampled_from(['127.0.0.1:9567', '127.0.0.1:9568']), 
        consul=st.sampled_from(['127.0.0.1:8500', '127.0.0.1:8501']), 
        no_usage_report=st.booleans(),
    )
    @precondition(lambda self: self.formatted  )
    def mount(self, juicefs, no_syslog, other_fuse_options, enable_xattr, attr_cache, entry_cache, dir_entry_cache,
        get_timeout, put_timeout, io_retries, max_uploads, max_deletes, buffer_size, upload_limit, download_limit, prefetch, 
        writeback, upload_delay, cache_dir, cache_size, free_space_ratio, cache_partial_only, backup_meta, heartbeat, read_only,
        no_bgjob, open_cache, sub_dir, metrics, consul, no_usage_report):
        assume (self.is_supported_version(juicefs))
        retry = 3
        while os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog') and retry > 0:
            os.system(f'umount {JuicefsMachine.MOUNT_POINT}')
            retry = retry - 1 
            time.sleep(1)
        if os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'):
            print(f'FATAL: umount {JuicefsMachine.MOUNT_POINT} failed.')
        assume(not os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start mount')
        options = [juicefs, 'mount', '-d',  self.meta_url, JuicefsMachine.MOUNT_POINT]
        if no_syslog:
            options.append('--no-syslog')
        options.extend(['--log', os.path.expanduser(f'~/.juicefs/juicefs.log')])
        if other_fuse_options:
            options.extend(['-o', ','.join(other_fuse_options)])
        if 'allow_other' in other_fuse_options:
            if os.path.exists('/etc/fuse.conf'):
                # subprocess.check_call(['sudo', 'bash',  '-c', '"echo user_allow_other >>/etc/fuse.conf"' ])
                os.system('sudo bash -c "echo user_allow_other >>/etc/fuse.conf"')
                print('add user_allow_other to /etc/fuse.conf succeed')
        if enable_xattr:
            options.append('--enable-xattr')
        options.extend(['--attr-cache', str(attr_cache)])
        options.extend(['--entry-cache', str(entry_cache)])
        options.extend(['--dir-entry-cache', str(dir_entry_cache)])
        options.extend(['--get-timeout', str(get_timeout)])
        options.extend(['--put-timeout', str(put_timeout)])
        options.extend(['--io-retries', str(io_retries)])
        options.extend(['--max-uploads', str(max_uploads)])
        if run_cmd(f'{juicefs} mount --help | grep --max-deletes') == 0:
            options.extend(['--max-deletes', str(max_deletes)])
        options.extend(['--buffer-size', str(buffer_size)])
        options.extend(['--upload-limit', str(upload_limit)])
        options.extend(['--download-limit', str(download_limit)])
        options.extend(['--prefetch', str(prefetch)])
        if writeback:
            options.append('--writeback')
        upload_delay = str(upload_delay)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            upload_delay = upload_delay + 's'
        options.extend(['--upload-delay', str(upload_delay)])
        options.extend(['--cache-dir', os.path.expanduser(f'~/.juicefs/{cache_dir}')])
        options.extend(['--cache-size', str(cache_size)])
        options.extend(['--free-space-ratio', str(free_space_ratio)])
        if cache_partial_only:
            options.append('--cache-partial-only')
        backup_meta = str(backup_meta)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            backup_meta = backup_meta + 's'
        if run_cmd(f'{juicefs} mount --help | grep --backup-meta') == 0:
            options.extend(['--backup-meta', backup_meta])
        if run_cmd(f'{juicefs} mount --help | grep --heartbeat') == 0:
            options.extend(['--heartbeat', str(heartbeat)])
        if read_only:
            options.append('--read-only')
        if no_bgjob and run_cmd(f'{juicefs} mount --help | grep --no-bgjob') == 0:
            options.append('--no-bgjob')

        options.extend(['--open-cache', str(open_cache)])
        print('TODO: subdir')
        # options.extend('--subdir', str(sub_dir))
        options.extend(['--metrics', str(metrics)])
        if run_cmd(f'{juicefs} mount --help | grep --consul') == 0:
            options.extend(['--consul', str(consul)])
        if no_usage_report:
            options.append('--no-usage-report')
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
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        print(f'status output: {output}')
        sessions = json.loads(output.decode().replace("'", '"'))['Sessions']
        if not read_only: 
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
        # don't force umount because it may not unmounted succeed.
        # if force:
        #    options.append('--force')
        run_jfs_cmd(options)
        self.mounted = False
        print('umount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and not self.mounted)
    def destroy(self, juicefs):
        assume (self.is_supported_version(juicefs))
        assume(run_cmd(f'{juicefs} --help | grep destroy') == 0)
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

    @rule(file_name=valid_file_name, data=st.binary() )
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
        assume (self.is_supported_version(juicefs))
        print('start dump')
        run_jfs_cmd([juicefs, 'dump', self.meta_url, 'dump.json'])
        print('dump succeed')

    @rule(juicefs = st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted and os.path.exists('dump.json'))
    def load(self, juicefs):
        assume (self.is_supported_version(juicefs))
        print('start load')
        if os.path.exists(JuicefsMachine.MOUNT_POINT) and os.path.exists(JuicefsMachine.MOUNT_POINT+'.accesslog'):
            run_cmd('umount %s'%JuicefsMachine.MOUNT_POINT)
            print(f'umount {JuicefsMachine.MOUNT_POINT} succeed')
            self.mounted = False
        flush_meta(self.meta_url)
        run_jfs_cmd([juicefs, 'load', self.meta_url, 'dump.json'])
        print('load succeed')
        options = [juicefs, 'config', self.meta_url]
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-rc1'):
            # use the latest version to change secret-key because rc1 has a bug for secret-key
            options[0] = JuicefsMachine.JFS_BINS[1]
        options.extend(['--access-key', 'minioadmin', '--secret-key', 'minioadmin'])
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
        assert(os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/.accesslog'))
        print('start bench')
        run_cmd(f'df | grep {JuicefsMachine.MOUNT_POINT}')
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
                write_block(JuicefsMachine.MOUNT_POINT, f'{JuicefsMachine.MOUNT_POINT}/bigfile', 1048576, 100)
                assert os.path.exists(f'{JuicefsMachine.MOUNT_POINT}/bigfile')
                options.append(f'{JuicefsMachine.MOUNT_POINT}/bigfile')
                
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


    valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
    @rule(juicefs=st.sampled_from(JFS_BINS), 
        get_timeout=st.integers(min_value=30, max_value=59), 
        put_timeout=st.integers(min_value=30, max_value=59), 
        io_retries=st.integers(min_value=5, max_value=15), 
        max_uploads=st.integers(min_value=1, max_value=100), 
        max_deletes=st.integers(min_value=1, max_value=100), 
        buffer_size=st.integers(min_value=100, max_value=1000), 
        upload_limit=st.integers(min_value=0, max_value=1000), 
        download_limit=st.integers(min_value=0, max_value=1000), 
        prefetch=st.integers(min_value=0, max_value=100), 
        writeback=st.booleans(),
        upload_delay=st.sampled_from([0, 2]), 
        cache_dir=st.sampled_from(['cache1', 'cache2']),
        cache_size=st.integers(min_value=0, max_value=1024000), 
        free_space_ratio=st.floats(min_value=0.1, max_value=0.5), 
        cache_partial_only=st.booleans(),
        backup_meta=st.integers(min_value=30, max_value=59),
        heartbeat=st.integers(min_value=5, max_value=30), 
        read_only=st.booleans(),
        no_bgjob=st.booleans(),
        open_cache=st.integers(min_value=0, max_value=100),
        attr_cache=st.integers(min_value=1, max_value=10), 
        entry_cache=st.integers(min_value=1, max_value=10), 
        dir_entry_cache=st.integers(min_value=1, max_value=10), 
        access_log=valid_file_name,
        no_banner=st.booleans(),
        multi_buckets=st.booleans(), 
        keep_etag=st.booleans(),
        umask=st.sampled_from(['022', '755']), 
        metrics=st.sampled_from(['127.0.0.1:9567', '127.0.0.1:9568']), 
        consul=st.sampled_from(['127.0.0.1:8500', '127.0.0.1:8501']), 
        no_usage_report=st.booleans(),
        sub_dir=valid_file_name,
        port=st.integers(min_value=9001, max_value=10000)
    )
    @precondition(lambda self: self.formatted )
    def gateway(self, juicefs, get_timeout, put_timeout, io_retries, max_uploads, max_deletes, buffer_size, upload_limit, 
        download_limit, prefetch, writeback, upload_delay, cache_dir, cache_size, free_space_ratio, cache_partial_only, 
        backup_meta,heartbeat, read_only, no_bgjob, open_cache, attr_cache, entry_cache, dir_entry_cache, access_log, 
        no_banner, multi_buckets, keep_etag, umask, metrics, consul, no_usage_report, sub_dir, port):
        assume (self.is_supported_version(juicefs))
        assume(self.is_port_in_use(port))
        print('start gateway')
        os.environ['MINIO_ROOT_USER'] = 'admin'
        os.environ['MINIO_ROOT_PASSWORD'] = '12345678'
        options = [juicefs, 'gateway', self.meta_url, f'localhost:{port}']
        
        options.extend(['--attr-cache', str(attr_cache)])
        options.extend(['--entry-cache', str(entry_cache)])
        options.extend(['--dir-entry-cache', str(dir_entry_cache)])
        options.extend(['--get-timeout', str(get_timeout)])
        options.extend(['--put-timeout', str(put_timeout)])
        options.extend(['--io-retries', str(io_retries)])
        options.extend(['--max-uploads', str(max_uploads)])
        if run_cmd(f'{juicefs} gateway --help | grep --max-deletes') == 0:
            options.extend(['--max-deletes', str(max_deletes)])
        options.extend(['--buffer-size', str(buffer_size)])
        options.extend(['--upload-limit', str(upload_limit)])
        options.extend(['--download-limit', str(download_limit)])
        options.extend(['--prefetch', str(prefetch)])
        if writeback:
            options.append('--writeback')
        upload_delay = str(upload_delay)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            upload_delay = upload_delay + 's'
        options.extend(['--upload-delay', upload_delay])
        options.extend(['--cache-dir', os.path.expanduser(f'~/.juicefs/{cache_dir}')])
        options.extend(['--access-log', os.path.expanduser(f'~/.juicefs/{access_log}')])
        options.extend(['--cache-size', str(cache_size)])
        options.extend(['--free-space-ratio', str(free_space_ratio)])
        if cache_partial_only:
            options.append('--cache-partial-only')
        backup_meta = str(backup_meta)
        if version.parse('-'.join(juicefs.split('-')[1:])) <= version.parse('1.0.0-beta2'):
            backup_meta = backup_meta + 's'
        if run_cmd(f'{juicefs} gateway --help | grep --backup-meta') == 0:
            options.extend(['--backup-meta', backup_meta])
        if run_cmd(f'{juicefs} gateway --help | grep --heartbeat') == 0:
            options.extend(['--heartbeat', str(heartbeat)])
        if read_only:
            options.append('--read-only')
        if no_bgjob and run_cmd(f'{juicefs} gateway --help | grep --no-bgjob') == 0:
            options.append('--no-bgjob')
        if no_banner:
            options.append('--no-banner')
        if multi_buckets and run_cmd(f'{juicefs} gateway --help | grep --multi-buckets') == 0:
            options.append('--multi-buckets')
        if keep_etag and run_cmd(f'{juicefs} gateway --help | grep --keep-etag') == 0:
            options.append('--keep-etag')
        if run_cmd(f'{juicefs} gateway --help | grep --umask') == 0:
            options.extend(['--umask', umask])

        options.extend(['--open-cache', str(open_cache)])
        print(f'TODO: subdir:{sub_dir}')
        # options.extend('--subdir', str(sub_dir))
        options.extend(['--metrics', str(metrics)])
        options.extend(['--consul', str(consul)])
        if no_usage_report:
            options.append('--no-usage-report')

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