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
import common
from common import run_cmd
from strategy import *
from fs_op import FsOperation
from command_op import CommandOperation
from fsrand2 import JuicefsMachine
from context import Context


SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))

SUDO_USERS = ['root', 'user1']
st_sudo_user = st.sampled_from(SUDO_USERS)

@seed(SEED)
class JuicefsCommandMachine(JuicefsMachine):
    Files = Bundle('files')
    Folders = Bundle('folders')
    Entries = Files | Folders
    ROOT_DIR1='/tmp/jfs1/fsrand'
    ROOT_DIR2='/tmp/jfs2/fsrand'
    
    context1 = Context(root_dir=ROOT_DIR1, mp='/tmp/jfs1')
    context2 = Context(root_dir=ROOT_DIR2, mp='/tmp/jfs2')
    
    EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file']
    # EXCLUDE_RULES = []
    INCLUDE_RULES = ['dump_load_dump', 'mkdir', 'create_file', 'set_xattr']
    log_level = os.environ.get('LOG_LEVEL', 'INFO')
    loggers = {f'{ROOT_DIR1}': common.setup_logger(f'./log1', 'cmdlogger1', log_level), \
                            f'{ROOT_DIR2}': common.setup_logger(f'./log2', 'cmdlogger2', log_level)}
    fsop = FsOperation(loggers)
    cmdop = CommandOperation(loggers)

    def get_meta_url(self, mp):
        with open(os.path.join(mp, '.config')) as f:
            config = json.loads(f.read())
            pid = config['Pid']
            process = psutil.Process(pid)
            cmdline = process.cmdline()
            for item in cmdline:
                if '://' in item:
                    return item
            raise Exception(f'get_meta_url: {cmdline} does not contain meta url')

    def equal(self, result1, result2):
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            if 'panic:' in str(result1) or 'panic:' in str(result2):
                return False
            r1 = str(result1).replace(self.context1.mp, '')
            r2 = str(result2).replace(self.context2.mp, '')
            return r1 == r2
        elif isinstance(result1, str):
            r1 = str(result1).replace(self.context1.mp, '')
            r2 = str(result2).replace(self.context2.mp, '')
            return r1 == r2
        elif isinstance(result1, tuple):
            r1 = [str(item).replace(self.context1.mp, '') for item in result1]
            r2 = [str(item).replace(self.context2.mp, '') for item in result2]
            return r1 == r2
        else:
            return  result1 == result2

    def get_client_version(self, mount):
        output = run_cmd(f'{mount} version')
        return output.split()[2]

    def __init__(self):
        super().__init__()
        self.context1.meta_url = self.get_meta_url(self.context1.mp)
        self.context2.meta_url = self.get_meta_url(self.context2.mp)
        assert self.context1.meta_url != ''
        assert self.context2.meta_url != ''
        
    @rule(
          entry = Entries.filter(lambda x: x != multiple()),
          raw = st.just(True),
          recuisive = st.booleans(),
          strict = st.just(True),
          user = st_sudo_user
          )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'info' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'info' in self.INCLUDE_RULES)
    )
    def info(self, entry, raw=True, recuisive=False, strict=True, user='root'):
        result1 = self.cmdop.do_info(self.context1, entry=entry, user=user, raw=raw, recuisive=recuisive) 
        result2 = self.cmdop.do_info(self.context2, entry=entry, user=user, raw=raw, recuisive=recuisive)
        assert self.equal(result1, result2), f'\033[31minfo:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          user = st_sudo_user
        )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rmr' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rmr' in self.INCLUDE_RULES)
    )
    def rmr(self, entry, user='root'):
        assume(entry != '')
        result1 = self.cmdop.do_rmr(context=self.context1, entry=entry, user=user)
        result2 = self.cmdop.do_rmr(context=self.context2, entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mrmr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule()
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'status' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'status' in self.INCLUDE_RULES)
    )
    def status(self):
        result1 = self.cmdop.do_status(context = self.context1)
        result2 = self.cmdop.do_status(context = self.context2)
        assert result1 == result2, f'\033[31mresult1 is {result1}\nresult2 is {result2}, {diff(result1, result2)}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
        user = st_sudo_user
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'warmup' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'warmup' in self.INCLUDE_RULES)
    )
    def warmup(self, entry, user='root'):
        result1 = self.cmdop.do_warmup(context=self.context1, entry=entry, user=user)
        result2 = self.cmdop.do_warmup(context=self.context2, entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mwarmup:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        compact = st.booleans(),
        delete = st.booleans(),
        user = st.just('root'),
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'gc' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'gc' in self.INCLUDE_RULES)
    )
    def gc(self, compact=False, delete=False, user='root'):
        result1 = self.cmdop.do_gc(context=self.context1, compact=compact, delete=delete, user=user)
        result2 = self.cmdop.do_gc(context=self.context2, compact=compact, delete=delete, user=user)
        assert self.equal(result1, result2), f'\033[31mgc:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        entry = Entries.filter(lambda x: x != multiple()),
        repair = st.booleans(),
        recuisive = st.booleans(),
        user = st_sudo_user, 
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'fsck' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'fsck' in self.INCLUDE_RULES)
    )
    def fsck(self, entry, repair=False, recuisive=False, user='root'):
        result1 = self.cmdop.do_fsck(context=self.context1, entry=entry, repair=repair, recuisive=recuisive, user=user)
        result2 = self.cmdop.do_fsck(context=self.context2, entry=entry, repair=repair, recuisive=recuisive, user=user)
        assert self.equal(result1, result2), f'\033[31mfsck:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
        entry = Entries.filter(lambda x: x != multiple()),
        parent = Folders.filter(lambda x: x != multiple()),
        new_entry_name = st_entry_name,
        user = st_sudo_user,
        preserve = st.booleans()
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'clone' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'clone' in self.INCLUDE_RULES)
    )
    def clone(self, entry, parent, new_entry_name, preserve=False, user='root'):
        result1 = self.cmdop.do_clone(context=self.context1, entry=entry, parent=parent, new_entry_name=new_entry_name, preserve=preserve, user=user)
        result2 = self.cmdop.do_clone(context=self.context2, entry=entry, parent=parent, new_entry_name=new_entry_name, preserve=preserve, user=user)
        assert self.equal(result1, result2), f'\033[31mclone:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(folder = Folders.filter(lambda x: x != multiple()),
        fast = st.booleans(),
        skip_trash = st.booleans(),
        threads = st.integers(min_value=1, max_value=10),
        keep_secret_key = st.booleans()
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'dump' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'dump' in self.INCLUDE_RULES)
    )
    def dump(self, folder, fast, skip_trash, threads, keep_secret_key):
        result1 = self.cmdop.do_dump(context=self.context1, folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        result2 = self.cmdop.do_dump(context=self.context2, folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
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
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'dump_load_dump' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'dump_load_dump' in self.INCLUDE_RULES)
    )
    def dump_load_dump(self, folder, fast=False, skip_trash=False, threads=10, keep_secret_key=False):
        result1 = self.cmdop.do_dump_load_dump(context=self.context1, folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        result2 = self.cmdop.do_dump_load_dump(context=self.context2, folder=folder, fast=fast, skip_trash=skip_trash, threads=threads, keep_secret_key=keep_secret_key)
        print(result1)
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
    
    