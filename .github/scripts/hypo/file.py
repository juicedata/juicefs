import os
import pwd
import re
import subprocess
import json
import common
from common import red
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import assume, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple, consumes
from hypothesis import Phase, seed
from hypothesis.database import DirectoryBasedExampleDatabase
from strategy import *
from file_op import FileOperation
import random
import time

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))

@seed(SEED)
class JuicefsDataMachine(RuleBasedStateMachine):
    FILE_NAME = 'a'
    fds = Bundle('fd')
    mms = Bundle('mm')
    use_sdk = os.environ.get('USE_SDK', 'false').lower() == 'true'
    meta_url = os.environ.get('META_URL')
    INCLUDE_RULES = []
    EXCLUDE_RULES = ['seek']
    if os.environ.get('EXCLUDE_RULES'):
        EXCLUDE_RULES = os.environ.get('EXCLUDE_RULES').split(',')
    # EXCLUDE_RULES = ['readline', 'readlines', 'truncate', 'seek', 'flush']
    ROOT_DIR1=os.environ.get('ROOT_DIR1', '/tmp/fsrand')
    ROOT_DIR2=os.environ.get('ROOT_DIR2', '/tmp/jfs/fsrand')
    if use_sdk:
        fsop1 = FileOperation(name='fs1', root_dir=ROOT_DIR1, use_sdk=use_sdk, is_jfs=False, volume_name=None)
        fsop2 = FileOperation(name='fs2', root_dir=ROOT_DIR2, use_sdk=use_sdk, is_jfs=True, volume_name='test-volume', meta_url=meta_url)
    else:
        fsop1 = FileOperation(name='fs1', root_dir=ROOT_DIR1)
        fsop2 = FileOperation(name='fs2', root_dir=ROOT_DIR2)

    def __init__(self):
        super(JuicefsDataMachine, self).__init__()
        print(f'__init__')

    def equal(self, result1, result2):
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            if 'panic:' in str(result1) or 'panic:' in str(result2):
                return False
            result1 = str(result1)
            result2 = str(result2)
            if self.use_sdk:
                result1 = self.parse_error_message(result1)
                result2 = self.parse_error_message(result2)
        result1 = common.replace(result1, self.fsop1.root_dir, '***')
        result2 = common.replace(result2, self.fsop2.root_dir, '***')
        return result1 == result2

    def parse_error_message(self, err):
        # extract "[Errno 22] Invalid argument" from the following error message
        # [Errno 22] Invalid argument: '/tmp/fsrand/' -> '/tmp/fsrand/izsn/rfnn'
        # [Errno 22] Invalid argument: (b'/fsrand', b'/fsrand/izsn/rfnn', c_uint(0))
        match = re.search(r"\[Errno \d+\] [^:]+", err)
        if match:
            return match.group(0)
        else:
            return err

    def should_run(self, rule):
        if len(self.EXCLUDE_RULES) > 0:
            return rule not in self.EXCLUDE_RULES
        else:
            return rule in self.INCLUDE_RULES

    @initialize(target = fds)
    def init_folders(self):
        self.fsop1.init_rootdir()
        self.fsop2.init_rootdir()
        f1, _ = self.fsop1.do_open(file=self.FILE_NAME, mode='w+', encoding='utf8', errors='strict')
        f2, _ = self.fsop2.do_open(file=self.FILE_NAME, mode='w+', encoding='utf8', errors='strict')
        assert f1 is not None and f2 is not None, red(f'init_folders:\nf1 is {f1}\nf2 is {f2}')
        return (self.FILE_NAME, f1, f2)

    
    @rule( fd = fds.filter(lambda x: x != multiple()), 
          length = st.integers(min_value=0, max_value=MAX_FILE_SIZE))
    @precondition(lambda self: self.should_run('read'))
    def read(self, fd, length):
        result1 = self.fsop1.do_read(fd=fd[1], file=fd[0], length=length)
        result2 = self.fsop2.do_read(fd=fd[2], file=fd[0], length=length)
        assert self.equal(result1, result2), red(f'read:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
        fd = fds.filter(lambda x: x != multiple()), 
        content = st_content,
    )
    @precondition(lambda self: self.should_run('write'))
    def write(self, fd, content):
        result1 = self.fsop1.do_write(fd=fd[1], file=fd[0], content=content)
        result2 = self.fsop2.do_write(fd=fd[2], file=fd[0], content=content)
        assert self.equal(result1, result2), red(f'write:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(fd = fds.filter(lambda x: x != multiple()), 
        lines = st_lines,
    )
    @precondition(lambda self: self.should_run('writelines'))
    def writelines(self, fd, lines):
        result1 = self.fsop1.do_writelines(fd=fd[1], file=fd[0], lines=lines)
        result2 = self.fsop2.do_writelines(fd=fd[2], file=fd[0], lines=lines)
        assert self.equal(result1, result2), red(f'write:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(fd = fds.filter(lambda x: x != multiple()), 
        offset = st_offset, 
        whence = st_whence
    )
    @precondition(lambda self: self.should_run('seek'))
    def seek(self, fd, offset, whence):
        result1 = self.fsop1.do_seek(fd=fd[1], file=fd[0], offset=offset, whence=whence)
        result2 = self.fsop2.do_seek(fd=fd[2], file=fd[0], offset=offset, whence=whence)
        assert self.equal(result1, result2), red(f'seek:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(fd = fds.filter(lambda x: x != multiple()))    
    @precondition(lambda self: self.should_run('tell'))
    def tell(self, fd):
        result1 = self.fsop1.do_tell(fd=fd[1], file=fd[0])
        result2 = self.fsop2.do_tell(fd=fd[2], file=fd[0])
        assert self.equal(result1, result2), red(f'tell:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(
        target = fds,    
        fd = consumes(fds).filter(lambda x: x != multiple()))
    @precondition(lambda self: self.should_run('close'))
    def close(self, fd):
        result1 = self.fsop1.do_close(fd=fd[1], file=fd[0])
        result2 = self.fsop2.do_close(fd=fd[2], file=fd[0])
        assert self.equal(result1, result2), red(f'close:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return fd
        else:
            return multiple()
    @rule(fd = fds.filter(lambda x: x != multiple()))
    @precondition(lambda self: self.should_run('flush_and_fsync'))
    def flush_and_fsync(self, fd):
        result1 = self.fsop1.do_flush_and_fsync(fd=fd[1], file=fd[0])
        result2 = self.fsop2.do_flush_and_fsync(fd=fd[2], file=fd[0])
        assert self.equal(result1, result2), red(f'flush:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(fd = fds.filter(lambda x: x != multiple()),
          offset = st_offset,
          length = st_fallocate_length,
          )
    @precondition(lambda self: self.should_run('fallocate') and not self.use_sdk)
    def fallocate(self, fd, offset, length):
        result1 = self.fsop1.do_fallocate(fd=fd[1], file=fd[0], offset=offset, length=length)
        result2 = self.fsop2.do_fallocate(fd=fd[2], file=fd[0], offset=offset, length=length)
        assert self.equal(result1, result2), red(f'fallocate:\nresult1 is {result1}\nresult2 is {result2}')

    @rule( fd = fds.filter(lambda x: x != multiple()))
    @precondition(lambda self: self.should_run('readlines'))
    def readlines(self, fd):
        result1 = self.fsop1.do_readlines(fd=fd[1], file=fd[0])
        result2 = self.fsop2.do_readlines(fd=fd[2], file=fd[0])
        assert self.equal(result1, result2), red(f'readlines:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule( fd = fds.filter(lambda x: x != multiple()))
    @precondition(lambda self: self.should_run('readline'))
    def readline(self, fd):
        result1 = self.fsop1.do_readline(fd=fd[1], file=fd[0])
        result2 = self.fsop2.do_readline(fd=fd[2], file=fd[0])
        assert self.equal(result1, result2), red(f'readline:\nresult1 is {result1}\nresult2 is {result2}')
    

    @rule(fd=fds.filter(lambda x: x != multiple()), 
          size=st_truncate_length, 
          )
    @precondition(lambda self: self.should_run('truncate'))
    def truncate(self, fd, size):
        result1 = self.fsop1.do_truncate(fd=fd[1], file=fd[0], size=size)
        result2 = self.fsop2.do_truncate(fd=fd[2], file=fd[0], size=size)
        assert self.equal(result1, result2), red(f'truncate:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
        src=fds.filter(lambda x: x != multiple()),
        dst=fds.filter(lambda x: x != multiple()),
        src_offset = st_offset,
        dst_offset = st_offset,
        length = st_length,
        )
    @precondition(lambda self: self.should_run('copy_file_range') and not self.use_sdk)
    def copy_file_range(self, src, dst, src_offset, dst_offset, length):
        result1 = self.fsop1.do_copy_file_range(src_file=src[0], dst_file=dst[0], src_fd=src[1], dst_fd=dst[1], src_offset=src_offset, dst_offset=dst_offset, length=length)
        result2 = self.fsop2.do_copy_file_range(src_file=src[0], dst_file=dst[0], src_fd=src[2], dst_fd=dst[2], src_offset=src_offset, dst_offset=dst_offset, length=length)
        assert self.equal(result1, result2), red(f'copy_file_range:\nresult1 is {result1}\nresult2 is {result2}')

    def teardown(self):
        pass
        
if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    ci_db = DirectoryBasedExampleDatabase(".hypothesis/examples")    
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("schedule", max_examples=1000, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=200, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    settings.register_profile("pull_request", max_examples=100, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=50, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target], 
        database=ci_db)
    settings.register_profile("generate", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, \
        phases=[Phase.generate, Phase.target])
    
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
    juicefs_machine = JuicefsDataMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FileOperation.stats.get(), sort_keys=True, indent=4))