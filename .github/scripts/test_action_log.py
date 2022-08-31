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

# JFS_BIN = ['./juicefs-1.0.0-beta1', './juicefs-1.0.0-beta2', './juicefs-1.0.0-beta3', './juicefs-1.0.0-rc1', './juicefs-1.0.0-rc2','./juicefs-1.0.0-rc3','./juicefs']
# JFS_BIN = [os.environ.get('OLD_JFS_BIN'), os.environ.get('NEW_JFS_BIN')]
JFS_BIN = ['./juicefs-1.0.0-rc1', './juicefs-1.1.0-dev']


class JuicefsMachine(RuleBasedStateMachine):
    MIN_CLIENT_VERSIONS = ['0.0.1', '0.0.17','1.0.0-beta1', '1.0.0-rc1']
    MAX_CLIENT_VERSIONS = ['1.1.0', '1.2.0', '2.0.0']

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
        self.formatted_by = ''
        print('\nINIT----------------------------------------------------------------------------------------\n')

    @rule(
          juicefs=st.sampled_from(JFS_BIN),
          meta_url=st.sampled_from(META_URLS),
          )
    @precondition(lambda self: not self.formatted)
    def format(self, juicefs, meta_url):
        print(f'juicefs: {juicefs}, formatted by : {self.formatted_by}')
        print('start format')
        os.system(f'{juicefs} version')
        options = [juicefs, 'format',  meta_url, JuicefsMachine.VOLUME_NAME]
        print(f'format options: {" ".join(options)}' )
        subprocess.check_call(['date'])
        self.meta_url = meta_url
        self.formatted = True
        self.formatted_by = juicefs
        print('format succeed')

    @rule(juicefs=st.sampled_from(JFS_BIN))
    @precondition(lambda self: self.formatted)
    def status(self, juicefs):
        print('start status')
        os.system(f'{juicefs} version')
        options =[juicefs, 'status', self.meta_url]
        output = subprocess.check_call(['date'])
        print('status succeed')

TestJuiceFS = JuicefsMachine.TestCase

def testLog1():
    print('start status\n')
    # os.system('./juicefs-1.0.0-dev version')
    options = ['./juicefs-1.0.0', 'status', 'redis://localhost/1']
    result = subprocess.run(['date'], check=True, capture_output=True)
    print(result.stdout.decode())

    print('status succeed1\n')
    output = subprocess.check_output(['date', '-R'])
    print(output)
    print('status succeed2\n')

def testLog2():
    print('start testLog2---------------------')
    storage_dir = os.path.expanduser('~/.juicefs/local/test')
    if os.path.exists(storage_dir):
        try:
            shutil.rmtree(storage_dir)
            print(f'remove cache dir {storage_dir} succeed')
        except OSError as e:
            print("Error: %s : %s" % (storage_dir, e.strerror))
    os.system('redis-cli flushall')
    print('start format')
    # os.system('./juicefs-1.0.0-dev version')
    options = ['./juicefs-1.0.0', 'format', 'redis://localhost/1', 'test']
    #output = subprocess.run(options, check=True, capture_output=True)
    # print(output.stdout.decode())
    output = subprocess.check_output(options)
    print(output.decode())

    print('format  succeed1')

    print('start status')
    options = ['./juicefs-1.0.0', 'status', 'redis://localhost/1']
    output = subprocess.check_output(options)
    print(output.decode())
    print('status succeed2')

if __name__ == "__main__":
    # for i in range(4):
    #    testLog1()
    for i in range(4):
        testLog2()
    # unittest.main()