import os
import pwd
import subprocess
import json
import unittest
import common
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import HealthCheck, assume, example, given, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple, consumes, invariant
from hypothesis import Phase, seed
from strategy import *
from fs_op import FsOperation
import random
import time

st_patterns = st.text(alphabet='abc?/*', min_size=1, max_size=5).filter(lambda s: s.find('***') == -1 or s.endswith('/***'))
st_option = st.fixed_dictionaries({
    "option": st.just("--include") | st.just("--exclude"),
    "pattern": st_patterns
})
st_options = st.lists(st_option, min_size=1, max_size=5).filter(lambda self: any(item["pattern"].count('***') ==1 and item["pattern"].endswith('/***') for item in self))
SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
SRC = '/tmp/src_sync'
DEST_RSYNC = '/tmp/dst_rsync'
DEST_JUICESYNC = '/tmp/dst_juicesync'
class TestEncoding(unittest.TestCase):
    @seed(SEED)
    @settings(verbosity=Verbosity.verbose, 
              max_examples=10000, 
              deadline=None, 
              suppress_health_check=(HealthCheck.filter_too_much,),
              )
    @given(options=st_options)
    @example(options=[{'option':'--include' ,'pattern': 'aaa/'}, {'option':'--include', 'pattern':'aaa/a'}, {'option':'--include', 'pattern':'aaa/a/***'}, {'option':'--exclude', 'pattern':'*'}])
    def test_sync(self, options):
        subprocess.check_call(['rm', '-rf', DEST_RSYNC])
        subprocess.check_call(['rm', '-rf', DEST_JUICESYNC])
        options_str = ' '.join([f'{item["option"]} {item["pattern"]}' for item in options])
        options_str2 = ' '.join([f'{item["option"]} "{item["pattern"]}"' for item in options])
        print(f'rm -rf {DEST_RSYNC} && rsync -r -vvv {SRC}/ {DEST_RSYNC}/ {options_str2}')
        cmd = f'rsync -r -vvv {SRC}/ {DEST_RSYNC}/ {options_str}'
        subprocess.check_call(cmd.split(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        
        print(f'rm -rf {DEST_JUICESYNC} && ./juicefs sync --dirs -v {SRC}/ {DEST_JUICESYNC}/ {options_str2}')
        cmd = f'./juicefs sync --dirs -v {SRC}/ {DEST_JUICESYNC}/ {options_str}'
        subprocess.check_call(cmd.split(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        
        subprocess.check_call(['diff', '-r', DEST_RSYNC, DEST_JUICESYNC])
if __name__ == "__main__":
    unittest.main()

