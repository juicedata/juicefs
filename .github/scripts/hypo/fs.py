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
from fs_op import FsOperation
import random
import time

SEED=int(os.environ.get('SEED', random.randint(0, 1000000000)))

@seed(SEED)
class JuicefsMachine(RuleBasedStateMachine):
    Files = Bundle('files')
    Folders = Bundle('folders')
    Entries = Files | Folders
    EntryWithACL = Bundle('entry_with_acl')
    Xattrs = Bundle('xattrs')
    start = time.time()
    use_sdk = os.environ.get('USE_SDK', 'false').lower() == 'true'
    meta_url = os.environ.get('META_URL')
    SUDO_USERS = ['root']
    if use_sdk:
        SUDO_USERS = ['root']
    if os.uname().sysname == 'Darwin':
        USERS=['root']
        GROUPS = ['root']
    else:
        USERS=['root', 'user1', 'user2','user3']
        GROUPS = USERS+['group1', 'group2', 'group3', 'group4']
    group_created = False
    INCLUDE_RULES = []
    if os.getenv('EXCLUDE_RULES'):
        EXCLUDE_RULES = os.getenv('EXCLUDE_RULES').split(',')
    else:
        EXCLUDE_RULES = ['readlines', 'readline']
        # EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file', 'clone_cp_file', 'clone_cp_dir', 'loop_symlink', 'hardlink', 'rename_dir', 'chown']
    ROOT_DIR1=os.environ.get('ROOT_DIR1', '/tmp/fsrand')
    ROOT_DIR2=os.environ.get('ROOT_DIR2', '/tmp/jfs/fsrand')
    if use_sdk:
        fsop1 = FsOperation(name='fs1', root_dir=ROOT_DIR1, use_sdk=use_sdk, is_jfs=False, volume_name=None)
        fsop2 = FsOperation(name='fs2', root_dir=ROOT_DIR2, mount_point='/tmp/jfs', use_sdk=use_sdk, is_jfs=True, volume_name='test-volume', meta_url=meta_url)
    else:
        fsop1 = FsOperation(name='fs1', root_dir=ROOT_DIR1, is_jfs=common.is_jfs(ROOT_DIR1))
        fsop2 = FsOperation(name='fs2', root_dir=ROOT_DIR2, is_jfs=common.is_jfs(ROOT_DIR2))
    check_dangling = os.environ.get('CHECK_DANGLING', 'false').lower() == 'true'
    @initialize(target=Folders)
    def init_folders(self):
        self.fsop1.init_rootdir()
        self.fsop2.init_rootdir()
        return ''
    
    def create_users(self, users):
        for user in users:
            if user != 'root':
                common.create_user(user)

    def get_default_rootdir1(self):
        return '/tmp/fsrand'
    
    def get_default_rootdir2(self):
        return '/tmp/jfs/fsrand'

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print(f'__init__')
        MAX_RUNTIME=int(os.environ.get('MAX_RUNTIME', '36000'))
        duration = time.time() - self.start
        print(f'duration is {duration}')
        if duration > MAX_RUNTIME:
            raise Exception(f'run out of time: {duration}')
        
        if not self.group_created:
            for group in self.GROUPS:
                if group != 'root':
                    common.create_group(group)
            self.group_created = True
        self.create_users(self.USERS)
        self.remove_dangling_files()

    def remove_dangling_files(self):
        if self.check_dangling:
            self.fsop1.do_remove_dangling_files()
            self.fsop2.do_remove_dangling_files()

    def equal(self, result1, result2):
        if os.getenv('PROFILE', 'dev') == 'generate':
            return True
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

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        # os.setegid(pwd.getpwnam(user).pw_gid)

    def should_run(self, rule):
        if len(self.EXCLUDE_RULES) > 0:
            return rule not in self.EXCLUDE_RULES
        else:
            return rule in self.INCLUDE_RULES

    @rule(
        entry = Entries,
        user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('stat'))
    def stat(self, entry, user = 'root'):
        result1 = self.fsop1.do_stat(entry=entry, user=user)
        result2 = self.fsop2.do_stat(entry=entry, user=user)
        assert self.equal(result1, result2), red(f'stat:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(
        entry = Entries,
        user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('lstat'))
    def lstat(self, entry, user = 'root'):
        result1 = self.fsop1.do_lstat(entry=entry, user=user)
        result2 = self.fsop2.do_lstat(entry=entry, user=user)
        assert self.equal(result1, result2), red(f'lstat:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
        entry = Entries,
        user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('exists'))
    def exists(self, entry, user = 'root'):
        result1 = self.fsop1.do_exists(entry=entry, user=user)
        result2 = self.fsop2.do_exists(entry=entry, user=user)
        assert result1 == result2, red(f'exists:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(file = Files.filter(lambda x: x != multiple()), 
          flags = st_open_flags, 
          umask = st_umask,
          mode = st_entry_mode,
          user = st.sampled_from(SUDO_USERS), 
          )
    @precondition(lambda self: self.should_run('open') and not self.use_sdk)
    def open(self, file, flags, mode, user='root', umask=0o022):
        result1 = self.fsop1.do_open(file, flags, umask, mode, user)
        result2 = self.fsop2.do_open(file, flags, umask, mode, user)
        assert self.equal(result1, result2), red(f'open:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(file = Files.filter(lambda x: x != multiple()), 
        mode = st_open_mode, 
        user = st.sampled_from(SUDO_USERS)
        )
    @precondition(lambda self: self.should_run('open') and not self.use_sdk)
    def open2(self, file, mode, user='root'):
        result1 = self.fsop1.do_open2(file=file, mode=mode, user=user)
        result2 = self.fsop2.do_open2(file=file, mode=mode, user=user)
        assert self.equal(result1, result2), red(f'open:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(file = Files.filter(lambda x: x != multiple()), 
          offset = st_offset, 
          content = st_content,
          mode = st_open_mode,
          encoding = st_open_encoding, 
          errors = st_open_errors,
          whence = st_whence,
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: self.should_run('write'))
    def write(self, file, offset, content, mode, whence, encoding=None, errors=None, user='root'):
        result1 = self.fsop1.do_write(file=file, offset=offset, content=content, mode=mode, encoding=encoding, errors=errors, whence=whence, user=user)
        result2 = self.fsop2.do_write(file=file, offset=offset, content=content, mode=mode, encoding=encoding, errors=errors, whence=whence, user=user)
        assert self.equal(result1, result2), red(f'write:\nresult1 is {result1}\nresult2 is {result2}')
    
    # TODO: fix hardcode mode
    @rule(file = Files.filter(lambda x: x != multiple()), 
        offset = st_offset, 
        lines = st_lines,
        mode = st_open_mode,
        whence = st_whence,
        user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('writelines'))
    def writelines(self, file, offset, lines, mode, whence, user='root'):
        result1 = self.fsop1.do_writelines(file=file, offset=offset, lines=lines, mode=mode, whence=whence, user=user)
        result2 = self.fsop2.do_writelines(file=file, offset=offset, lines=lines, mode=mode, whence=whence, user=user)
        assert self.equal(result1, result2), red(f'write:\nresult1 is {result1}\nresult2 is {result2}')
    

    @rule(file = Files.filter(lambda x: x != multiple()),
          offset = st.integers(min_value=0, max_value=MAX_FILE_SIZE),
          length = st.integers(min_value=0, max_value=MAX_FALLOCATE_LENGTH),
          mode = st.just(0), 
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: self.should_run('fallocate') and not self.use_sdk)
    def fallocate(self, file, offset, length, mode, user='root'):
        result1 = self.fsop1.do_fallocate(file, offset, length, mode, user)
        result2 = self.fsop2.do_fallocate(file, offset, length, mode, user)
        assert self.equal(result1, result2), red(f'fallocate:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(src = Files.filter(lambda x: x != multiple()),
        dst = Files.filter(lambda x: x != multiple()),
        src_offset = st_offset,
        dst_offset = st_offset,
        count = st_length,
        user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('copy_file_range') and not self.use_sdk)
    def copy_file_range(self, src, dst, src_offset, dst_offset, count, user):
        result1 = self.fsop1.do_copy_file_range(src=src, dst=dst, src_offset=src_offset, dst_offset=dst_offset, count=count, user=user)
        result2 = self.fsop2.do_copy_file_range(src=src, dst=dst, src_offset=src_offset, dst_offset=dst_offset, count=count, user=user)
        assert self.equal(result1, result2), red(f'copy_file_range:\nresult1 is {result1}\nresult2 is {result2}')

    @rule( file = Files.filter(lambda x: x != multiple()), 
          mode = st_open_mode,
          encoding = st_open_encoding,
          errors = st_open_errors,
          offset = st_offset, 
          length = st.integers(min_value=0, max_value=MAX_FILE_SIZE), 
          whence = st_whence,
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('read'))
    def read(self, file, mode, offset, length, whence=os.SEEK_CUR, encoding=None, errors=None, user='root'):
        result1 = self.fsop1.do_read(file=file, mode=mode, length=length, offset=offset, whence=whence, user=user, encoding=encoding, errors=errors)
        result2 = self.fsop2.do_read(file=file, mode=mode, length=length, offset=offset, whence=whence, user=user, encoding=encoding, errors=errors)
        assert self.equal(result1, result2), red(f'read:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule( file = Files.filter(lambda x: x != multiple()), 
          mode = st_open_mode,
          offset = st_offset, 
          whence = st_whence,
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('readlines'))
    def readlines(self, file, mode, offset, whence=os.SEEK_CUR, user='root'):
        result1 = self.fsop1.do_readlines(file=file, mode=mode, offset=offset, whence=whence, user=user)
        result2 = self.fsop2.do_readlines(file=file, mode=mode, offset=offset, whence=whence, user=user)
        assert self.equal(result1, result2), red(f'readlines:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule( file = Files.filter(lambda x: x != multiple()), 
          mode = st_open_mode,
          offset = st_offset, 
          whence = st_whence,
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('readline'))
    def readline(self, file, mode, offset, whence=os.SEEK_CUR, user='root'):
        result1 = self.fsop1.do_readline(file=file, mode=mode, offset=offset, whence=whence, user=user)
        result2 = self.fsop2.do_readline(file=file, mode=mode, offset=offset, whence=whence, user=user)
        assert self.equal(result1, result2), red(f'readline:\nresult1 is {result1}\nresult2 is {result2}')
    

    @rule(file=Files.filter(lambda x: x != multiple()), 
          size=st.integers(min_value=0, max_value=MAX_TRUNCATE_LENGTH), 
          user=st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('truncate'))
    def truncate(self, file, size, user='root'):
        result1 = self.fsop1.do_truncate(file=file, size=size, user=user)
        result2 = self.fsop2.do_truncate(file=file, size=size, user=user)
        assert self.equal(result1, result2), red(f'truncate:\nresult1 is {result1}\nresult2 is {result2}')
    
    @rule(target=Files, 
          parent = Folders.filter(lambda x: x != multiple()), 
          file_name = st_file_name, 
          content = st_content,
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: self.should_run('create_file'))
    def create_file(self, parent, file_name, content, mode='xb', buffering=-1, user='root', umask=0o022):
        result1 = self.fsop1.do_create_file(parent=parent, file_name=file_name, mode=mode, buffering=buffering, content=content, user=user, umask=umask)
        result2 = self.fsop2.do_create_file(parent=parent, file_name=file_name, mode=mode, buffering=buffering, content=content, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'create_file:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, file_name)

    @rule(dir = Folders.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('listdir'))
    def listdir(self, dir, user='root'):
        result1 = self.fsop1.do_listdir(dir=dir, user=user)
        result2 = self.fsop2.do_listdir(dir=dir, user=user)
        assert self.equal(result1, result2), red(f'listdir:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
          target = Files,
          file = consumes(Files).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('unlink'))
    def unlink(self, file, user='root'):
        result1 = self.fsop1.do_unlink(file=file, user=user)
        result2 = self.fsop2.do_unlink(file=file, user=user)
        assert self.equal(result1, result2), red(f'unlink:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return file
        else:
            return multiple()
            
    @rule( target=Files, 
          entry = consumes(Files).filter(lambda x: x != multiple()),
          parent = Folders, 
          new_entry_name = st_file_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: self.should_run('rename_file'))
    def rename_file(self, entry, parent, new_entry_name, user='root', umask=0o022):
        result1 = self.fsop1.do_rename(entry=entry, parent=parent, new_entry_name=new_entry_name, user=user, umask=umask)
        result2 = self.fsop2.do_rename(entry=entry, parent=parent, new_entry_name=new_entry_name, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'rename_file:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        
    @rule( target=Folders, 
          entry = consumes(Folders).filter(lambda x: x != multiple()), 
          parent = Folders, 
          new_entry_name = valid_dir_name(),
          user = st.sampled_from(SUDO_USERS),
          umask = st_umask)
    @precondition(lambda self: self.should_run('rename_dir'))
    def rename_dir(self, entry, parent, new_entry_name, user='root', umask=0o022):
        result1 = self.fsop1.do_rename(entry=entry, parent=parent, new_entry_name=new_entry_name, user=user, umask=umask)
        result2 = self.fsop2.do_rename(entry=entry, parent=parent, new_entry_name=new_entry_name, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'rename_dir:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        

    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_file_name, 
          follow_symlinks = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask )
    @precondition(lambda self: self.should_run('copy_file') and not self.use_sdk)
    def copy_file(self, entry, parent, new_entry_name, follow_symlinks, user='root',  umask=0o022):
        result1 = self.fsop1.do_copy_file(entry, parent, new_entry_name, follow_symlinks, user, umask)
        result2 = self.fsop2.do_copy_file(entry, parent, new_entry_name, follow_symlinks, user, umask)
        assert self.equal(result1, result2), red(f'copy_file:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, new_entry_name)
    
    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_file_name, 
          preserve = st.just(False),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask )
    @precondition(lambda self: self.should_run('clone_cp_file') \
                  and (self.fsop1.singlezone or self.fsop2.singlezone))
    def clone_cp_file(self, entry, parent, new_entry_name, preserve, user='root', umask=0o022):
        result1 = self.fsop1.do_clone_entry(entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop2.do_clone_entry(entry, parent, new_entry_name, preserve, user, umask)
        assert type(result1) == type(result2), red(f'clone_cp_file:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            assert result1 == result2, red(f'clone_cp_file:\nresult1 is {result1}\nresult2 is {result2}')
            return os.path.join(parent, new_entry_name)
        
    @rule( target=Folders, 
          entry = Folders.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = valid_dir_name(), 
          preserve = st.just(False),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask,
    )
    @precondition(lambda self: self.should_run('clone_cp_dir') \
                  and (self.fsop1.singlezone or self.fsop2.singlezone))
    def clone_cp_dir(self, entry, parent, new_entry_name, preserve, user, umask):
        result1 = self.fsop1.do_clone_entry(entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop2.do_clone_entry(entry, parent, new_entry_name, preserve, user, umask)
        assert self.equal(result1, result2), red(f'clone_cp_dir:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            assert result1 == result2, red(f'clone_cp_dir:\nresult1 is {result1}\nresult2 is {result2}')
            return os.path.join(parent, new_entry_name)

    @rule( target = Folders, 
          parent = Folders.filter(lambda x: x != multiple()),
          subdir = valid_dir_name(),
          mode = st_entry_mode,
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: self.should_run('mkdir'))
    def mkdir(self, parent, subdir, mode, user='root', umask=0o022):
        result1 = self.fsop1.do_mkdir(parent=parent, subdir=subdir, mode=mode, user=user, umask=umask)
        result2 = self.fsop2.do_mkdir(parent=parent, subdir=subdir, mode=mode, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'mkdir:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, subdir)

    @rule( target = Folders,
          dir = consumes(Folders).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('rmdir'))
    def rmdir(self, dir, user='root'):
        assume(dir != '')
        result1 = self.fsop1.do_rmdir(dir=dir, user=user)
        result2 = self.fsop2.do_rmdir(dir=dir, user=user)
        assert self.equal(result1, result2), red(f'rmdir:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return dir
        else:
            return multiple()

    @rule(target = Files, 
          src_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()), 
          link_file_name = st_file_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: self.should_run('hardlink'))
    def hardlink(self, src_file, parent, link_file_name, user='root', umask=0o022):
        result1 = self.fsop1.do_hardlink(src_file=src_file, parent=parent, link_file_name=link_file_name, user=user, umask=umask)
        result2 = self.fsop2.do_hardlink(src_file=src_file, parent=parent, link_file_name=link_file_name, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'hardlink:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)
    
    @rule(target = Files , 
          src_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()),
          link_file_name = st_file_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: self.should_run('symlink'))
    def symlink(self, src_file, parent, link_file_name, user='root', umask=0o022):
        result1 = self.fsop1.do_symlink(src_file=src_file, parent=parent, link_file_name=link_file_name, user=user, umask=umask)
        result2 = self.fsop2.do_symlink(src_file=src_file, parent=parent, link_file_name=link_file_name, user=user, umask=umask)
        assert self.equal(result1, result2), red(f'symlink:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)

    @rule(target = Files , 
          parent = Folders.filter(lambda x: x != multiple()),
          link_file_name = st_file_name, 
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: self.should_run('loop_symlink'))
    def loop_symlink(self, parent, link_file_name, user='root'):
        result1 = self.fsop1.do_loop_symlink(parent=parent, link_file_name=link_file_name, user=user)
        result2 = self.fsop2.do_loop_symlink(parent=parent, link_file_name=link_file_name, user=user)
        assert self.equal(result1, result2), red(f'loop_symlink:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)

    @rule(file = Files.filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('readlink'))
    def readlink(self, file, user='root'):
        result1 = self.fsop1.do_readlink(file=file, user=user)
        result2 = self.fsop2.do_readlink(file=file, user=user)
        assert self.equal(result1, result2), red(f'read_link:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(target=Xattrs, 
          file = Files.filter(lambda x: x != multiple()), 
          name = st_xattr_name,
          value = st_xattr_value, 
          flag = st_xattr_flag,
          user = st.sampled_from(SUDO_USERS)
        )
    @precondition(lambda self: self.should_run('set_xattr'))
    def set_xattr(self, file, name, value, flag, user='root'):
        result1 = self.fsop1.do_set_xattr(file=file, name=name, value=value, flag=flag, user=user)
        result2 = self.fsop2.do_set_xattr(file=file, name=name, value=value, flag=flag, user=user)
        assert self.equal(result1, result2), red(f'set_xattr:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return (file, name)

    @rule(xattr = Xattrs.filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS)
    )
    @precondition(lambda self: self.should_run('get_xattr'))
    def get_xattr(self, xattr, user):
        result1 = self.fsop1.do_get_xattr(file=xattr[0], name=xattr[1], user=user)
        result2 = self.fsop2.do_get_xattr(file=xattr[0], name=xattr[1], user=user)
        assert self.equal(result1, result2), red(f'get_xattr:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(file=Files.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('list_xattr'))
    def list_xattr(self, file, user='root'):
        result1 = self.fsop1.do_list_xattr(file=file, user=user)
        result2 = self.fsop2.do_list_xattr(file=file, user=user)
        assert self.equal(result1, result2), red(f'list_xattr:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
        target = Xattrs,
        xattr = consumes(Xattrs).filter(lambda x: x != multiple()), 
        user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('remove_xattr'))
    def remove_xattr(self, xattr, user='root'):
        result1 = self.fsop1.do_remove_xattr(file=xattr[0], name=xattr[1], user=user)
        result2 = self.fsop2.do_remove_xattr(file=xattr[0], name=xattr[1], user=user)
        assert self.equal(result1, result2), red(f'remove_xattr:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return xattr
        else:
            return multiple()
        
    @rule(user = st.sampled_from(USERS).filter(lambda x: x != 'root'), 
          group = st.sampled_from(GROUPS),
          groups = st.lists(st.sampled_from(GROUPS), unique=True))
    @precondition(lambda self: self.should_run('change_groups') and not self.use_sdk)
    def change_groups(self, user, group, groups):
        self.fsop1.do_change_groups(user, group, groups)
        self.fsop2.do_change_groups(user, group, groups)

    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          mode = st_entry_mode, 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('chmod'))
    def chmod(self, entry, mode, user='root'):
        result1 = self.fsop1.do_chmod(entry=entry, mode=mode, user=user)
        result2 = self.fsop2.do_chmod(entry=entry, mode=mode, user=user)
        assert self.equal(result1, result2), red(f'chmod:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(entry = Entries.filter(lambda x: x != multiple()))
    @precondition(lambda self: self.should_run('get_acl') and not self.use_sdk)
    def get_acl(self, entry):
        result1 = self.fsop1.do_get_acl(entry)
        result2 = self.fsop2.do_get_acl(entry)
        assert self.equal(result1, result2), red(f'get_acl:\nresult1 is {result1}\nresult2 is {result2}')

    
    @rule(entry = EntryWithACL.filter(lambda x: x != multiple()), 
          option = st.sampled_from(['--remove-all', '--remove-default']),
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: self.should_run('remove_acl') and not self.use_sdk)
    def remove_acl(self, entry: str, option: str, user='root'):
        result1 = self.fsop1.do_remove_acl(entry, option, user)
        result2 = self.fsop2.do_remove_acl(entry, option, user)
        assert self.equal(result1, result2), red(f'remove_acl:\nresult1 is {result1}\nresult2 is {result2}')

    @rule(
          target=EntryWithACL,
          sudo_user = st.sampled_from(SUDO_USERS),
          entry = Entries.filter(lambda x: x != multiple()), 
          user=st.sampled_from(USERS+['']),
          user_perm = st.sets(st.sampled_from(['r', 'w', 'x'])),
          group=st.sampled_from(GROUPS+['']),
          group_perm = st.sets(st.sampled_from(['r', 'w', 'x'])),
          other_perm = st.sets(st.sampled_from(['r', 'w', 'x'])),
          set_mask = st.booleans(),
          mask = st.sets(st.sampled_from(['r', 'w', 'x'])),
          default = st.booleans(),
          recursive = st.booleans(),
          recalc_mask = st.booleans(),
          not_recalc_mask = st.booleans(),
          logical = st.booleans(),
          physical = st.booleans(),
          )
    @precondition(lambda self: self.should_run('set_acl') and not self.use_sdk)
    def set_acl(self, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical):
        result1 = self.fsop1.do_set_acl(sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        result2 = self.fsop2.do_set_acl(sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        assert self.equal(result1, result2), red(f'set_acl:\nresult1 is {result1}\nresult2 is {result2}')
        if isinstance(result1, Exception):
            return multiple()
        else:
            return entry

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          access_time=st_time, 
          modify_time=st_time, 
          follow_symlinks=st.booleans(), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('utime') and False)
    def utime(self, entry, access_time, modify_time, follow_symlinks, user='root'):
        result1 = self.fsop1.do_utime(entry=entry, access_time=access_time, modify_time=modify_time, follow_symlinks=follow_symlinks, user=user)
        result2 = self.fsop2.do_utime(entry=entry, access_time=access_time, modify_time=modify_time, follow_symlinks=follow_symlinks, user=user)
        assert self.equal(result1, result2), red(f'utime:\nresult1 is {result1}\nresult2 is {result2}')


    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          owner= st.sampled_from(USERS), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: self.should_run('chown'))
    def chown(self, entry, owner, user='root'):
        result1 = self.fsop1.do_chown(entry=entry, owner=owner, user=user)
        result2 = self.fsop2.do_chown(entry=entry, owner=owner, user=user)
        assert self.equal(result1, result2), red(f'chown:\nresult1 is {result1}\nresult2 is {result2}')
     
    @rule( dir =Folders, vdirs = st.integers(min_value=2, max_value=31) )
    @precondition(lambda self: self.should_run('split_dir') \
                  and (self.fsop1.is_jfs or self.fsop2.is_jfs) \
                  and not self.use_sdk
    )
    def split_dir(self, dir, vdirs):
        self.fsop1.do_split_dir(dir, vdirs)
        self.fsop2.do_split_dir(dir, vdirs)

    @rule(dir = Folders)
    @precondition(lambda self: self.should_run('merge_dir') \
                 and (self.fsop1.is_jfs or self.fsop2.is_jfs) \
                 and not self.use_sdk
    )
    def merge_dir(self, dir):
        self.fsop1.do_merge_dir(dir)
        self.fsop2.do_merge_dir(dir)
    
    @rule(dir = Folders,
          zone1=st.sampled_from(common.get_zones(ROOT_DIR1)),
          zone2=st.sampled_from(common.get_zones(ROOT_DIR2)),
          is_vdir=st.booleans())
    @precondition(lambda self: self.should_run('rebalance_dir') \
                   and (self.fsop1.is_jfs or self.fsop2.is_jfs) \
                   and not self.use_sdk \
                   and os.getenv('PROFILE', 'dev') != 'generate'
    )
    def rebalance_dir(self, dir, zone1, zone2, is_vdir, pysdk=True):
        self.fsop1.do_rebalance(entry=dir, zone=zone1, is_vdir=is_vdir, pysdk=pysdk)
        self.fsop2.do_rebalance(entry=dir, zone=zone2, is_vdir=is_vdir, pysdk=pysdk)

    @rule(file = Files, 
          zone1=st.sampled_from(common.get_zones(ROOT_DIR1)),
          zone2=st.sampled_from(common.get_zones(ROOT_DIR2)),
          )
    @precondition(lambda self: self.should_run('rebalance_file') \
                   and (self.fsop1.is_jfs or self.fsop2.is_jfs) \
                   and not self.use_sdk \
                   and os.getenv('PROFILE', 'dev') != 'generate'
    )
    def rebalance_file(self, file, zone1, zone2, pysdk=True):
        self.fsop1.do_rebalance(entry=file, zone=zone1, is_vdir=False, pysdk=pysdk)
        self.fsop2.do_rebalance(entry=file, zone=zone2, is_vdir=False, pysdk=pysdk)

    def teardown(self):
        if self.check_dangling:
            self.fsop1.do_check_dangling_files()
            self.fsop2.do_check_dangling_files()

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
    juicefs_machine = JuicefsMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FsOperation.stats.get(), sort_keys=True, indent=4))