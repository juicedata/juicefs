import hashlib
import os
import pwd
import shutil
from string import ascii_lowercase
import subprocess
import logging
import json
import stat
import sys
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
import fallocate
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
try:
    __import__("hypothesis")
except ImportError:
    subprocess.check_call(["pip", "install", "hypothesis"])
from hypothesis import assume, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize
from hypothesis import Phase, seed
import random
import time

EXCLUDE_RULES = ['mkfifo', 'copy_tree']
COMPARE = os.environ.get('COMPARE', 'true') == 'true'
CLEAN_DIR = os.environ.get('CLEAN_DIR', 'true') == 'true'
MAX_RUNTIME=int(os.environ.get('MAX_RUNTIME', '36000'))
MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
DERANDOMIZE=os.environ.get('DERANDOMIZE', 'false') == 'true'
print(f'MAX_EXAMPLE: {MAX_EXAMPLE}, STEP_COUNT: {STEP_COUNT}, DERANDOMIZE: {DERANDOMIZE}, MAX_RUNTIME: {MAX_RUNTIME}, COMPARE: {COMPARE}, CLEAN_DIR: {CLEAN_DIR}')
INVALID_FILE = 'invalid_file'
INVALID_DIR = 'invalid_dir'
JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
MAX_CODEPOINT=255
MIN_FILE_NAME=4
MAX_FILE_NAME=4
MAX_XATTR_NAME=255
MAX_XATTR_VALUE=65535
MAX_FILE_SIZE=1024*10
MAX_TRUNCATE_LENGTH=1024*128
MAX_FALLOCATE_LENGTH=1024*128
st_entry_name = st.text(alphabet=ascii_lowercase, min_size=MIN_FILE_NAME, max_size=MAX_FILE_NAME)
st_content = st.binary(min_size=0, max_size=MAX_FILE_SIZE)
st_xattr_name = st.text(st.characters(), min_size=1, max_size=MAX_XATTR_NAME) #.map(lambda s: s.strip()).filter(lambda s: len(s) > 0)
st_xattr_value = st.binary(min_size=1, max_size=MAX_XATTR_VALUE)
st_file_mode = st.integers(min_value=0o000, max_value=0o777)
st_open_flags = st.lists(st.sampled_from([os.O_RDONLY, os.O_WRONLY, os.O_RDWR, os.O_APPEND, os.O_CREAT, os.O_EXCL, os.O_TRUNC, os.O_SYNC, os.O_DSYNC, os.O_RSYNC]), unique=True, min_size=1)
st_time=st.integers(min_value=0, max_value=int(time.time()))
st_offset=st.integers(min_value=0, max_value=MAX_FILE_SIZE)

ROOT_DIR1=os.environ.get('ROOT_DIR1', '/tmp/fsrand').rstrip('/')
ROOT_DIR2=os.environ.get('ROOT_DIR2', '/myjfs/fsrand').rstrip('/')

def clean_dir(dir):
    subprocess.check_call(f'rm -rf {dir}'.split())
    assert not os.path.exists(dir), f'clean_dir: {dir} should not exist'
    subprocess.check_call(f'mkdir -p {dir}'.split())
    assert os.path.isdir(dir), f'clean_dir: {dir} should be dir'

clean_dir(ROOT_DIR1)
clean_dir(ROOT_DIR2)

os.system('id -u juicedata1  && userdel juicedata1')
os.system('id -u juicedata2  && userdel juicedata2')
subprocess.check_call('useradd juicedata1'.split())
subprocess.check_call('useradd juicedata2'.split())
USERNAMES=['root', 'juicedata1', 'juicedata2']

def get_stat(path):
    if os.path.isfile(path):
        stat = os.stat(path)
        print(f'{path} is file: {stat}')
        return stat.st_gid, stat.st_uid,  stat.st_size, oct(stat.st_mode), stat.st_nlink
    elif os.path.isdir(path):
        stat = os.stat(path)
        print(f'{path} is dir: {stat}')
        return stat.st_gid, stat.st_uid, oct(stat.st_mode), stat.st_nlink
    elif os.path.islink(path) and os.path.exists(path): # good link
        stat = os.stat(path)
        lstat = os.lstat(path)
        print(f'{path} is good link: {stat}\n{lstat}')
        return stat.st_gid, stat.st_uid,  stat.st_size, oct(stat.st_mode), stat.st_nlink, \
            lstat.st_gid, lstat.st_uid, oct(lstat.st_mode), 
    elif os.path.islink(path) and not os.path.exists(path): # broken link
        lstat = os.lstat(path)
        print(f'{path} is broken link: {lstat}')
        return lstat.st_gid, lstat.st_uid, oct(lstat.st_mode), lstat.st_nlink
    else:
        return ()

