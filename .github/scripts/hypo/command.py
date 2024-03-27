import json
import os
import subprocess
from jsondiff import diff
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
import psutil

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
          user = st_sudo_user
          )
    @precondition(lambda self: 'info' not in self.EXCLUDE_RULES )
    def info(self, entry, raw=True, recuisive=False, user='root'):
        result1 = self.cmdop.do_info(self.context1, entry=entry, user=user, raw=raw, recuisive=recuisive) 
        result2 = self.cmdop.do_info(self.context2, entry=entry, user=user, raw=raw, recuisive=recuisive)
        assert self.equal(result1, result2), f'\033[31minfo:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          user = st_sudo_user
        )
    @precondition(lambda self: 'rmr' not in self.EXCLUDE_RULES)
    def rmr(self, entry, user='root'):
        assume(entry != '')
        result1 = self.cmdop.do_rmr(context=self.context1, entry=entry, user=user)
        result2 = self.cmdop.do_rmr(context=self.context2, entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mrmr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(user = st_sudo_user
        )
    @precondition(lambda self: 'status' not in self.EXCLUDE_RULES)
    def status(self, user='root'):
        result1 = self.cmdop.do_status(context = self.context1, user=user)
        result2 = self.cmdop.do_status(context = self.context2, user=user)
        assert result1 == result2, f'\033[31mresult1 is {result1}\nresult2 is {result2}, {diff(result1, result2)}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
        user = st_sudo_user
    )
    @precondition(lambda self: 'warmup' not in self.EXCLUDE_RULES)
    def warmup(self, entry, user='root'):
        result1 = self.cmdop.do_warmup(context=self.context1, entry=entry, user=user)
        result2 = self.cmdop.do_warmup(context=self.context2, entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mwarmup:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()),
        user = st_sudo_user
    )
    @precondition(lambda self: 'dump' not in self.EXCLUDE_RULES)
    def dump(self, entry, user='root'):
        result1 = self.cmdop.do_dump(context=self.context1, entry=entry, user=user)
        result2 = self.cmdop.do_dump(context=self.context2, entry=entry, user=user)
        assert self.equal(result1, result2), f'\033[31mdump:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        
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
    
    