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
from hypothesis import HealthCheck, assume, reproduce_failure, strategies as st, settings, Verbosity
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
    ROOT_DIR1=os.environ.get('ROOT_DIR1', '/tmp/fsrand').rstrip('/')
    ROOT_DIR2=os.environ.get('ROOT_DIR2', '/tmp/jfs/fsrand').rstrip('/')
    log_level = os.environ.get('LOG_LEVEL', 'INFO')
    loggers = {f'{ROOT_DIR1}': common.setup_logger(f'./log1', 'logger1', log_level), \
            f'{ROOT_DIR2}': common.setup_logger(f'./log2', 'logger2', log_level)}
    fsop = FsOperation(loggers)
    ZONES = {ROOT_DIR1:common.get_zones(ROOT_DIR1), ROOT_DIR2:common.get_zones(ROOT_DIR2)}
    SUDO_USERS = ['root']
    USERS=['root', 'user1', 'user2','user3']
    GROUPS = USERS+['group1', 'group2', 'group3', 'group4']
    group_created = False
    INCLUDE_RULES = []
    EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file', \
                        'clone_cp_file', 'clone_cp_dir']
    # EXCLUDE_RULES = ['rebalance_dir', 'rebalance_file', 'clone_cp_file', 'clone_cp_dir', \
    #                  'write', 'fallocate', 'truncate', 'symlink', 'hardlink', 'rename_file', \
    #                 'rename_dir', 'unlink', 'rmdir', 'read']
    @initialize(target=Folders)
    def init_folders(self):
        if not os.path.exists(self.ROOT_DIR1):
            os.makedirs(self.ROOT_DIR1)
        if not os.path.exists(self.ROOT_DIR2):
            os.makedirs(self.ROOT_DIR2)
        if os.environ.get('PROFILE', 'dev') != 'generate':
            common.clean_dir(self.ROOT_DIR1)
            common.clean_dir(self.ROOT_DIR2)
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

    def equal(self, result1, result2):
        if os.getenv('PROFILE', 'dev') == 'generate':
            return True
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

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        # os.setegid(pwd.getpwnam(user).pw_gid)

    @rule(file = Files.filter(lambda x: x != multiple()), 
          flags = st_open_flags, 
          umask = st_umask,
          mode = st_entry_mode,
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'open' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'open' in self.INCLUDE_RULES)
    )
    def open(self, file, flags, mode, user='root', umask=0o022):
        result1 = self.fsop.do_open(self.ROOT_DIR1, file, flags, umask, mode, user)
        result2 = self.fsop.do_open(self.ROOT_DIR2, file, flags, umask, mode, user)
        assert self.equal(result1, result2), f'\033[31mopen:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file = Files.filter(lambda x: x != multiple()), 
          offset = st_offset, 
          content = st_content,
          flags = st_open_flags,
          whence = st.sampled_from([os.SEEK_SET, os.SEEK_CUR, os.SEEK_END]),
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'write' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'write' in self.INCLUDE_RULES)
    )
    def write(self, file, offset, content, flags, whence, user='root'):
        result1 = self.fsop.do_write(self.ROOT_DIR1, file, offset, content, flags, whence, user)
        result2 = self.fsop.do_write(self.ROOT_DIR2, file, offset, content, flags, whence, user)
        assert self.equal(result1, result2), f'\033[31mwrite:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file = Files.filter(lambda x: x != multiple()),
          offset = st.integers(min_value=0, max_value=MAX_FILE_SIZE),
          length = st.integers(min_value=0, max_value=MAX_FALLOCATE_LENGTH),
          mode = st.just(0), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'fallocate' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'fallocate' in self.INCLUDE_RULES)
    )
    def fallocate(self, file, offset, length, mode, user='root'):
        result1 = self.fsop.do_fallocate(self.ROOT_DIR1, file, offset, length, mode, user)
        result2 = self.fsop.do_fallocate(self.ROOT_DIR2, file, offset, length, mode, user)
        assert self.equal(result1, result2), f'\033[31mfallocate:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule( file = Files.filter(lambda x: x != multiple()), 
          offset = st_offset, 
          length = st.integers(min_value=0, max_value=MAX_FILE_SIZE), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'read' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'read' in self.INCLUDE_RULES)
    )
    def read(self, file, offset, length, user='root'):
        result1 = self.fsop.do_read(self.ROOT_DIR1, file, offset, length, user)
        result2 = self.fsop.do_read(self.ROOT_DIR2, file, offset, length, user)
        assert self.equal(result1, result2), f'\033[31mread:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    

    @rule(file=Files.filter(lambda x: x != multiple()), 
          size=st.integers(min_value=0, max_value=MAX_TRUNCATE_LENGTH), 
          user=st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'truncate' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'truncate' in self.INCLUDE_RULES)
    )
    def truncate(self, file, size, user='root'):
        result1 = self.fsop.do_truncate(self.ROOT_DIR1, file, size, user)
        result2 = self.fsop.do_truncate(self.ROOT_DIR2, file, size, user)
        assert self.equal(result1, result2), f'\033[31mtruncate:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
    
    @rule(target=Files, 
          parent = Folders.filter(lambda x: x != multiple()), 
          file_name = st_entry_name, 
          mode = st_open_mode, 
          content = st_content, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'create_file' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'create_file' in self.INCLUDE_RULES)
    )
    def create_file(self, parent, file_name, content, mode='x', user='root', umask=0o022):
        result1 = self.fsop.do_create_file(self.ROOT_DIR1, parent, file_name, mode, content, user, umask)
        result2 = self.fsop.do_create_file(self.ROOT_DIR2, parent, file_name, mode, content, user, umask)
        assert self.equal(result1, result2), f'\033[31mcreate_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, file_name)

    @rule(dir = Folders.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'listdir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'listdir' in self.INCLUDE_RULES)
    )
    def listdir(self, dir, user='root'):
        result1 = self.fsop.do_listdir(self.ROOT_DIR1, dir, user)
        result2 = self.fsop.do_listdir(self.ROOT_DIR2, dir, user)
        assert self.equal(result1, result2), f'\033[31mlistdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(
          target = Files,
          file = consumes(Files).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'unlink' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'unlink' in self.INCLUDE_RULES)
    )
    def unlink(self, file, user='root'):
        print(file)
        result1 = self.fsop.do_unlink(self.ROOT_DIR1, file, user)
        result2 = self.fsop.do_unlink(self.ROOT_DIR2, file, user)
        assert self.equal(result1, result2), f'\033[31munlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return file
        else:
            return multiple()
            
    @rule( target=Files, 
          entry = consumes(Files).filter(lambda x: x != multiple()),
          parent = Folders, 
          new_entry_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rename_file' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rename_file' in self.INCLUDE_RULES)
    )
    def rename_file(self, entry, parent, new_entry_name, user='root', umask=0o022):
        result1 = self.fsop.do_rename(self.ROOT_DIR1, entry, parent, new_entry_name, user, umask)
        result2 = self.fsop.do_rename(self.ROOT_DIR2, entry, parent, new_entry_name, user, umask)
        assert self.equal(result1, result2), f'\033[31mrename_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        
    @rule( target=Folders, 
          entry = consumes(Folders).filter(lambda x: x != multiple()), 
          parent = Folders, 
          new_entry_name = st_entry_name,
          user = st.sampled_from(SUDO_USERS),
          umask = st_umask)
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rename_dir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rename_dir' in self.INCLUDE_RULES)
    )
    def rename_dir(self, entry, parent, new_entry_name, user='root', umask=0o022):
        result1 = self.fsop.do_rename(self.ROOT_DIR1, entry, parent, new_entry_name, user, umask)
        result2 = self.fsop.do_rename(self.ROOT_DIR2, entry, parent, new_entry_name, user, umask)
        assert self.equal(result1, result2), f'\033[31mrename_dir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return entry
        else:
            return os.path.join(parent, new_entry_name)
        

    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_entry_name, 
          follow_symlinks = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'copy_file' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'copy_file' in self.INCLUDE_RULES)
    )
    def copy_file(self, entry, parent, new_entry_name, follow_symlinks, user='root',  umask=0o022):
        result1 = self.fsop.do_copy_file(self.ROOT_DIR1, entry, parent, new_entry_name, follow_symlinks, user, umask)
        result2 = self.fsop.do_copy_file(self.ROOT_DIR2, entry, parent, new_entry_name, follow_symlinks, user, umask)
        assert self.equal(result1, result2), f'\033[31mcopy_file:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, new_entry_name)
    
    @rule( target=Files, entry = Files.filter(lambda x: x != multiple()),
          parent = Folders.filter(lambda x: x != multiple()),
          new_entry_name = st_entry_name, 
          preserve = st.booleans(),
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'clone_cp_file' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'clone_cp_file' in self.INCLUDE_RULES)
    )
    def clone_cp_file(self, entry, parent, new_entry_name, preserve, user='root', umask=0o022):
        result1 = self.fsop.do_clone_entry(self.ROOT_DIR1, entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop.do_clone_entry(self.ROOT_DIR2, entry, parent, new_entry_name, preserve, user, umask)
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
    )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'clone_cp_dir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'clone_cp_dir' in self.INCLUDE_RULES)
    )
    def clone_cp_dir(self, entry, parent, new_entry_name, preserve, user, umask):
        result1 = self.fsop.do_clone_entry(self.ROOT_DIR1, entry, parent, new_entry_name, preserve, user, umask)
        result2 = self.fsop.do_clone_entry(self.ROOT_DIR2, entry, parent, new_entry_name, preserve, user, umask)
        assert self.equal(result1, result2), f'\033[31mclone_cp_dir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
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
          umask = st_umask )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'mkdir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'mkdir' in self.INCLUDE_RULES)
    )
    def mkdir(self, parent, subdir, mode, user='root', umask=0o022):
        result1 = self.fsop.do_mkdir(self.ROOT_DIR1, parent, subdir, mode, user, umask)
        result2 = self.fsop.do_mkdir(self.ROOT_DIR2, parent, subdir, mode, user, umask)
        assert self.equal(result1, result2), f'\033[31mmkdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, subdir)

    @rule( target = Folders,
          dir = consumes(Folders).filter(lambda x: x != multiple()),
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rmdir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rmdir' in self.INCLUDE_RULES)
    )
    def rmdir(self, dir, user='root'):
        assume(dir != '')
        result1 = self.fsop.do_rmdir(self.ROOT_DIR1, dir, user)
        result2 = self.fsop.do_rmdir(self.ROOT_DIR2, dir, user)
        assert self.equal(result1, result2), f'\033[31mrmdir:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return dir
        else:
            return multiple()

    @rule(target = Files, 
          dest_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()), 
          link_file_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask)
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'hardlink' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'hardlink' in self.INCLUDE_RULES)
    )
    def hardlink(self, dest_file, parent, link_file_name, user='root', umask=0o022):
        result1 = self.fsop.do_hardlink(self.ROOT_DIR1, dest_file, parent, link_file_name, user, umask)
        result2 = self.fsop.do_hardlink(self.ROOT_DIR2, dest_file, parent, link_file_name, user, umask)
        assert self.equal(result1, result2), f'\033[31mhardlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)
    
    @rule(target = Files , 
          dest_file = Files.filter(lambda x: x != multiple()), 
          parent = Folders.filter(lambda x: x != multiple()),
          link_file_name = st_entry_name, 
          user = st.sampled_from(SUDO_USERS), 
          umask = st_umask )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'symlink' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'symlink' in self.INCLUDE_RULES)
    )
    def symlink(self, dest_file, parent, link_file_name, user='root', umask=0o022):
        result1 = self.fsop.do_symlink(self.ROOT_DIR1, dest_file, parent, link_file_name, user, umask)
        result2 = self.fsop.do_symlink(self.ROOT_DIR2, dest_file, parent, link_file_name, user, umask)
        assert self.equal(result1, result2), f'\033[31msymlink:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return os.path.join(parent, link_file_name)

    @rule(target=FilesWithXattr, 
          file = Files.filter(lambda x: x != multiple()), 
          name = st_xattr_name,
          value = st_xattr_value, 
          flag = st.sampled_from([xattr.XATTR_CREATE]), 
          user = st.sampled_from(SUDO_USERS)
        )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'set_xattr' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'set_xattr' in self.INCLUDE_RULES)
    )
    def set_xattr(self, file, name, value, flag, user='root'):
        # assert '\x00' not in name, f'xattr name should not include \x00'
        result1 = self.fsop.do_set_xattr(self.ROOT_DIR1, file, name, value, flag, user)
        result2 = self.fsop.do_set_xattr(self.ROOT_DIR2, file, name, value, flag, user)
        assert self.equal(result1, result2), f'\033[31mset_xattr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return file

    @rule(file = FilesWithXattr.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'list_xattr' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'list_xattr' in self.INCLUDE_RULES)
    )
    def list_xattr(self, file, user='root'):
        result1 = self.fsop.do_list_xattr(self.ROOT_DIR1, file, user)
        result2 = self.fsop.do_list_xattr(self.ROOT_DIR2, file, user)
        assert self.equal(result1, result2), f'\033[31mlist_xattr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'


    @rule(file = FilesWithXattr.filter(lambda x: x != multiple()), 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'get_xattr' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'get_xattr' in self.INCLUDE_RULES)
    )
    def remove_xattr(self, file, user='root'):
        result1 = self.fsop.do_remove_xattr(self.ROOT_DIR1, file, user)
        result2 = self.fsop.do_remove_xattr(self.ROOT_DIR2, file, user)
        assert self.equal(result1, result2), f'\033[31mremove_xattr:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(user = st.sampled_from(USERS).filter(lambda x: x != 'root'), 
          group = st.sampled_from(GROUPS),
          groups = st.lists(st.sampled_from(GROUPS), unique=True))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'change_groups' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'change_groups' in self.INCLUDE_RULES)
    )
    def change_groups(self, user, group, groups):
        self.fsop.do_change_groups(self.ROOT_DIR1, user, group, groups)
        self.fsop.do_change_groups(self.ROOT_DIR2, user, group, groups)

    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          mode = st_entry_mode, 
          user = st.sampled_from(SUDO_USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'chmod' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'chmod' in self.INCLUDE_RULES)
    )
    def chmod(self, entry, mode, user='root'):
        result1 = self.fsop.do_chmod(self.ROOT_DIR1, entry, mode, user)
        result2 = self.fsop.do_chmod(self.ROOT_DIR2, entry, mode, user)
        assert self.equal(result1, result2), f'\033[31mchmod:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    @rule(entry = Entries.filter(lambda x: x != multiple()))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'get_acl' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'get_acl' in self.INCLUDE_RULES) \
                  and common.support_acl(self.ROOT_DIR1) and common.support_acl(self.ROOT_DIR2) 
    )
    def get_acl(self, entry):
        result1 = self.fsop.do_get_acl(self.ROOT_DIR1, entry)
        result2 = self.fsop.do_get_acl(self.ROOT_DIR2, entry)
        assert self.equal(result1, result2), f'\033[31mget_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

    
    @rule(entry = EntryWithACL.filter(lambda x: x != multiple()), 
          option = st.sampled_from(['--remove-all', '--remove-default']),
          user = st.sampled_from(SUDO_USERS)
          )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'remove_acl' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'remove_acl' in self.INCLUDE_RULES) \
                  and common.support_acl(self.ROOT_DIR1) and common.support_acl(self.ROOT_DIR2) 
    )
    def remove_acl(self, entry: str, option: str, user='root'):
        result1 = self.fsop.do_remove_acl(self.ROOT_DIR1, entry, option, user)
        result2 = self.fsop.do_remove_acl(self.ROOT_DIR2, entry, option, user)
        assert self.equal(result1, result2), f'\033[31mremove_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'

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
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'set_acl' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'set_acl' in self.INCLUDE_RULES) \
                  and common.support_acl(self.ROOT_DIR1) and common.support_acl(self.ROOT_DIR2) 
    )
    def set_acl(self, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical):
        result1 = self.fsop.do_set_acl(self.ROOT_DIR1, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        result2 = self.fsop.do_set_acl(self.ROOT_DIR2, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical)
        assert self.equal(result1, result2), f'\033[31mset_acl:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
        if isinstance(result1, Exception):
            return multiple()
        else:
            return entry

    @rule(entry = Entries.filter(lambda x: x != multiple()),
          access_time=st_time, 
          modify_time=st_time, 
          follow_symlinks=st.booleans(), 
          user = st.sampled_from(USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'utime' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'utime' in self.INCLUDE_RULES)
    )
    def utime(self, entry, access_time, modify_time, follow_symlinks, user='root'):
        result1 = self.fsop.do_utime(self.ROOT_DIR1, entry, access_time, modify_time, follow_symlinks, user)
        result2 = self.fsop.do_utime(self.ROOT_DIR2, entry, access_time, modify_time, follow_symlinks, user)
        assert self.equal(result1, result2), f'\033[31mutime:\nresult1 is {result1}\nresult2 is {result2}\033[0m'


    @rule(entry = Entries.filter(lambda x: x != multiple()), 
          owner= st.sampled_from(USERS), 
          user = st.sampled_from(USERS))
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'chown' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'chown' in self.INCLUDE_RULES)
    )
    def chown(self, entry, owner, user='root'):
        result1 = self.fsop.do_chown(self.ROOT_DIR1, entry, owner, user)
        result2 = self.fsop.do_chown(self.ROOT_DIR2, entry, owner, user)
        assert self.equal(result1, result2), f'\033[31mchown:\nresult1 is {result1}\nresult2 is {result2}\033[0m'
     
    @rule( dir =Folders, vdirs = st.integers(min_value=2, max_value=31) )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'split_dir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'split_dir' in self.INCLUDE_RULES) \
                    and (common.is_jfs(self.ROOT_DIR1) or common.is_jfs(self.ROOT_DIR2))
    )
    def split_dir(self, dir, vdirs):
        self.fsop.do_split_dir(self.ROOT_DIR1, dir, vdirs)
        self.fsop.do_split_dir(self.ROOT_DIR2, dir, vdirs)
    

    @rule(dir = Folders)
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'merge_dir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'merge_dir' in self.INCLUDE_RULES) \
                   and (common.is_jfs(self.ROOT_DIR1) or common.is_jfs(self.ROOT_DIR2))
    )
    def merge_dir(self, dir):
        self.fsop.do_merge_dir(self.ROOT_DIR1, dir)
        self.fsop.do_merge_dir(self.ROOT_DIR2, dir)
    
    @rule(dir = Folders,
          zone1=st.sampled_from(ZONES[ROOT_DIR1]),
          zone2=st.sampled_from(ZONES[ROOT_DIR2]),
          is_vdir=st.booleans())
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rebalance_dir' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rebalance_dir' in self.INCLUDE_RULES) \
                   and (common.is_jfs(self.ROOT_DIR1) or common.is_jfs(self.ROOT_DIR2))
    )
    def rebalance_dir(self, dir, zone1, zone2, is_vdir):
        self.fsop.do_rebalance(self.ROOT_DIR1, dir, zone1, is_vdir)
        self.fsop.do_rebalance(self.ROOT_DIR2, dir, zone2, is_vdir)

    @rule(file = Files, 
          zone1=st.sampled_from(ZONES[ROOT_DIR1]),
          zone2=st.sampled_from(ZONES[ROOT_DIR2]),
          )
    @precondition(lambda self: (len(self.EXCLUDE_RULES)>0 and 'rebalance_file' not in self.EXCLUDE_RULES)\
                   or (len(self.EXCLUDE_RULES)==0 and 'rebalance_file' in self.INCLUDE_RULES) \
                   and (common.is_jfs(self.ROOT_DIR1) or common.is_jfs(self.ROOT_DIR2))
    )
    def rebalance_file(self, file, zone1, zone2):
        self.fsop.do_rebalance(self.ROOT_DIR1, file, zone1, False)
        self.fsop.do_rebalance(self.ROOT_DIR2, file, zone2, False)

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
    settings.register_profile("schedule", max_examples=1000, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=200, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target])
    settings.register_profile("pull_request", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, 
        phases=[Phase.reuse, Phase.generate, Phase.target])
    settings.register_profile("generate", max_examples=MAX_EXAMPLE, verbosity=Verbosity.debug, 
        print_blob=True, stateful_step_count=STEP_COUNT, deadline=None, \
        report_multiple_bugs=False, \
        phases=[Phase.generate, Phase.target])
    
    profile = os.environ.get('PROFILE', 'dev')
    settings.load_profile(profile)
    juicefs_machine = JuicefsMachine.TestCase()
    juicefs_machine.runTest()
    print(json.dumps(FsOperation.stats.get(), sort_keys=True, indent=4))