def setup_logger(log_file_path, logger_name):
    # Create a logger object
    assert os.path.exists(os.path.dirname(log_file_path)), f'setup_logger: {log_file_path} should exist'
    print(f'setup_logger {log_file_path}')
    logger = logging.getLogger(logger_name)
    logger.setLevel(logging.DEBUG)
    # Create a file handler for the logger
    file_handler = logging.FileHandler(log_file_path)
    file_handler.setLevel(logging.DEBUG)
    # Create a stream handler for the logger
    stream_handler = logging.StreamHandler()
    stream_handler.setLevel(logging.DEBUG)
    # Create a formatter for the log messages
    formatter = logging.Formatter('%(asctime)s - %(levelname)s - %(message)s')
    file_handler.setFormatter(formatter)
    stream_handler.setFormatter(formatter)
    # Add the file and stream handlers to the logger
    logger.addHandler(file_handler)
    logger.addHandler(stream_handler)
    return logger
loggers = {f'{ROOT_DIR1}': setup_logger(f'./log1', 'logger1'), \
           f'{ROOT_DIR2}': setup_logger(f'./log2', 'logger2')}

class Statistics:
    def __init__(self):
        self.stats = {}
    def success(self, function_name):
        if function_name not in self.stats:
            self.stats[function_name] = {'success': 0, 'failure': 0}
        self.stats[function_name]['success'] += 1
    def failure(self, function_name):
        if function_name not in self.stats:
            self.stats[function_name] = {'success': 0, 'failure': 0}
        self.stats[function_name]['failure'] += 1

    def get(self):
        return self.stats

@seed(random.randint(10000, 1000000))
@settings(verbosity=Verbosity.debug, 
    max_examples=MAX_EXAMPLE, 
    stateful_step_count=STEP_COUNT, 
    derandomize = DERANDOMIZE,
    deadline=None, 
    report_multiple_bugs=False, 
    phases=[Phase.explicit, Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain ])
