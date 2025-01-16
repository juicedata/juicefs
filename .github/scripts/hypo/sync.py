import os
import subprocess
import json
import common
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import assume, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple, consumes, invariant
from hypothesis import Phase, seed
from strategy import *
from fs_op import FsOperation
import random

st_entry_name = st.text(alphabet='abc*?', min_size=1, max_size=4)
st_patterns = st.lists(st.sampled_from(['a','?','/','*']), min_size=1, max_size=10)\
    .map(''.join).filter(lambda s: s.find('***') == -1 or (s.count('***') == 1 and s.endswith('/***')))

st_option = st.fixed_dictionaries({
    "option": st.just("--include") | st.just("--exclude"),
    "pattern": st_patterns
})

st_options = st.lists(st_option, min_size=1, max_size=10)

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))
@seed(SEED)
class SyncMachine(RuleBasedStateMachine):
    Files = Bundle('files')
    Folders = Bundle('folders')
    ROOT_DIR1 = '/tmp/sync_src'
    ROOT_DIR2 = '/tmp/sync_src2'
    DEST_RSYNC = '/tmp/rsync'
    DEST_JUICESYNC = '/tmp/juicesync'
    
    fsop1 = FsOperation('fs1', ROOT_DIR1)
    fsop2 = FsOperation('fs2', ROOT_DIR2)
    
    @initialize(target=Folders)
    def init_folders(self):
        if not os.path.exists(self.ROOT_DIR1):
            os.makedirs(self.ROOT_DIR1)
        if not os.path.exists(self.ROOT_DIR2):
            os.makedirs(self.ROOT_DIR2)
        common.clean_dir(self.ROOT_DIR1)
        common.clean_dir(self.ROOT_DIR2)
        return ''
    
    def __init__(self):
        super(SyncMachine, self).__init__()
        
    def equal(self, result1, result2):
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            r1 = str(result1).replace(self.ROOT_DIR1, '')
            r2 = str(result2).replace(self.ROOT_DIR2, '')
            return r1 == r2
        elif isinstance(result1, tuple):
            return result1 == result2
        elif isinstance(result1, str):
            r1 = str(result1).replace(self.ROOT_DIR1, '')
            r2 = str(result2).replace(self.ROOT_DIR2, '')
            return  r1 == r2
        else:
            return result1 == result2

    @rule(target=Files, 
          parent = Folders.filter(lambda x: x != multiple()), 
          file_name = st_entry_name, 
          umask = st_umask, 
            )
    def create_file(self, parent, file_name, content='s', mode='x', user='root', umask=0o022):
        result1 = self.fsop1.do_create_file(parent=parent, file_name=file_name, mode=mode, content=content, user=user, umask=umask)
        result2 = self.fsop2.do_create_file(parent=parent, file_name=file_name, mode=mode, content=content, user=user, umask=umask)
        assert self.equal(result1, result2), f'\033[31mcreate_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, file_name)
    
    @rule( target = Folders, 
          parent = Folders.filter(lambda x: x != multiple()),
          subdir = st_entry_name,
          mode = st_entry_mode,
          umask = st_umask, 
          )
    def mkdir(self, parent, subdir, mode, user='root', umask=0o022):
        result1 = self.fsop1.do_mkdir(parent, subdir, mode, user, umask)
        result2 = self.fsop2.do_mkdir(parent, subdir, mode, user, umask)
        assert self.equal(result1, result2), f'\033[31mmkdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, subdir)

    @rule(options = st_options
        )
    def sync(self, options):
        subprocess.check_call(['rm', '-rf', self.DEST_RSYNC])
        subprocess.check_call(['rm', '-rf', self.DEST_JUICESYNC])
        options_run = ' '.join([f'{item["option"]} {item["pattern"]}' for item in options])
        options_display = ' '.join([f'{item["option"]} "{item["pattern"]}"' for item in options])
        print(f'rsync -r -vvv {self.ROOT_DIR1}/ {self.DEST_RSYNC}/ {options_display}')
        subprocess.check_call(f'rsync -r -vvv {self.ROOT_DIR1}/ {self.DEST_RSYNC}/ {options_run}'.split(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        print(f'./juicefs sync --dirs -v {self.ROOT_DIR1}/ {self.DEST_JUICESYNC}/ {options_display}')
        subprocess.check_call(f'./juicefs sync --dirs -v {self.ROOT_DIR1}/ {self.DEST_JUICESYNC}/ {options_run}'.split(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            subprocess.check_call(['diff', '-r', self.DEST_RSYNC, self.DEST_JUICESYNC])
        except subprocess.CalledProcessError as e:
            print(f'\033[31m{e}\033[0m')
            raise e
        self.fsop1.stats.success('do_sync')
        self.fsop2.stats.success('do_sync')

    def teardown(self):
        pass

if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '1000'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("ci", max_examples=MAX_EXAMPLE, verbosity=Verbosity.normal, 
        print_blob=False, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    juicefs_machine = SyncMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FsOperation.stats.get(), sort_keys=True, indent=4))
