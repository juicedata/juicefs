from difflib import Differ
import json
import os
import re
import subprocess
try: 
    __import__('jsondiff')
except ImportError:
    subprocess.check_call(["pip", "install", "jsondiff"])
from jsondiff import diff
try: 
    __import__('psutil')
except ImportError:
    subprocess.check_call(["pip", "install", "psutil"])
import psutil
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import HealthCheck, assume, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple
from hypothesis import Phase, seed
import random
from common import run_cmd
from strategy import *
from fs_op import FsOperation
from command_op import CommandOperation
from fs import JuicefsMachine
import common

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))

SUDO_USERS = ['root', 'user1']
st_sudo_user = st.sampled_from(SUDO_USERS)

@seed(SEED)
class JuicefsCommandMachine(JuicefsMachine):
    Files = Bundle('files')
    Folders = Bundle('folders')
    Entries = Files | Folders
    MP1 = '/tmp/jfs1'
    MP2 = '/tmp/jfs2'
    ROOT_DIR1=os.path.join(MP1, 'fsrand')
    ROOT_DIR2=os.path.join(MP2, 'fsrand')
    EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file']
    # EXCLUDE_RULES = []
    INCLUDE_RULES = ['dump_load_dump', 'mkdir', 'create_file', 'set_xattr']
    cmd1 = CommandOperation('cmd1', MP1, ROOT_DIR1)
    cmd2 = CommandOperation('cmd2', MP2, ROOT_DIR2)
    fsop1 = FsOperation('fs1', ROOT_DIR1)
    fsop2 = FsOperation('fs2', ROOT_DIR2)
    def __init__(self):
        super().__init__()
        
    def get_default_rootdir1(self):
        return os.path.join(self.MP1, 'fsrand')
    
    def get_default_rootdir2(self):
        return os.path.join(self.MP2, 'fsrand')

    def equal(self, result1, result2):
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            if 'panic:' in str(result1) or 'panic:' in str(result2):
                return False
            result1 = str(result1)
            result2 = str(result2)
        result1 = common.replace(result1, self.MP1, '***')
        result2 = common.replace(result2, self.MP2, '***')
        print(f'result1 is {result1}\nresult2 is {result2}')
        return result1 == result2

    def get_client_version(self, mount):
        output = run_cmd(f'{mount} version')
        return output.split()[2]

    def should_run(self, rule):
        if len(self.EXCLUDE_RULES) > 0:
            return rule not in self.EXCLUDE_RULES
        else:
            return rule in self.INCLUDE_RULES
        
    @rule(
          entry = Entries.filter(lambda x: x != multiple()),
          raw = st.just(True),
          recuisive = st.booleans(),
          strict = st.just(True),
          user = st_sudo_user
          )
    @precondition(lambda self: self.should_run('info'))
    def info(self, entry, raw=True, recuisive=False, strict=True, user='root'):
        result1 = self.cmd1.do_info(entry=entry, user=user, strict=strict, raw=raw, recuisive=recuisive) 
        result2 = self.cmd2.do_info(entry=entry, user=user, strict=strict, raw=raw, recuisive=recuisive)
        assert self.equal(result1, result2), f'\033[31minfo:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          user = st_sudo_user
        )
    @precondition(lambda self: self.should_run('rmr'))
    def rmr(self, entry, user='root'):
        assume(entry != '')
        result1 = self.cmd1.do_rmr(entry=entry, user=user)
        result2 = self.cmd2.do_rmr(entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mrmr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule()
    @precondition(lambda self: self.should_run('status'))
    def status(self):
        result1 = self.cmd1.do_status()
        result2 = self.cmd2.do_status()
        assert result1 == result2, f'\033[31mresult1 is {result1}\nresult2 is {result2}, {diff(result1, result2)}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
        user = st_sudo_user
    )
    @precondition(lambda self: self.should_run('warmup'))
    def warmup(self, entry, user='root'):
        result1 = self.cmd1.do_warmup(entry=entry, user=user)
        result2 = self.cmd2.do_warmup(entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mwarmup:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        compact = st.booleans(),
        delete = st.booleans(),
        user = st.just('root'),
    )
    @precondition(lambda self: self.should_run('gc'))
    def gc(self, compact=False, delete=False, user='root'):
        result1 = self.cmd1.do_gc(compact=compact, delete=delete, user=user)
        result2 = self.cmd2.do_gc(compact=compact, delete=delete, user=user)
        assert self.equal(result1, result2), f'\033[31mgc:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        entry = Entries.filter(lambda x: x != multiple()),
        repair = st.booleans(),
        recuisive = st.booleans(),
        user = st_sudo_user, 
    )
    @precondition(lambda self: self.should_run('fsck'))
    def fsck(self, entry, repair=False, recuisive=False, user='root'):
        result1 = self.cmd1.do_fsck(entry=entry, repair=repair, recuisive=recuisive, user=user)
        result2 = self.cmd2.do_fsck(entry=entry, repair=repair, recuisive=recuisive, user=user)
        assert self.equal(result1, result2), f'\033[31mfsck:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        entry = Entries.filter(lambda x: x != multiple()),
        parent = Folders.filter(lambda x: x != multiple()),
        new_entry_name = st_entry_name,
        user = st_sudo_user,
        preserve = st.booleans()
    )
    @precondition(lambda self: self.should_run('clone'))
    def clone(self, entry, parent, new_entry_name, preserve=False, user='root'):
        result1 = self.cmd1.do_clone(entry=entry, parent=parent, new_entry_name=new_entry_name, preserve=preserve, user=user)
        result2 = self.cmd2.do_clone(entry=entry, parent=parent, new_entry_name=new_entry_name, preserve=preserve, user=user)
        assert self.equal(result1, result2), f'\033[31mclone:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(folder = Folders.filter(lambda x: x != multiple()),
        fast = st.booleans(),
        skip_trash = st.booleans(),
        threads = st.integers(min_value=1, max_value=10),
        keep_secret_key = st.booleans()
    )
    @precondition(lambda self: self.should_run('dump'))
    def dump(self, folder, fast, skip_trash, threads, keep_secret_key):
        result1 = self.cmd1.do_dump(folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        result2 = self.cmd2.do_dump(folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        result1 = self.clean_dump(result1)
        result2 = self.clean_dump(result2)
        d=self.diff(result1, result2)
        assert self.equal(result1, result2), f'\033[31mdump:\nresult1 is {result1}\nresult2 is {result2}\ndiff is {d}\033[0m'

    @rule(folder = st.just(''),
        fast = st.booleans(),
        skip_trash = st.booleans(),
        threads = st.integers(min_value=1, max_value=10),
        keep_secret_key = st.booleans()
    )
    @precondition(lambda self: self.should_run('dump_load_dump'))
    def dump_load_dump(self, folder, fast=False, skip_trash=False, threads=10, keep_secret_key=False):
        result1 = self.cmd1.do_dump_load_dump(folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        result2 = self.cmd2.do_dump_load_dump(folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        print(result1)
        print(result2)
        result1 = self.clean_dump(result1)
        result2 = self.clean_dump(result2)
        d=self.diff(result1, result2)
        assert self.equal(result1, result2), f'\033[31mdump:\nresult1 is {result1}\nresult2 is {result2}\ndiff is {d}\033[0m'

    def diff(self, str1:str, str2:str):
        differ = Differ()
        diff = differ.compare(str1.splitlines(), str2.splitlines())
        return '\n'.join([line for line in diff])

    def clean_dump(self, dump):
        lines = dump.split('\n')
        new_lines = []
        exclude_keys = ['Name', 'UUID', 'usedSpace', 'usedInodes', 'nextInodes', 'nextChunk', 'nextTrash', 'nextSession']
        reset_keys = ['id', 'inode', 'atimensec', 'mtimensec', 'ctimensec', 'atime', 'ctime', 'mtime']
        for line in lines:
            should_delete = False
            for key in exclude_keys:
                if f'"{key}"' in line:
                    should_delete = True
                    break
            if should_delete:
                continue
            for key in reset_keys:
                if f'"{key}"' in line:
                    pattern = rf'"{key}":(\d+)'
                    line = re.sub(pattern, f'"{key}":0', line)
            new_lines.append(line)
        return '\n'.join(new_lines)
    

    def teardown(self):
        pass

if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    
    juicefs_machine = JuicefsCommandMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FsOperation.stats.get(), sort_keys=True, indent=4))
    
    