class JuicefsMachine(RuleBasedStateMachine):
    Files = Bundle('files')
    Folders = Bundle('folders')
    FilesWithXattr = Bundle('files_with_xattr')
    stats = Statistics()
    start = time.time()
    
    @initialize(target=Folders)
    def init_folders(self):
        print('init_folders')
        # self.init_folder(ROOT_DIR1)
        # self.init_folder(ROOT_DIR2)
        return ""
    
    def init_folder(self, dir):
        for i in range(1, 1200):
            file = os.path.join(dir, f'file{i}')
            with open(file, 'x') as f:
                f.write(str(i))

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print(f'__init__')
        duration = time.time() - self.start
        if duration > MAX_RUNTIME:
            raise Exception(f'run out of time: {duration}')
        if CLEAN_DIR:
            clean_dir(ROOT_DIR1)
            clean_dir(ROOT_DIR2)

    def equal(self, result1, result2):
        if not COMPARE:
            return True
        if isinstance(result1, tuple):
            return result1 == result2
        elif isinstance(result1, str):
            r1 = result1.replace(ROOT_DIR1, '')
            r2 = str(result2).replace(ROOT_DIR2, '')
            return  r1 == r2
        return False

    @rule(target=Files, parent = Folders, file_name = st_entry_name, flags=st_open_flags, mode=st_file_mode)
    @precondition(lambda self: 'open_file' not in EXCLUDE_RULES)
    def open_file(self, parent, file_name, flags, mode):
        result1 = self.do_open_file(ROOT_DIR1, parent, file_name, flags, mode)
        result2 = self.do_open_file(ROOT_DIR2, parent, file_name, flags, mode)
        assert self.equal(result1, result2), f'open_file:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, file_name)
        else:
            return INVALID_FILE
    
    def do_open_file(self, root_dir, parent, file_name, flags, mode):
        loggers[f'{root_dir}'].debug(f'do_open_file {root_dir} {parent} {file_name} {flags} {mode}')
        abspath = os.path.join(root_dir, parent, file_name)
        flag = 0
        for f in flags:
            flag |= f
        try:
            os.open(abspath, flags=flag, mode=mode)
        except Exception as e :
            self.stats.failure('do_open_file')
            loggers[f'{root_dir}'].info(f'do_open_file {abspath} {flags} {mode} failed: {str(e)}')
            return str(e)
        assert os.path.isfile(abspath), f'do_open_file: {abspath} should be file'
        self.stats.success('do_open_file')
        loggers[f'{root_dir}'].info(f'do_open_file {abspath} {flags} {mode} succeed')
        return get_stat(abspath)  

    @rule(file=Files, offset=st_offset, content = st_content)
    @precondition(lambda self: 'write' not in EXCLUDE_RULES)
    def write(self, file, offset, content):
        result1 = self.do_write(ROOT_DIR1, file, offset, content)
        result2 = self.do_write(ROOT_DIR2, file, offset, content)
        assert self.equal(result1, result2), f'write:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_write(self, root_dir, file, offset, content):
        loggers[f'{root_dir}'].debug(f'do_write {root_dir} {file} {offset}')
        abspath = os.path.join(root_dir, file)
        try:
            file_fd = os.open(abspath, os.O_RDWR)
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            os.lseek(file_fd, offset, os.SEEK_SET)
            os.write(file_fd, content)
            os.close(file_fd)
        except Exception as e :
            self.stats.failure('do_write')
            loggers[f'{root_dir}'].info(f'do_write {abspath} {offset} failed: {str(e)}')
            return str(e)
        self.stats.success('do_write')
        loggers[f'{root_dir}'].info(f'do_write {abspath} {offset} succeed')
        return get_stat(abspath)

    @rule(file = Files, 
          offset = st.integers(min_value=0, max_value=MAX_FILE_SIZE),
          length = st.integers(min_value=0, max_value=MAX_FALLOCATE_LENGTH),
          mode = st.just(0))
    @precondition(lambda self: 'fallocate' not in EXCLUDE_RULES)
    def fallocate(self, file, offset, length, mode):
        result1 = self.do_fallocate(ROOT_DIR1, file, offset, length, mode)
        result2 = self.do_fallocate(ROOT_DIR2, file, offset, length, mode)
        assert self.equal(result1, result2), f'fallocate:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_fallocate(self, root_dir, file, offset, length, mode):
        loggers[f'{root_dir}'].debug(f'do_fallocate {root_dir} {file} {offset} {length} {mode}')
        abspath = os.path.join(root_dir, file)
        try:
            fd = os.open(abspath, os.O_RDWR)
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            fallocate.fallocate(fd, offset, length, mode)
            os.close(fd)
        except Exception as e :
            self.stats.failure('do_fallocate')
            loggers[f'{root_dir}'].info(f'do_fallocate {abspath} {offset} {length} {mode} failed: {str(e)}')
            return str(e)
        self.stats.success('do_fallocate')
        loggers[f'{root_dir}'].info(f'do_fallocate {abspath} {offset} {length} {mode} succeed')
        return get_stat(abspath)

    @rule( file=Files, pos=st_offset, length=st.integers(min_value=0, max_value=MAX_FILE_SIZE))
    @precondition(lambda self: 'read' not in EXCLUDE_RULES)
    def read(self, file, pos, length):
        result1 = self.do_read(ROOT_DIR1, file, pos, length)
        result2 = self.do_read(ROOT_DIR2, file, pos, length)
        assert self.equal(result1, result2), f'read:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_read(self, root_dir, file, pos, length):
        loggers[f'{root_dir}'].debug(f'do_read {root_dir} {file} {pos} {length}')
        abspath = os.path.join(root_dir, file)
        try:
            fd = os.open(abspath, os.O_RDONLY)
            size = os.stat(abspath).st_size
            pos = pos % size
            os.lseek(fd, pos, os.SEEK_SET)
            result = os.read(fd, length)
            md5sum = hashlib.md5(result).hexdigest()
            os.close(fd)
        except Exception as e :
            self.stats.failure('do_read')
            loggers[f'{root_dir}'].info(f'do_read {abspath} {pos} {length} failed: {str(e)}')
            return str(e)
        self.stats.success('do_read')
        loggers[f'{root_dir}'].info(f'do_read {abspath} {pos} {length} succeed')
        return (md5sum, )

    @rule(file=Files, size=st.integers(min_value=0, max_value=MAX_TRUNCATE_LENGTH))
    @precondition(lambda self: 'truncate' not in EXCLUDE_RULES)
    def truncate(self, file, size):
        result1 = self.do_truncate(ROOT_DIR1, file, size)
        result2 = self.do_truncate(ROOT_DIR2, file, size)
        assert self.equal(result1, result2), f'truncate:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_truncate(self, root_dir, file, size):
        loggers[f'{root_dir}'].debug(f'do_truncate {root_dir} {file} {size}')
        abspath = os.path.join(root_dir, file)
        try:
            fd = os.open(abspath, os.O_WRONLY | os.O_TRUNC)
            os.ftruncate(fd, size)
            os.close(fd)
        except Exception as e :
            self.stats.failure('do_truncate')
            loggers[f'{root_dir}'].info(f'do_truncate {abspath} {size} failed: {str(e)}')
            return str(e)
        self.stats.success('do_truncate')
        loggers[f'{root_dir}'].info(f'do_truncate {abspath} {size} succeed')
        return get_stat(abspath)

    @rule(target=Files, parent = Folders, file_name = st_entry_name, content = st_content)
    @precondition(lambda self: 'create_file' not in EXCLUDE_RULES)
    def create_file(self, parent, file_name, content):
        result1 = self.do_create_file(ROOT_DIR1, parent, file_name, content)
        result2 = self.do_create_file(ROOT_DIR2, parent, file_name, content)
        assert self.equal(result1, result2), f'create_file:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, file_name)
        else:
            return INVALID_FILE
    
    def do_create_file(self, root_dir, parent, file_name, content):
        relpath = os.path.join(parent, file_name)
        abspath = os.path.join(root_dir, relpath)
        try:
            with open(abspath, 'x') as file:
                file.write(str(content))
        except Exception as e :
            self.stats.failure('do_create_file')
            loggers[f'{root_dir}'].info(f'do_create_file {abspath} failed: {str(e)}')
            return str(e)
        assert os.path.isfile(abspath), f'do_create_file: {abspath} should be file'
        self.stats.success('do_create_file')
        loggers[f'{root_dir}'].info(f'do_create_file {abspath} succeed')
        return get_stat(abspath)
    
    @rule(target=Files, parent = Folders, file_name = st_entry_name, mode = st_file_mode)
    @precondition(lambda self: 'mkfifo' not in EXCLUDE_RULES)
    def mkfifo(self, parent, file_name, mode):
        result1 = self.do_mkfifo(ROOT_DIR1, parent, file_name, mode)
        result2 = self.do_mkfifo(ROOT_DIR2, parent, file_name, mode)
        assert self.equal(result1, result2), f'mkfifo:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, file_name)
        else:
            return INVALID_FILE
    
    def do_mkfifo(self, root_dir, parent, file_name, mode):
        abspath = os.path.join(root_dir, parent, file_name)
        try:
            os.mkfifo(abspath, mode)
        except Exception as e :
            self.stats.failure('do_mkfifo')
            loggers[f'{root_dir}'].info(f'do_mkfifo {abspath} failed: {str(e)}')
            return str(e)
        assert os.path.exists(abspath), f'do_mkfifo: {abspath} should exist'
        assert stat.S_ISFIFO(os.stat(abspath).st_mode), f'do_mkfifo: {abspath} should be fifo'
        self.stats.success('do_mkfifo')
        loggers[f'{root_dir}'].info(f'do_mkfifo {abspath} succeed')
        return get_stat(abspath)

    @rule(dir = Folders)
    @precondition(lambda self: 'listdir' not in EXCLUDE_RULES)
    def listdir(self, dir):
        result1 = self.do_listdir(ROOT_DIR1, dir)
        result2 = self.do_listdir(ROOT_DIR2, dir)
        assert self.equal(result1, result2), f'listdir:\nresult1 is {result1}\nresult2 is {result2}'

    def do_listdir(self, root_dir, dir):
        abspath = os.path.join(root_dir, dir)
        try:
            li = os.listdir(abspath) 
            li = sorted(list(filter(lambda x: x not in JFS_CONTROL_FILES, li)))
        except Exception as e:
            self.stats.failure('do_listdir')
            loggers[f'{root_dir}'].info(f'do_listdir {abspath} failed: {str(e)}')
            return str(e)
        self.stats.success('do_listdir')
        loggers[f'{root_dir}'].info(f'do_listdir {abspath} succeed')
        return tuple(li)

    @rule(file = Files)
    @precondition(lambda self: 'unlink' not in EXCLUDE_RULES)
    def unlink(self, file):
        result1 = self.do_unlink(ROOT_DIR1, file)
        result2 = self.do_unlink(ROOT_DIR2, file)
        assert self.equal(result1, result2), f'unlink:\nresult1 is {result1}\nresult2 is {result2}'

    def do_unlink(self, root_dir, file):
        abspath = os.path.join(root_dir, file)
        # assume(os.path.isfile(abspath))
        try:
            os.unlink(abspath)
        except Exception as e:
            self.stats.failure('do_unlink')
            loggers[f'{root_dir}'].info(f'do_unlink {abspath} failed: {str(e)}')
            return str(e)
        assert not os.path.exists(abspath), f'do_unlink: {abspath} should not exist'
        self.stats.success('do_unlink')
        loggers[f'{root_dir}'].info(f'do_unlink {abspath} succeed')
        return () 

    @rule( target=Files, entry = Files, parent = Folders, new_entry_name = st_entry_name )
    @precondition(lambda self: 'rename_file' not in EXCLUDE_RULES)
    def rename_file(self, entry, parent, new_entry_name):
        result1 = self.do_rename(ROOT_DIR1, entry, parent, new_entry_name)
        result2 = self.do_rename(ROOT_DIR2, entry, parent, new_entry_name)
        assert self.equal(result1, result2), f'rename_file:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, new_entry_name)
        else:
            return INVALID_FILE
        
    @rule( target=Folders, entry = Folders, parent = Folders, new_entry_name = st_entry_name )
    @precondition(lambda self: 'rename_dir' not in EXCLUDE_RULES)
    def rename_dir(self, entry, parent, new_entry_name):
        result1 = self.do_rename(ROOT_DIR1, entry, parent, new_entry_name)
        result2 = self.do_rename(ROOT_DIR2, entry, parent, new_entry_name)
        assert self.equal(result1, result2), f'rename_dir:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, new_entry_name)
        else:
            return INVALID_DIR

    def do_rename(self, root_dir, entry, parent, new_entry_name):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            os.rename(abspath, new_abspath)
        except Exception as e:
            self.stats.failure('do_rename')
            loggers[f'{root_dir}'].info(f'do_rename {abspath} {new_abspath} failed: {str(e)}')
            return str(e)
        # if abspath != new_abspath:
        #     assert not os.path.exists(abspath), f'{abspath} should not exist'
        assert os.path.lexists(new_abspath), f'do_rename: {new_abspath} should exist'
        self.stats.success('do_rename')
        loggers[f'{root_dir}'].info(f'do_rename {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    @rule( target=Files, entry = Files, parent = Folders, new_entry_name = st_entry_name, 
          follow_symlinks = st.booleans() )
    @precondition(lambda self: 'copy_file' not in EXCLUDE_RULES)
    def copy_file(self, entry, parent, new_entry_name, follow_symlinks):
        result1 = self.do_copy_file(ROOT_DIR1, entry, parent, new_entry_name, follow_symlinks)
        result2 = self.do_copy_file(ROOT_DIR2, entry, parent, new_entry_name, follow_symlinks)
        assert self.equal(result1, result2), f'copy_file:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, new_entry_name)
        else:
            return INVALID_FILE

    def do_copy_file(self, root_dir, entry, parent, new_entry_name, follow_symlinks):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            shutil.copy(abspath, new_abspath, follow_symlinks=follow_symlinks)
        except Exception as e:
            self.stats.failure('do_copy_file')
            loggers[f'{root_dir}'].info(f'do_copy_file {abspath} {new_abspath} failed: {str(e)}')
            return str(e)
        assert os.path.lexists(new_abspath), f'do_copy_file: {new_abspath} should exist'
        self.stats.success('do_copy_file')
        loggers[f'{root_dir}'].info(f'do_copy_file {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    @rule( target=Folders, entry = Folders.filter(lambda x: x != ''), parent = Folders, new_entry_name = st_entry_name,
          symlinks=st.booleans(),
          ignore_dangling_symlinks=st.booleans(), 
          dir_exist_ok=st.booleans())
    @precondition(lambda self: 'copy_tree' not in EXCLUDE_RULES)
    def copy_tree(self, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok):
        result1 = self.do_copy_tree(ROOT_DIR1, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok)
        result2 = self.do_copy_tree(ROOT_DIR2, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok)
        assert self.equal(result1, result2), f'copy_tree:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, new_entry_name)
        else:
            return INVALID_DIR

    def do_copy_tree(self, root_dir, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            shutil.copytree(abspath, new_abspath, \
                            symlinks=symlinks, \
                            ignore_dangling_symlinks=ignore_dangling_symlinks, \
                            dirs_exist_ok=dir_exist_ok)
        except Exception as e:
            self.stats.failure('do_copy_dir')
            loggers[f'{root_dir}'].info(f'do_copy_dir {abspath} {new_abspath} failed: {str(e)}')
            return str(e)
        assert os.path.lexists(new_abspath), f'do_copy_tree: {new_abspath} should exist'
        self.stats.success('do_copy_dir')
        loggers[f'{root_dir}'].info(f'do_copy_dir {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    @rule( target = Folders, parent = Folders, subdir = st_entry_name )
    @precondition(lambda self: 'mkdir' not in EXCLUDE_RULES)
    def mkdir(self, parent, subdir):
        # assume(not os.path.exists(os.path.join(ROOT_DIR1, parent, subdir)))
        result1 = self.do_mkdir(ROOT_DIR1, parent, subdir)
        result2 = self.do_mkdir(ROOT_DIR2, parent, subdir)
        assert self.equal(result1, result2), f'mkdir:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, subdir)
        else:
            return INVALID_DIR

    def do_mkdir(self, root_dir, parent, subdir):
        relpath = os.path.join(parent, subdir)
        abspath = os.path.join(root_dir, relpath)
        try:
            os.makedirs(abspath, exist_ok=True)
        except Exception as e:
            self.stats.failure('do_mkdir')
            loggers[f'{root_dir}'].info(f'do_mkdir {abspath} failed: {str(e)}')
            return str(e)
        assert os.path.isdir(abspath), f'do_mkdir: {abspath} should be dir'
        self.stats.success('do_mkdir')
        loggers[f'{root_dir}'].info(f'do_mkdir {abspath} succeed')
        return get_stat(abspath)
    
    @rule( dir = Folders )
    @precondition(lambda self: 'rmdir' not in EXCLUDE_RULES)
    def rmdir(self, dir):
        result1 = self.do_rmdir(ROOT_DIR1, dir)
        result2 = self.do_rmdir(ROOT_DIR2, dir)
        assert self.equal(result1, result2), f'rmdir:\nresult1 is {result1}\nresult2 is {result2}'

    def do_rmdir(self, root_dir, dir ):
        abspath = os.path.join(root_dir, dir)
        try:
            os.rmdir(abspath)
        except Exception as e:
            self.stats.failure('do_rmdir')
            loggers[f'{root_dir}'].info(f'do_rmdir {abspath} failed: {str(e)}')
            return str(e)
        assert not os.path.exists(abspath), f'{abspath} should not exist'
        self.stats.success('do_rmdir')
        loggers[f'{root_dir}'].info(f'do_rmdir {abspath} succeed')
        return ()

    @rule(target = Files, dest_file = Files, parent = Folders, link_file_name = st_entry_name)
    @precondition(lambda self: 'hardlink' not in EXCLUDE_RULES)
    def hardlink(self, dest_file, parent, link_file_name):
        # assume(not os.path.exists(os.path.join(ROOT_DIR1, parent_dir, link_file_name)))
        result1 = self.do_hardlink(ROOT_DIR1, dest_file, parent, link_file_name)
        result2 = self.do_hardlink(ROOT_DIR2, dest_file, parent, link_file_name)
        assert self.equal(result1, result2), f'hardlink:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, link_file_name)
        else:
            return INVALID_FILE

    def do_hardlink(self, root_dir, dest_file, parent, link_file_name):
        dest_abs_path = os.path.join(root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(root_dir, link_rel_path)
        try:
            # print(f'call os.link({dest_abs_path}, {link_abs_path})')
            os.link(dest_abs_path, link_abs_path)
        except Exception as e:
            self.stats.failure('do_hardlink')
            loggers[f'{root_dir}'].info(f"do_hardlink {dest_abs_path} {link_abs_path} failed: {str(e)}")
            return str(e)
        # time.sleep(0.005)
        assert os.path.lexists(link_abs_path), f'do_hardlink {link_abs_path} should exist'
        self.stats.success('do_hardlink')
        loggers[f'{root_dir}'].info(f'do_hardlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)
    
    @rule(target = Files , dest_file = Files, parent = Folders, link_file_name = st_entry_name )
    @precondition(lambda self: 'symlink' not in EXCLUDE_RULES)
    def symlink(self, dest_file, parent, link_file_name):
        # assume(not os.path.exists(os.path.join(ROOT_DIR1, parent_dir, link_file_name)))
        result1 = self.do_symlink(ROOT_DIR1, dest_file, parent, link_file_name)
        result2 = self.do_symlink(ROOT_DIR2, dest_file, parent, link_file_name)
        assert self.equal(result1, result2), f'symlink:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return os.path.join(parent, link_file_name)
        else:
            return INVALID_FILE

    def do_symlink(self, root_dir, dest_file, parent, link_file_name):
        dest_abs_path = os.path.join(root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(root_dir, link_rel_path)
        try:
            os.symlink(dest_abs_path, link_abs_path)
        except Exception as e:
            self.stats.failure('do_symlink')
            loggers[f'{root_dir}'].info(f"do_symlink {dest_abs_path} {link_abs_path} failed: {str(e)}")
            return str(e)
        assert os.path.islink(link_abs_path), f'do_symlink: {link_abs_path} should be link'
        self.stats.success('do_symlink')
        loggers[f'{root_dir}'].info(f'do_symlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)

    @rule(target=FilesWithXattr, file = Files.filter(lambda f: f != ''), 
        name = st_xattr_name,
        value = st_xattr_value, 
        flag = st.sampled_from([xattr.XATTR_CREATE, xattr.XATTR_REPLACE])
        )
    @precondition(lambda self: 'set_xattr' not in EXCLUDE_RULES)
    def set_xattr(self, file, name, value, flag):
        result1 = self.do_set_xattr(ROOT_DIR1, file, name, value, flag)
        result2 = self.do_set_xattr(ROOT_DIR2, file, name, value, flag)
        assert self.equal(result1, result2), f'set_xattr:\nresult1 is {result1}\nresult2 is {result2}'
        if isinstance(result1, tuple):
            return file
        else:
            return INVALID_FILE
    
    def do_set_xattr(self, root_dir, file, name, value, flag):
        abspath = os.path.join(root_dir, file)
        try:
            xattr.setxattr(abspath, 'user.'+name, value, flag)
        except Exception as e:
            self.stats.failure('do_set_xattr')
            loggers[f'{root_dir}'].info(f"do_set_xattr {abspath} user.{name} {value} {flag} failed: {str(e)}")
            return str(e)
        self.stats.success('do_set_xattr')
        loggers[f'{root_dir}'].info(f"do_set_xattr {abspath} user.{name} {value} {flag} succeed")
        v = xattr.getxattr(abspath, 'user.'+name)
        return (v,)

    @rule(file = FilesWithXattr)
    @precondition(lambda self: 'get_xattr' not in EXCLUDE_RULES)
    def remove_xattr(self, file):
        result1 = self.do_remove_xattr(ROOT_DIR1, file)
        result2 = self.do_remove_xattr(ROOT_DIR2, file)
        assert self.equal(result1, result2), f'remove_xattr:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_remove_xattr(self, root_dir, file):
        abspath = os.path.join(root_dir, file)
        try:
            name = ''
            names = sorted(xattr.listxattr(abspath))
            if len(names) > 0:
                name = names[0]
                xattr.removexattr(abspath, name)
        except Exception as e:
            self.stats.failure('do_remove_xattr')
            loggers[f'{root_dir}'].info(f"do_remove_xattr {abspath} {name} failed: {str(e)}")
            return str(e)
        self.stats.success('do_remove_xattr')
        loggers[f'{root_dir}'].info(f"do_remove_xattr {abspath} {name} succeed")
        assert name not in xattr.listxattr(abspath), f'do_remove_xattr: {name} should not in xattr list'
        return tuple(sorted(xattr.listxattr(abspath)))

    @rule(file = Files, mode = st_file_mode, owner = st.sampled_from(USERNAMES))
    @precondition(lambda self: 'chmod_file' not in EXCLUDE_RULES)
    def chmod_file(self, file, mode, owner):
        result1 = self.do_chmod(ROOT_DIR1, file, mode, owner)
        result2 = self.do_chmod(ROOT_DIR2, file, mode, owner)
        assert self.equal(result1, result2), f'chmod_file:\nresult1 is {result1}\nresult2 is {result2}'

    @rule(dir = Folders, mode = st_file_mode, owner = st.sampled_from(USERNAMES))
    @precondition(lambda self: 'chmod_dir' not in EXCLUDE_RULES)
    def chmod_dir(self, dir, mode, owner):
        result1 = self.do_chmod(ROOT_DIR1, dir, mode, owner)
        result2 = self.do_chmod(ROOT_DIR2, dir, mode, owner)
        assert self.equal(result1, result2), f'chmod_dir:\nresult1 is {result1}\nresult2 is {result2}'

    def do_chmod(self, root_dir, entry, mode, owner):
        abspath = os.path.join(root_dir, entry)
        # assume(os.path.isfile(abspath))
        try:
            # TODO: uncomment after euid issue fixed
            # info = pwd.getpwnam(owner)
            # uid = info.pw_uid
            # old_euid = os.geteuid()
            # os.seteuid(uid)
            os.chmod(abspath, mode)
            # os.seteuid(old_euid)
        except Exception as e:
            self.stats.failure('do_chmod')
            loggers[f'{root_dir}'].info(f"do_chmod {abspath} {mode} {owner} failed: {str(e)}")
            return str(e)
        self.stats.success('do_chmod')
        loggers[f'{root_dir}'].info(f"do_chmod {abspath} {mode} {owner} succeed")
        return get_stat(abspath)

    @rule(file = Files, access_time=st_time, modify_time=st_time, follow_symlinks=st.booleans())
    @precondition(lambda self: 'utime_file' not in EXCLUDE_RULES)
    def utime_file(self, file, access_time, modify_time, follow_symlinks):
        result1 = self.do_utime(ROOT_DIR1, file, access_time, modify_time, follow_symlinks)
        result2 = self.do_utime(ROOT_DIR2, file, access_time, modify_time, follow_symlinks)
        assert self.equal(result1, result2), f'utime_file:\nresult1 is {result1}\nresult2 is {result2}'

    @rule(dir = Folders, access_time=st_time, modify_time=st_time, follow_symlinks=st.booleans())
    @precondition(lambda self: 'utime_dir' not in EXCLUDE_RULES)
    def utime_dir(self, dir, access_time, modify_time, follow_symlinks):
        result1 = self.do_utime(ROOT_DIR1, dir, access_time, modify_time, follow_symlinks)
        result2 = self.do_utime(ROOT_DIR2, dir, access_time, modify_time, follow_symlinks)
        assert self.equal(result1, result2), f'utime_dir:\nresult1 is {result1}\nresult2 is {result2}'

    def do_utime(self, root_dir, entry, access_time, modify_time, follow_symlinks):
        abspath = os.path.join(root_dir, entry)
        try:
            os.utime(abspath, (access_time, modify_time), follow_symlinks=follow_symlinks)
        except Exception as e:
            self.stats.failure('do_utime')
            loggers[f'{root_dir}'].info(f"do_utime {abspath} {access_time} {modify_time} failed: {str(e)}")
            return str(e)
        self.stats.success('do_utime')
        loggers[f'{root_dir}'].info(f"do_utime {abspath} {access_time} {modify_time} succeed")
        return get_stat(abspath)

    @rule(file = Files, owner=st.sampled_from(USERNAMES))
    @precondition(lambda self: 'chown_file' not in EXCLUDE_RULES)
    def chown_file(self, file, owner):
        result1 = self.do_chown(ROOT_DIR1, file, owner)
        result2 = self.do_chown(ROOT_DIR2, file, owner)
        assert self.equal(result1, result2), f'chown_file:\nresult1 is {result1}\nresult2 is {result2}'

    @rule(dir = Folders, owner = st.sampled_from(USERNAMES))
    @precondition(lambda self: 'chown_dir' not in EXCLUDE_RULES)
    def chown_dir(self, dir, owner):
        result1 = self.do_chown(ROOT_DIR1, dir, owner)
        result2 = self.do_chown(ROOT_DIR2, dir, owner)
        assert self.equal(result1, result2), f'chown_dir:\nresult1 is {result1}\nresult2 is {result2}'
    
    def do_chown(self, root_dir, entry, owner):
        abspath = os.path.join(root_dir, entry)
        info = pwd.getpwnam(owner)
        uid = info.pw_uid
        gid = info.pw_gid
        try:
            os.chown(abspath, uid, gid)
        except Exception as e:
            self.stats.failure('do_chown')
            loggers[f'{root_dir}'].info(f"do_chown {abspath} {owner} failed: {str(e)}")
            return str(e)
        self.stats.success('do_chown')
        loggers[f'{root_dir}'].info(f"do_chown {abspath} {owner} succeed")
        return get_stat(abspath)
     
def compare_content(dir1, dir2):
    os.system('find /tmp/fsrand  -type l ! -exec test -e {} \; -print > broken_symlink.log ')
    exclude_files = []
    with open('broken_symlink.log', 'r') as f:
        lines = f.readlines()
        for line in lines:
            filename = os.path.basename(line.strip())
            exclude_files.append(filename)
    exclude_options = [f'--exclude="{item}"' for item in exclude_files ]
    exclude_options = ' '.join(exclude_options)
    diff_command = f'diff -ur {dir1} {dir2} {exclude_options} 2>&1 |tee diff.log'
    print(diff_command)
    os.system(diff_command)
    with open('diff.log', 'r') as f:
        lines = f.readlines()
        filtered_lines = [line for line in lines if "recursive directory loop" not in line]
        assert len(filtered_lines) == 0, f'found diff: \n' + '\n'.join(filtered_lines)

def compare_stat(dir1, dir2):
    for root, dirs, files in os.walk(dir1):
        for file in files:
            path1 = os.path.join(root, file)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            stat1 = get_stat(path1)
            stat2 = get_stat(path2)
            assert stat1 == stat2, f"{path1}: {stat1} and {path2}: {stat2} have different stats"
        for dir in dirs:
            path1 = os.path.join(root, dir)
            path2 = os.path.join(dir2, os.path.relpath(path1, dir1))
            stat1 = get_stat(path1)
            stat2 = get_stat(path2)
            assert stat1 == stat2, f"{path1}: {stat1} and {path2}: {stat2} have different stats"

if __name__ == '__main__':
    juicefs_machine = JuicefsMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(JuicefsMachine.stats.get(), sort_keys=True, indent=4))
    if COMPARE:
        compare_content(ROOT_DIR1, ROOT_DIR2)
        compare_stat(ROOT_DIR1, ROOT_DIR2)
    