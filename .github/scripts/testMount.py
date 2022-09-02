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
from utils import clear_cache, clear_storage, flush_meta, run_jfs_cmd

class JuicefsMachine(RuleBasedStateMachine):
    JFS_BINS = ['./'+os.environ.get('OLD_JFS_BIN'), './'+os.environ.get('NEW_JFS_BIN')]
    META_URLS = [os.environ.get('META_URL')]
    STORAGES = [os.environ.get('STORAGE')]
    MOUNT_POINT = '/tmp/sync-test/'
    VOLUME_NAME = 'test-volume'

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print('\n__init__')
        self.formatted = False
        self.mounted = False
        self.meta_url = ''
        self.formatted_by = ''
        os.system(f'mc alias set myminio http://localhost:9000 minioadmin minioadmin')
        os.system("for pid in $(ps -ef | awk '/juicefs/ {print $2}'); do kill -9  $pid; done")

    @rule(
          juicefs=st.sampled_from(JFS_BINS),
          storage=st.sampled_from(STORAGES), 
          meta_url=st.sampled_from(META_URLS),
          )
    def format(self, juicefs, storage, meta_url):
        print('start format')
        options = [juicefs, 'format',  meta_url, JuicefsMachine.VOLUME_NAME]
        if not self.formatted:
            options.extend(['--storage', storage])
        
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
    
    valid_file_name = st.text(st.characters(max_codepoint=1000, blacklist_categories=('Cc', 'Cs')), min_size=2).map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
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
        upload_delay=st.integers(min_value=0, max_value=60), 
        cache_dir=valid_file_name,
        cache_size=st.integers(min_value=0, max_value=1024000), 
        free_space_ratio=st.floats(min_value=0.1, max_value=0.5), 
        cache_partial_only=st.booleans(),
        backup_meta=st.integers(min_value=300, max_value=3600),
        heartbeat=st.integers(min_value=1, max_value=12), 
        read_only=st.booleans(),
        no_bgjob=st.booleans(),
        open_cache=st.integers(min_value=0, max_value=100),
        sub_dir=valid_file_name,
        metrics=st.sampled_from(['127.0.0.1:9567', '127.0.0.1:9568']), 
        consul=st.sampled_from(['127.0.0.1:8500', '127.0.0.1:8501']), 
        no_usage_report=st.booleans(),
    )
    @precondition(lambda self: self.formatted )
    def mount(self, juicefs, no_syslog, other_fuse_options, enable_xattr, attr_cache, entry_cache, dir_entry_cache,
        get_timeout, put_timeout, io_retries, max_uploads, max_deletes, buffer_size, upload_limit, download_limit, prefetch, 
        writeback, upload_delay, cache_dir, cache_size, free_space_ratio, cache_partial_only, backup_meta, heartbeat, read_only,
        no_bgjob, open_cache, sub_dir, metrics, consul, no_usage_report):
        print('start mount')
        options = [juicefs, 'mount', '-d',  self.meta_url, JuicefsMachine.MOUNT_POINT]
        if no_syslog:
            options.append('--no-syslog')
        options.extend(['--log', os.path.expanduser(f'~/.juicefs/juicefs.log')])
        if other_fuse_options:
            options.extend(['-o', ','.join(other_fuse_options)])
        if enable_xattr:
            options.append('--enable-xattr')
        options.extend(['--attr-cache', str(attr_cache)])
        options.extend(['--entry-cache', str(entry_cache)])
        options.extend(['--dir-entry-cache', str(dir_entry_cache)])
        options.extend(['--get-timeout', str(get_timeout)])
        options.extend(['--put-timeout', str(put_timeout)])
        options.extend(['--io-retries', str(io_retries)])
        options.extend(['--max-uploads', str(max_uploads)])
        options.extend(['--max-deletes', str(max_deletes)])
        options.extend(['--buffer-size', str(buffer_size)])
        options.extend(['--upload-limit', str(upload_limit)])
        options.extend(['--download-limit', str(download_limit)])
        options.extend(['--prefetch', str(prefetch)])
        if writeback:
            options.append('--writeback')
        options.extend(['--upload-delay', str(upload_delay)])
        options.extend(['--cache-dir', os.path.expanduser(f'~/.juicefs/{cache_dir}')])
        options.extend(['--cache-size', str(cache_size)])
        options.extend(['--free-space-ratio', str(free_space_ratio)])
        if cache_partial_only:
            options.append('--cache-partial-only')
        options.extend(['--backup-meta', str(backup_meta)])
        options.extend(['--heartbeat', str(heartbeat)])
        if read_only:
            options.append('--read-only')
        if no_bgjob:
            options.append('--no-bgjob')

        options.extend(['--open-cache', str(open_cache)])
        print('TODO: subdir')
        # options.extend('--subdir', str(sub_dir))
        options.extend(['--metrics', str(metrics)])
        options.extend(['--consul', str(consul)])
        if no_usage_report:
            options.append('--no-usage-report')
        run_jfs_cmd(options)
        time.sleep(2)
        output = subprocess.check_output([juicefs, 'status', self.meta_url])
        print(f'status output: {output}')
        sessions = json.loads(output.decode().replace("'", '"'))['Sessions']
        assert len(sessions) != 0 
        self.mounted = True
        print('mount succeed')

    @rule(juicefs=st.sampled_from(JFS_BINS), 
    force=st.booleans())
    @precondition(lambda self: self.mounted)
    def umount(self, juicefs, force):
        print('start umount')
        options = [juicefs, 'umount', JuicefsMachine.MOUNT_POINT]
        if force:
            options.append('--force')
        run_jfs_cmd(options)
        self.mounted = False
        print('umount succeed')

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

    @rule(juicefs=st.sampled_from(JFS_BINS))
    @precondition(lambda self: self.formatted)
    def fsck(self, juicefs):
        print('start fsck')
        run_jfs_cmd([juicefs, 'fsck', self.meta_url])
        print('fsck succeed')


TestJuiceFS = JuicefsMachine.TestCase

if __name__ == "__main__":
    unittest.main()