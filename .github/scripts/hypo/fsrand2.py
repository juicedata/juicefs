import os
import pwd
import subprocess
import json
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
from hypothesis import HealthCheck, assume, strategies as st, settings, Verbosity
from hypothesis.stateful import rule, precondition, RuleBasedStateMachine, Bundle, initialize, multiple, consumes
from hypothesis import Phase, seed
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
    FilesWithXattr = Bundle('files_with_xattr')
    start = time.time()
    DEFALUT_ROOT_DIR1 = '/tmp/fsrand'
    DEFALUT_ROOT_DIR2 = '/tmp/jfs/fsrand'
    ROOT_DIR1=os.environ.get('ROOT_DIR1', DEFALUT_ROOT_DIR1).split(',')
    ROOT_DIR1 = [x.rstrip('/') for x in ROOT_DIR1]
    ROOT_DIR2=os.environ.get('ROOT_DIR2', DEFALUT_ROOT_DIR2).split(',')
    ROOT_DIR2 = [x.rstrip('/') for x in ROOT_DIR2]
    log_level = os.environ.get('LOG_LEVEL', 'INFO')
    logger = common.setup_logger(f'./fsrand.log', 'fsrand_logger', log_level)
    fsop = FsOperation(logger)
    ZONES1 = common.get_zones(ROOT_DIR1[0])
    ZONES2 = common.get_zones(ROOT_DIR2[0])
    SUDO_USERS = ['root', 'user1']
    USERS=['root', 'user1', 'user2','user3']
    GROUPS = USERS+['group1', 'group2', 'group3', 'group4']
    group_created = False
    INCLUDE_RULES = []
    EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file', 'merge_dir', 'split_dir', \
                        'clone_cp_file', 'clone_cp_dir', 'set_acl']
    @initialize(target=Folders)
    def init_folders(self):
        if not os.path.exists(self.ROOT_DIR1[0]):
            os.makedirs(self.ROOT_DIR1[0])
        if not os.path.exists(self.ROOT_DIR2[0]):
            os.makedirs(self.ROOT_DIR2[0])
        if os.environ.get('PROFILE', 'dev') != 'generate':
            common.clean_dir(self.ROOT_DIR1[0])
            common.clean_dir(self.ROOT_DIR2[0])
        return ''
    
    def create_users(self, users):
        for user in users:
            if user != 'root':
                common.create_user(user)

    def __init__(self):
        super(JuicefsMachine, self).__init__()
        print(f'__init__')
        if os.environ.get('EXCLUDE_RULES') is not None:
            self.EXCLUDE_RULES = os.environ.get('EXCLUDE_RULES').split(',')
        if not self.group_created:
            for group in self.GROUPS:
                common.create_group(group)
            self.group_created = True
        self.create_users(self.USERS)
        MAX_RUNTIME=int(os.environ.get('MAX_RUNTIME', '36000'))
        duration = time.time() - self.start
        print(f'duration is {duration}')
        if duration > MAX_RUNTIME:
            raise Exception(f'run out of time: {duration}')

    def equal(self, result1, result2, rootdir1, rootdir2):
        if os.getenv('PROFILE', 'dev') == 'generate':
            return True
        if type(result1) != type(result2):
            return False
        if isinstance(result1, Exception):
            r1 = str(result1).replace(rootdir1, '')
            r2 = str(result2).replace(rootdir2, '')
            return r1 == r2
        elif isinstance(result1, tuple):
            return result1 == result2
        elif isinstance(result1, str):
            r1 = str(result1).replace(rootdir1, '')
            r2 = str(result2).replace(rootdir2, '')
            return  r1 == r2
        else:
            return result1 == result2

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        # os.setegid(pwd.getpwnam(user).pw_gid)

    @rule(file = Files.filter(lambda x: x != multiple()), 
          flags = st_open_flags, 
          umask = st_umask,
          mode = st_entry_mode,
          user = st.sampled_from(SUDO_USERS),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2) 
          )
    @precondition(lambda self: 'open' not in self.EXCLUDE_RULES)
    def open(self, file, flags, mode, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_open(rootdir1, file, flags, umask, mode, user)
        result2 = self.fsop.do_open(rootdir2, file, flags, umask, mode, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mopen:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file = Files.filter(lambda x: x != multiple()), 
          offset = st_offset, 
          content = st_content,
          flags = st_open_flags,
          whence = st.sampled_from([os.SEEK_SET, os.SEEK_CUR, os.SEEK_END]),
          user = st.sampled_from(SUDO_USERS),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'write' not in self.EXCLUDE_RULES)
    def write(self, file, offset, content, flags, whence, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_write(rootdir1, file, offset, content, flags, whence, user)
        result2 = self.fsop.do_write(rootdir2, file, offset, content, flags, whence, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mwrite:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file = Files.filter(lambda x: x != multiple()),
          offset = st.integers(min_value=0, max_value=MAX_FILE_SIZE),
          length = st.integers(min_value=0, max_value=MAX_FALLOCATE_LENGTH),
          mode = st.just(0), 
          user = st.sampled_from(SUDO_USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'fallocate' not in self.EXCLUDE_RULES)
    def fallocate(self, file, offset, length, mode, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_fallocate(rootdir1, file, offset, length, mode, user)
        result2 = self.fsop.do_fallocate(rootdir2, file, offset, length, mode, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mfallocate:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule( file = Files.filter(lambda x: x != multiple()), 
          offset = st_offset, 
          length = st.integers(min_value=0, max_value=MAX_FILE_SIZE), 
          user = st.sampled_from(SUDO_USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'read' not in self.EXCLUDE_RULES)
    def read(self, file, offset, length, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_read(rootdir1, file, offset, length, user)
        result2 = self.fsop.do_read(rootdir2, file, offset, length, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mread:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file=Files.filter(lambda x: x != multiple()), 
          size=st.integers(min_value=0, max_value=MAX_TRUNCATE_LENGTH), 
          user=st.sampled_from(SUDO_USERS), 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'truncate' not in self.EXCLUDE_RULES)
    def truncate(self, file, size, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_truncate(rootdir1, file, size, user)
        result2 = self.fsop.do_truncate(rootdir2, file, size, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mtruncate:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    
    @rule(target=Files, 
          parent = Folders.filter(lambda x: x != multiple()), 
          file_name = st_entry_name, 
          mode = st_open_mode, 
          content = st_content, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'create_file' not in self.EXCLUDE_RULES)
    def create_file(self, parent, file_name, content, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, mode='x', user='root', umask=0o022):
        result1 = self.fsop.do_create_file(rootdir1, parent, file_name, mode, content, user, umask)
        result2 = self.fsop.do_create_file(rootdir2, parent, file_name, mode, content, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mcreate_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, file_name)

    @rule(dir = Folders.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'listdir' not in self.EXCLUDE_RULES)
    def listdir(self, dir, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_listdir(rootdir1, dir, user)
        result2 = self.fsop.do_listdir(rootdir2, dir, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mlistdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
          target = Files,
          file = consumes(Files).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'unlink' not in self.EXCLUDE_RULES)
    def unlink(self, file, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        print(file)
        result1 = self.fsop.do_unlink(rootdir1, file, user)
        result2 = self.fsop.do_unlink(rootdir2, file, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31munlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return file
        else:
            return multiple()
            
    @rule( target=Files, 
          entry = consumes(Files).filter(lambda x: x != multiple()),
          parent = Folders, 
          new_entry_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'rename_file' not in self.EXCLUDE_RULES)
    def rename_file(self, entry, parent, new_entry_name, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_rename(rootdir1, entry, parent, new_entry_name, user, umask)
        result2 = self.fsop.do_rename(rootdir2, entry, parent, new_entry_name, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mrename_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        
    @rule( target=Folders, 
          entry = consumes(Folders).filter(lambda x: x != multiple()), 
          parent = Folders, 
          new_entry_name = st_entry_name,
          user = st.sampled_from(SUDO_USERS),
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'rename_dir' not in self.EXCLUDE_RULES)
    def rename_dir(self, entry, parent, new_entry_name, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_rename(rootdir1, entry, parent, new_entry_name, user, umask)
        result2 = self.fsop.do_rename(rootdir2, entry, parent, new_entry_name, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mrename_dir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        

    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_entry_name, 
          follow_symlinks = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
        )
    @precondition(lambda self: 'copy_file' not in self.EXCLUDE_RULES)
    def copy_file(self, entry, parent, new_entry_name, follow_symlinks, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root',  umask=0o022):
        result1 = self.fsop.do_copy_file(rootdir1, entry, parent, new_entry_name, follow_symlinks, user, umask)
        result2 = self.fsop.do_copy_file(rootdir2, entry, parent, new_entry_name, follow_symlinks, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mcopy_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, new_entry_name)
    
    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_entry_name, 
          preserve = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'clone_cp_file' not in self.EXCLUDE_RULES)
    def clone_cp_file(self, entry, parent, new_entry_name, preserve, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_clone_entry(rootdir1, entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop.do_clone_entry(rootdir2, entry, parent, new_entry_name, preserve, user, umask)
        # assert self.equal(result1, result2), f'clone_file:\nresult1 is {result1}\nresult2 is {result2}'
        assert type(result1) == type(result2), f'\033[31mclone_cp_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            assert result1 == result2, f'\033[31mclone_cp_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
            return os.path.join(parent, new_entry_name)
        
    @rule( target=Folders, 
          entry = Folders.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_entry_name, 
          preserve = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask,
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
    )
    @precondition(lambda self: 'clone_cp_dir' not in self.EXCLUDE_RULES )
    def clone_cp_dir(self, entry, parent, new_entry_name, preserve, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_clone_entry(rootdir1, entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop.do_clone_entry(rootdir2, entry, parent, new_entry_name, preserve, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mclone_cp_dir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            assert result1 == result2, f'\033[31mclone_cp_dir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
            return os.path.join(parent, new_entry_name)

    @rule( target = Folders, 
          parent = Folders.filter(lambda x: x != multiple()),
          subdir = st_entry_name,
          mode = st_entry_mode,
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'mkdir' not in self.EXCLUDE_RULES)
    def mkdir(self, parent, subdir, mode, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_mkdir(rootdir1, parent, subdir, mode, user, umask)
        result2 = self.fsop.do_mkdir(rootdir2, parent, subdir, mode, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mmkdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, subdir)

    @rule( target = Folders,
          dir = consumes(Folders).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS), 
          rootdir1=st.sampled_from(ROOT_DIR1),
          rootdir2=st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'rmdir' not in self.EXCLUDE_RULES)
    def rmdir(self, dir, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        assume(dir != '')
        result1 = self.fsop.do_rmdir(rootdir1, dir, user)
        result2 = self.fsop.do_rmdir(rootdir2, dir, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mrmdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return dir
        else:
            return multiple()

    @rule(target = Files, 
          dest_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()), 
          link_file_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask,
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'hardlink' not in self.EXCLUDE_RULES)
    def hardlink(self, dest_file, parent, link_file_name, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root', umask=0o022):
        result1 = self.fsop.do_hardlink(rootdir1, dest_file, parent, link_file_name, user, umask)
        result2 = self.fsop.do_hardlink(rootdir2, dest_file, parent, link_file_name, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mhardlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)
    
    @rule(target = Files , 
          dest_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()),
          link_file_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask, 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'symlink' not in self.EXCLUDE_RULES)
    def symlink(self, dest_file, parent, link_file_name,rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2,  user='root', umask=0o022):
        result1 = self.fsop.do_symlink(rootdir1, dest_file, parent, link_file_name, user, umask)
        result2 = self.fsop.do_symlink(rootdir2, dest_file, parent, link_file_name, user, umask)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31msymlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)

    @rule(target=FilesWithXattr, 
          file = Files.filter(lambda x: x != multiple()), 
          name = st_xattr_name,
          value = st_xattr_value, 
          flag = st.sampled_from([xattr.XATTR_CREATE, xattr.XATTR_REPLACE]), 
          user = st.sampled_from(SUDO_USERS),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'set_xattr' not in self.EXCLUDE_RULES)
    def set_xattr(self, file, name, value, flag, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_set_xattr(rootdir1, file, name, value, flag, user)
        result2 = self.fsop.do_set_xattr(rootdir2, file, name, value, flag, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mset_xattr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return file

    @rule(file = FilesWithXattr.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'get_xattr' not in self.EXCLUDE_RULES)
    def remove_xattr(self, file, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_remove_xattr(rootdir1, file, user)
        result2 = self.fsop.do_remove_xattr(rootdir2, file, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mremove_xattr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(user = st.sampled_from(USERS).filter(lambda x: x != 'root'), 
          group = st.sampled_from(GROUPS),
          groups = st.lists(st.sampled_from(GROUPS), unique=True),
          )
    @precondition(lambda self: 'change_groups' not in self.EXCLUDE_RULES)
    def change_groups(self, user, group, groups):
        self.fsop.do_change_groups(user, group, groups)

    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          mode = st_entry_mode, 
          user = st.sampled_from(SUDO_USERS),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'chmod' not in self.EXCLUDE_RULES)
    def chmod(self, entry, mode, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_chmod(rootdir1, entry, mode, user)
        result2 = self.fsop.do_chmod(rootdir2, entry, mode, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mchmod:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'get_acl' not in self.EXCLUDE_RULES)
    def get_acl(self, entry, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        assume(common.support_acl(rootdir1) and common.support_acl(rootdir2))
        result1 = self.fsop.do_get_acl(rootdir1, entry)
        result2 = self.fsop.do_get_acl(rootdir2, entry)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mget_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    
    @rule(entry = EntryWithACL.filter(lambda x: x != multiple()), 
          option = st.sampled_from(['--remove-all', '--remove-default']),
          user = st.sampled_from(SUDO_USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'remove_acl' not in self.EXCLUDE_RULES )
    def remove_acl(self, entry, option, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        assume(common.support_acl(rootdir1) and common.support_acl(rootdir2))
        result1 = self.fsop.do_remove_acl(rootdir1, entry, option, user)
        result2 = self.fsop.do_remove_acl(rootdir2, entry, option, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mremove_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
          target=EntryWithACL,
          sudo_user = st.sampled_from(SUDO_USERS),
          entry = Entries.filter(lambda x: x != multiple()), 
          user=st.sampled_from(USERS+['']),
          user_perm = st.sets(st.sampled_from(['r', 'w', 'x', ''])),
          group=st.sampled_from(GROUPS+['']),
          group_perm = st.sets(st.sampled_from(['r', 'w', 'x'])),
          other_perm = st.sets(st.sampled_from(['r', 'w', 'x', ''])),
          set_mask = st.booleans(),
          mask = st.sets(st.sampled_from(['r', 'w', 'x', ''])),
          default = st.booleans(),
          recursive = st.booleans(),
          recalc_mask = st.booleans(),
          not_recalc_mask = st.booleans(),
          logical = st.booleans(),
          physical = st.booleans(),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'set_acl' not in self.EXCLUDE_RULES)
    def set_acl(self, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        assume(common.support_acl(rootdir1) and common.support_acl(rootdir2))
        result1 = self.fsop.do_set_acl(rootdir1, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        result2 = self.fsop.do_set_acl(rootdir2, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mset_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return entry

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          access_time=st_time, 
          modify_time=st_time, 
          follow_symlinks=st.booleans(), 
          user = st.sampled_from(USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
            )
    @precondition(lambda self: 'utime' not in self.EXCLUDE_RULES)
    def utime(self, entry, access_time, modify_time, follow_symlinks, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_utime(rootdir1, entry, access_time, modify_time, follow_symlinks, user)
        result2 = self.fsop.do_utime(rootdir2, entry, access_time, modify_time, follow_symlinks, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mutime:\nresult1 is {result1}\nresult2 is {result2}\033[0m'


    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          owner= st.sampled_from(USERS), 
          user = st.sampled_from(USERS), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
        )
    @precondition(lambda self: 'chown' not in self.EXCLUDE_RULES)
    def chown(self, entry, owner, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2, user='root'):
        result1 = self.fsop.do_chown(rootdir1, entry, owner, user)
        result2 = self.fsop.do_chown(rootdir2, entry, owner, user)
        assert self.equal(result1, result2, rootdir1, rootdir2), f'\033[31mchown:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
     
    @rule( dir =Folders, 
          vdirs = st.integers(min_value=2, max_value=31), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
        )
    @precondition(lambda self: 'split_dir' not in self.EXCLUDE_RULES)
    def split_dir(self, dir, vdirs, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        self.fsop.do_split_dir(rootdir1, dir, vdirs)
        self.fsop.do_split_dir(rootdir2, dir, vdirs)
    
    @rule(dir = Folders, 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
        )
    @precondition(lambda self: 'merge_dir' not in self.EXCLUDE_RULES)
    def merge_dir(self, dir, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        self.fsop.do_merge_dir(rootdir1, dir)
        self.fsop.do_merge_dir(rootdir2, dir)
    
    @rule(dir = Folders,
          zone1=st.sampled_from(ZONES1),
          zone2=st.sampled_from(ZONES2),
          is_vdir=st.booleans(), 
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
        )
    @precondition(lambda self: 'rebalance_dir' not in self.EXCLUDE_RULES)
    def rebalance_dir(self, dir, zone1, zone2, is_vdir, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        self.fsop.do_rebalance(rootdir1, dir, zone1, is_vdir)
        self.fsop.do_rebalance(rootdir2, dir, zone2, is_vdir)

    @rule(file = Files, 
          zone1=st.sampled_from(ZONES1),
          zone2=st.sampled_from(ZONES2),
          rootdir1 = st.sampled_from(ROOT_DIR1),
          rootdir2 = st.sampled_from(ROOT_DIR2)
          )
    @precondition(lambda self: 'rebalance_file' not in self.EXCLUDE_RULES )
    def rebalance_file(self, file, zone1, zone2, rootdir1=DEFALUT_ROOT_DIR1, rootdir2=DEFALUT_ROOT_DIR2):
        self.fsop.do_rebalance(rootdir1, file, zone1, False)
        self.fsop.do_rebalance(rootdir2, file, zone2, False)

    def teardown(self):
        pass
        # if COMPARE and os.path.exists(ROOT_DIR1):
        #     common.compare_content(ROOT_DIR1, ROOT_DIR2)
        #     common.compare_stat(ROOT_DIR1, ROOT_DIR2)
        #     common.compare_acl(ROOT_DIR1, ROOT_DIR2)

if __name__ == '__main__':
    MAX_EXAMPLE=int(os.environ.get('MAX_EXAMPLE', '100'))
    STEP_COUNT=int(os.environ.get('STEP_COUNT', '50'))
    settings.register_profile("dev", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("schedule", max_examples=2000, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=200, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("pull_request", max_examples=100, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=50, deadline=None, \
        report_multiple_bugs=False, derandomize=True, 
        phases=[Phase.reuse, Phase.generate, Phase.target, Phase.shrink, Phase.explain])
    settings.register_profile("generate", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, \
        phases=[Phase.generate, Phase.target])
    
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    juicefs_machine = JuicefsMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FsOperation.stats.get(), sort_keys=True, indent=4))
