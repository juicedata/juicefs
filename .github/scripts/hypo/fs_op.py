from ast import List
import hashlib
import json
import logging
import os
import pwd
import re
import shlex
import shutil
import stat
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
from common import is_jfs, get_acl, get_root, get_stat
from typing import Dict
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
import fallocate
from context import Context
from stats import Statistics

class FsOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    def __init__(self, loggers: Dict[str, logging.Logger]):
        self.loggers = loggers

    def run_cmd(self, command:str, root_dir:str) -> str:
        self.loggers[root_dir].info(f'run_cmd: {command}')
        if '|' in command or '>' in command or '&' in command:
            ret=os.system(command)
            if ret == 0:
                return ret
            else: 
                raise Exception(f"run command {command} failed with {ret}")
        try:
            output = subprocess.run(command.split(), check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        except subprocess.CalledProcessError as e:
            raise e
        return output.stdout.decode()

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        os.setegid(pwd.getpwnam(user).pw_gid)
    
    #TODO: remove root_dir
    def handleException(self, e, root_dir, action, path, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = str(e)
        err = '\n'.join([elem.split('<FATAL>:')[-1].split('<ERROR>:')[-1] for elem in err.split('\n')])
        err = re.sub(r'\[\w+\.go:\d+\]', '', err)
        if err.find('setfacl') != -1 and err.find('\n') != -1:
            err = '\n'.join(sorted(err.split('\n')))
        self.stats.failure(action)
        self.loggers[root_dir].info(f'{action} {path} {kwargs} failed: {err}')
        return Exception(err)

    def do_open(self, root_dir, file, flags, mask, mode, user):
        self.loggers[root_dir].debug(f'do_open {root_dir} {file} {flags} {mode} {user}')
        abspath = os.path.join(root_dir, file)
        flag = 0
        fd = -1
        for f in flags:
            flag |= f
        try:
            old_mask = os.umask(mask)
            self.seteuid(user)
            fd = os.open(abspath, flags=flag, mode=mode)
        except Exception as e :
            return self.handleException(e, root_dir, 'do_open', abspath, flags=flags, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
            if fd > 0:
                os.close(fd)
        self.stats.success('do_open')
        self.loggers[root_dir].info(f'do_open {abspath} {flags} {mode} succeed')
        return get_stat(abspath)  
    
    def do_write(self, root_dir, file, offset, content, flags, whence, user):
        self.loggers[root_dir].debug(f'do_write {root_dir} {file} {offset}')
        abspath = os.path.join(root_dir, file)
        fd = -1
        flag = 0
        for f in flags:
            flag |= f
        try:
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            self.seteuid(user)
            fd = os.open(abspath, flag)
            os.lseek(fd, offset, whence)
            os.write(fd, content)
        except Exception as e :
            return self.handleException(e, root_dir, 'do_write', abspath, offset=offset, whence=whence, flag=flag, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_write')
        self.loggers[root_dir].info(f'do_write {abspath} {offset} succeed')
        return get_stat(abspath)
    

    def do_fallocate(self, root_dir, file, offset, length, mode, user):
        self.loggers[root_dir].debug(f'do_fallocate {root_dir} {file} {offset} {length} {mode}')
        abspath = os.path.join(root_dir, file)
        fd = -1
        try:
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            self.seteuid(user)
            fd = os.open(abspath, os.O_RDWR)
            fallocate.fallocate(fd, offset, length, mode)
        except Exception as e :
            return self.handleException(e, root_dir, 'do_fallocate', abspath, offset=offset, length=length, mode=mode, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_fallocate')
        self.loggers[root_dir].info(f'do_fallocate {abspath} {offset} {length} {mode} succeed')
        return get_stat(abspath)
    

    def do_read(self, root_dir, file, offset, length, user):
        self.loggers[root_dir].debug(f'do_read {root_dir} {file} {offset} {length}')
        abspath = os.path.join(root_dir, file)
        fd = -1
        try:
            size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            self.seteuid(user)
            fd = os.open(abspath, os.O_RDONLY)
            os.lseek(fd, offset, os.SEEK_SET)
            result = os.read(fd, length)
            md5sum = hashlib.md5(result).hexdigest()
        except Exception as e :
            return self.handleException(e, root_dir, 'do_read', abspath, offset=offset, length=length, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_read')
        self.loggers[root_dir].info(f'do_read {abspath} {offset} {length} succeed')
        return (md5sum, )

    def do_truncate(self, root_dir, file, size, user):
        self.loggers[root_dir].debug(f'do_truncate {root_dir} {file} {size}')
        abspath = os.path.join(root_dir, file)
        fd = -1
        try:
            self.seteuid(user)
            fd = os.open(abspath, os.O_WRONLY | os.O_TRUNC)
            os.ftruncate(fd, size)
        except Exception as e :
            return self.handleException(e, root_dir, 'do_truncate', abspath, size=size, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_truncate')
        self.loggers[root_dir].info(f'do_truncate {abspath} {size} succeed')
        return get_stat(abspath)

    def do_create_file(self, root_dir, parent, file_name, mode, content, user, umask):
        relpath = os.path.join(parent, file_name)
        abspath = os.path.join(root_dir, relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            with open(abspath, mode) as file:
                file.write(str(content))
        except Exception as e :
            return self.handleException(e, root_dir, 'do_create_file', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.isfile(abspath), f'do_create_file: {abspath} with mode {mode} should be file'
        self.stats.success('do_create_file')
        self.loggers[root_dir].info(f'do_create_file {abspath} with mode {mode} succeed')
        return get_stat(abspath)
    
    def do_mkfifo(self, root_dir, parent, file_name, mode, user, umask):
        abspath = os.path.join(root_dir, parent, file_name)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            os.mkfifo(abspath, mode)
        except Exception as e :
            return self.handleException(e, root_dir, 'do_mkfifo', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.exists(abspath), f'do_mkfifo: {abspath} should exist'
        assert stat.S_ISFIFO(os.stat(abspath).st_mode), f'do_mkfifo: {abspath} should be fifo'
        self.stats.success('do_mkfifo')
        self.loggers[root_dir].info(f'do_mkfifo {abspath} succeed')
        return get_stat(abspath)
    
    def do_listdir(self, root_dir, dir, user):
        abspath = os.path.join(root_dir, dir)
        try:
            self.seteuid(user)
            li = os.listdir(abspath) 
            li = sorted(list(filter(lambda x: x not in self.JFS_CONTROL_FILES, li)))
        except Exception as e:
            return self.handleException(e, root_dir, 'do_listdir', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_listdir')
        self.loggers[root_dir].info(f'do_listdir {abspath} succeed')
        return tuple(li)

    def do_unlink(self, root_dir, file, user):
        abspath = os.path.join(root_dir, file)
        try:
            self.seteuid(user)
            os.unlink(abspath)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_unlink', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        assert not os.path.exists(abspath), f'do_unlink: {abspath} should not exist'
        self.stats.success('do_unlink')
        self.loggers[root_dir].info(f'do_unlink {abspath} succeed')
        return () 

    def do_rename(self, root_dir, entry, parent, new_entry_name, user, umask):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            os.rename(abspath, new_abspath)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_rename', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        # if abspath != new_abspath:
        #     assert not os.path.exists(abspath), f'do_rename: {abspath} should not exist'
        assert os.path.lexists(new_abspath), f'do_rename: {new_abspath} should exist'
        self.stats.success('do_rename')
        self.loggers[root_dir].info(f'do_rename {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_copy_file(self, root_dir, entry, parent, new_entry_name, follow_symlinks, user, umask):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            shutil.copy(abspath, new_abspath, follow_symlinks=follow_symlinks)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_copy_file', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), f'do_copy_file: {new_abspath} should exist'
        self.stats.success('do_copy_file')
        self.loggers[root_dir].info(f'do_copy_file {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_clone_entry(self, root_dir:str, entry, parent, new_entry_name, preserve, user='root', umask=0o022, mount='cmd/mount/mount'):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            if is_jfs(abspath):
                if preserve:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath} --preserve', root_dir)
                else:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath}', root_dir)
            else:
                if preserve:
                    self.run_cmd(f'sudo -u {user} cp  {abspath} {new_abspath} -L --preserve=all', root_dir)
                else:
                    self.run_cmd(f'sudo -u {user} cp  {abspath} {new_abspath} -L', root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, root_dir, 'do_clone_entry', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), f'do_clone_entry: {new_abspath} should exist'
        self.stats.success('do_clone_entry')
        self.loggers[root_dir].info(f'do_clone_entry {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)
    
    def do_copy_tree(self, root_dir, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok, user, umask):
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            shutil.copytree(abspath, new_abspath, \
                            symlinks=symlinks, \
                            ignore_dangling_symlinks=ignore_dangling_symlinks, \
                            dirs_exist_ok=dir_exist_ok)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_copy_tree', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.lexists(new_abspath), f'do_copy_tree: {new_abspath} should exist'
        self.stats.success('do_copy_tree')
        self.loggers[root_dir].info(f'do_copy_tree {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_mkdir(self, root_dir, parent, subdir, mode, user, umask):
        relpath = os.path.join(parent, subdir)
        abspath = os.path.join(root_dir, relpath)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.mkdir(abspath, mode)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_mkdir', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.isdir(abspath), f'do_mkdir: {abspath} should be dir'
        self.stats.success('do_mkdir')
        self.loggers[root_dir].info(f'do_mkdir {abspath} with mode {oct(mode)} succeed')
        return get_stat(abspath)
    
    def do_rmdir(self, root_dir, dir, user ):
        abspath = os.path.join(root_dir, dir)
        try:
            self.seteuid(user)
            os.rmdir(abspath)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_rmdir', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        assert not os.path.exists(abspath), f'do_rmdir: {abspath} should not exist'
        self.stats.success('do_rmdir')
        self.loggers[root_dir].info(f'do_rmdir {abspath} succeed')
        return ()

    def do_hardlink(self, root_dir, dest_file, parent, link_file_name, user, umask):
        dest_abs_path = os.path.join(root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(root_dir, link_rel_path)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.link(dest_abs_path, link_abs_path)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_hardlink', dest_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        # TODO: fix me
        # time.sleep(0.005)
        assert os.path.lexists(link_abs_path), f'do_hardlink: {link_abs_path} should exist'
        self.stats.success('do_hardlink')
        self.loggers[root_dir].info(f'do_hardlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)

    def do_symlink(self, root_dir, dest_file, parent, link_file_name, user, umask):
        dest_abs_path = os.path.join(root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(root_dir, link_rel_path)
        relative_path = os.path.relpath(dest_abs_path, os.path.dirname(link_abs_path))
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.symlink(relative_path, link_abs_path)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_symlink', dest_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.islink(link_abs_path), f'do_symlink: {link_abs_path} should be link'
        self.stats.success('do_symlink')
        self.loggers[root_dir].info(f'do_symlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)
    
    def do_set_xattr(self, root_dir, file, name, value, flag, user):
        abspath = os.path.join(root_dir, file)
        try:
            self.seteuid(user)
            xattr.setxattr(abspath, 'user.'+name, value, flag)
            # self.run_cmd(f'sudo -u {user} setfattr -n user.{name} -v {value} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_set_xattr', abspath, name=name, value=value, flag=flag, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_set_xattr')
        self.loggers[root_dir].info(f"do_set_xattr {abspath} user.{name} {value} {flag} succeed")
        v = xattr.getxattr(abspath, 'user.'+name)
        return (v,)

    def do_list_xattr(self, root_dir, file, user):
        abspath = os.path.join(root_dir, file)
        xattr_list = []
        try:
            self.seteuid(user)
            xattrs = xattr.listxattr(abspath)
            xattr_list = []
            for attr in xattrs:
                value = xattr.getxattr(abspath, attr)
                xattr_list.append((attr, value))
            xattr_list.sort()  # Sort the list based on xattr names
        except Exception as e:
            return self.handleException(e, root_dir, 'do_list_xattr', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_list_xattr')
        self.loggers[f'{root_dir}'].info(f"do_list_xattr {abspath} succeed")
        return xattr_list

    def do_remove_xattr(self, root_dir, file, user):
        abspath = os.path.join(root_dir, file)
        try:
            name = ''
            names = sorted(xattr.listxattr(abspath))
            if len(names) > 0:
                name = names[0]
            self.seteuid(user)
            if name != '':
                xattr.removexattr(abspath, name)
            # self.run_cmd(f'sudo -u {user} setfattr -x user.{name} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_remove_xattr', abspath, name=name, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_remove_xattr')
        self.loggers[f'{root_dir}'].info(f"do_remove_xattr {abspath} {name} succeed")
        assert name not in xattr.listxattr(abspath), f'do_remove_xattr: {name} should not in xattr list'
        return tuple(sorted(xattr.listxattr(abspath)))
    
    def do_change_groups(self, root_dir, user, group, groups):
        try:
            subprocess.run(['usermod', '-g', group, '-G', ",".join(groups), user], check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        except subprocess.CalledProcessError as e:
            self.stats.failure('do_change_groups')
            self.loggers[root_dir].info(f"do_change_groups {user} {group} {groups} failed: {e.output.decode()}")
            return
        self.stats.success('do_change_groups')
        self.loggers[root_dir].info(f"do_change_groups {user} {group} {groups} succeed")

    def do_chmod(self, root_dir, entry, mode, user):
        abspath = os.path.join(root_dir, entry)
        try:
            self.seteuid(user)
            os.chmod(abspath, mode)
            # self.run_cmd(f'sudo -u {user} chmod {mode} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_chmod', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_chmod')
        self.loggers[root_dir].info(f"do_chmod {abspath} {oct(mode)} {user} succeed")
        return get_stat(abspath)

    def do_get_acl(self, root_dir: str, entry: str):
        abspath = os.path.join(root_dir, entry)
        try:
            acl = get_acl(abspath)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_get_acl', abspath)
        self.stats.success('do_get_acl')
        self.loggers[f'{root_dir}'].info(f"do_get_acl {abspath} succeed")
        return acl

    def do_remove_acl(self, root_dir: str, entry: str, option: str, user: str):
        abspath = os.path.join(root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} setfacl {option} {abspath} ', root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, root_dir, 'do_remove_acl', abspath, option=option,user=user)
        self.stats.success('do_remove_acl')
        self.loggers[root_dir].info(f"do_remove_acl {abspath} with {option} succeed")
        return get_acl(abspath)
    
    def do_set_acl(self, root_dir, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical):
        abspath = os.path.join(root_dir, entry)
        user_perm = ''.join(user_perm) == '' and '-' or ''.join(user_perm)
        group_perm = ''.join(group_perm) == '' and '-' or ''.join(group_perm)
        other_perm = ''.join(other_perm) == '' and '-' or ''.join(other_perm)
        mask = ''.join(mask) == '' and '-' or ''.join(mask)
        default = default and '-d' or ''
        recursive = recursive and '-R' or ''
        recalc_mask = recalc_mask and '--mask' or ''
        not_recalc_mask = not_recalc_mask and '--no-mask' or ''
        logical = (recursive and logical) and '-L' or ''
        physical = (recursive and physical) and '-P' or ''
        try:
            text = f'u:{user}:{user_perm},g:{group}:{group_perm},o::{other_perm}'
            if set_mask:
                text += f',m::{mask}'
            self.run_cmd(f'sudo -u {sudo_user} setfacl {default} {recursive} {recalc_mask} {not_recalc_mask} {logical} {physical} -m {text} {abspath}', root_dir)
            acl = get_acl(abspath)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, root_dir, 'do_set_acl', abspath, user_perm=user_perm, group_perm=group_perm, other_perm=other_perm)
        self.stats.success('do_set_acl')
        self.loggers[f'{root_dir}'].info(f"do_set_acl {abspath} with {text} succeed")
        return (acl,)

    def do_utime(self, root_dir, entry, access_time, modify_time, follow_symlinks, user):
        abspath = os.path.join(root_dir, entry)
        try:
            self.seteuid(user)
            os.utime(abspath, (access_time, modify_time), follow_symlinks=follow_symlinks)
            # self.run_cmd(f'sudo -u {user} touch -a -t {access_time} {abspath}', root_dir)
            # self.run_cmd(f'sudo -u {user} touch -m -t {modify_time} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_utime', abspath, access_time=access_time, modify_time=modify_time, follow_symlinks=follow_symlinks, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_utime')
        self.loggers[root_dir].info(f"do_utime {abspath} {access_time} {modify_time} succeed")
        return get_stat(abspath)

    def do_chown(self, root_dir, entry, owner, user):
        abspath = os.path.join(root_dir, entry)
        info = pwd.getpwnam(owner)
        uid = info.pw_uid
        gid = info.pw_gid
        try:
            self.seteuid(user)
            os.chown(abspath, uid, gid)
            # self.run_cmd(f'sudo -u {user} chown {owner} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, root_dir, 'do_chown', abspath, owner=owner, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_chown')
        self.loggers[root_dir].info(f"do_chown {abspath} {owner} succeed")
        return get_stat(abspath)

    def do_split_dir(self, root_dir, dir, vdirs):
        relpath = os.path.join(dir, f'.jfs_split#{vdirs}')
        abspath = os.path.join(root_dir, relpath)
        if not is_jfs(abspath):
            return 
        try:
            subprocess.check_call(['touch', abspath])
        except Exception as e:
            self.stats.failure('do_split_dir')
            self.loggers[root_dir].info(f"do_split_dir {abspath} {vdirs} failed: {str(e)}")
            return
        self.stats.success('do_split_dir')
        self.loggers[root_dir].info(f"do_split_dir {abspath} {vdirs} succeed")

    def do_merge_dir(self, root_dir, dir):
        relpath = os.path.join(dir, f'.jfs_split#1')
        abspath = os.path.join(root_dir, relpath)
        if not is_jfs(abspath):
            return 
        try:
            subprocess.check_call(['touch', abspath])
        except Exception as e:
            self.stats.failure('do_merge_dir')
            self.loggers[f'{root_dir}'].info(f"do_merge_dir {abspath} failed: {str(e)}")
            return
        self.stats.success('do_merge_dir')
        self.loggers[f'{root_dir}'].info(f"do_merge_dir {abspath} succeed")

    def do_rebalance(self, root_dir, entry, zone, is_vdir):
        if zone == '':
            print(f'{root_dir} is not multizoned, skip rebalance')
            return
        abspath = os.path.join(root_dir, entry)
        vdir_relpath = os.path.join(entry, '.jfs#1')
        vdir_abspath = os.path.join(root_dir, vdir_relpath)
        if is_vdir and os.path.isfile( vdir_abspath ):
            abspath = vdir_abspath
        try :
            dest = os.path.join(get_root(abspath), zone, os.path.basename(abspath.rstrip('/')))
            # print(f'rename {abspath} {dest}')
            os.rename(abspath, dest)
        except Exception as e:
            self.stats.failure('do_rebalance')
            self.loggers[root_dir].info(f"do_rebalance {abspath} {dest} failed: {str(e)}")
            return
        self.stats.success('do_rebalance')
        self.loggers[root_dir].info(f"do_rebalance {abspath} {dest} succeed")

    def do_mount(self, context:Context, mount, allow_other=True, enable_xattr=True, enable_acl=True, read_only=False, user='root'):
        command = f'sudo -u {user} {mount} mount {context.volume} {context.mp} --conf-dir={context.conf_dir} --no-update'
        if allow_other:
            command += ' -o allow_other'
        if enable_xattr:
            command += ' --enable-xattr'
        if enable_acl:
            command += ' --enable-acl'
        if read_only:
            command += ' --read-only'
        if context.cache_dir != '':
            command += f' --cache-dir={context.cache_dir}'
        try:
            output = self.run_cmd(command, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_mount', context.root_dir)
        return output
    
    def do_gateway(self, context:Context, mount, user='root'):
        command = f'sudo -u {user} MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin {mount} gateway {context.volume} {context.gateway_address} --conf-dir={context.conf_dir} --no-update &'
        try:
            self.run_cmd(command, context.root_dir)
        except Exception as e:
            return self.handleException(e, context.root_dir, 'do_gateway', context.root_dir)
        return True
    
    def get_raw(self, size:str):
        # get bytes count from '4.00 KiB (4096 Bytes)' or '3 Bytes'
        if size.find('(') > -1:
            return size.split('(')[1].split(' ')[0]
        else:
            return size.split(' ')[0]

    def parse_info(self, info: str):
        li = info.split('\n')
        filename = li[0].split(':')[0].strip()
        # assert li[0].strip().startswith('inode:'), f'parse_info: {li[0]} should start with inode:'
        # inode = li[0].split(':')[1].strip()
        assert li[2].strip().startswith('files:'), f'parse_info: {li[1]} should start with files:'
        files = li[2].split(':')[1].strip()   
        assert li[3].strip().startswith('dirs:'), f'parse_info: {li[2]} should start with dirs:'  
        dirs = li[3].split(':')[1].strip()
        assert li[4].strip().startswith('length:'), f'parse_info: {li[3]} should start with length:'
        length = li[4].split(':')[1].strip()
        length = self.get_raw(length)
        assert li[5].strip().startswith('size:'), f'parse_info: {li[4]} should start with size:'
        size = li[5].split(':')[1].strip()
        size = self.get_raw(size)
        assert li[6].strip().startswith('path'), f'parse_info: {li[5]} should start with path:'
        paths = []
        if li[6].strip().startswith('path:'):
            paths.append(li[6].split(':')[1].strip())
        elif li[6].strip().startswith('paths:'):
            for i in range(7, len(li)):
                if li[i].strip().startswith('/'):
                    paths.append(li[i].strip())
                else:
                    break
        paths = ','.join(paths)
        return filename, files, dirs, length, size, paths

    def do_info(self, context:Context, mount, entry, user='root', **kwargs):
        abs_path = os.path.join(context.root_dir, entry)
        try:
            cmd = f'sudo -u {user} {mount} info {abs_path}'
            if kwargs.get('raw', True):
                cmd += ' --raw'
            if kwargs.get('recuisive', False):
                cmd += ' --recursive'
            result = self.run_cmd(cmd, context.root_dir)
            if '<ERROR>:' in result or "permission denied" in result:
                return self.handleException(Exception(result), context.root_dir, 'do_info', abs_path, **kwargs)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_info', abs_path)
        result = self.parse_info(result)
        self.stats.success('do_info')
        self.loggers[context.root_dir].info(f'do_info {abs_path} succeed')
        return result 
    
    def do_rmr(self, context:Context, entry, mount, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            result = self.run_cmd(f'sudo -u {user} {mount} rmr {abspath}', context.root_dir)
            if '<ERROR>:' in result:
                return self.handleException(Exception(result), context.root_dir, 'do_rmr', abspath)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_rmr', abspath)
        assert not os.path.exists(abspath), f'do_rmr: {abspath} should not exist'
        self.stats.success('do_rmr')
        self.loggers[context.root_dir].info(f'do_rmr {abspath} succeed')
        return True
    
    def do_status(self, context:Context, mount, user='root'):
        try:
            result = self.run_cmd(f'sudo -u {user} {mount} status {context.volume} --conf-dir={context.conf_dir}', context.root_dir)
            result = json.loads(result)['Setting']
            # TODO: check why, should remove this line.
            if 'tested' in result:
                del result['tested']
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_status', '')
        self.stats.success('do_status')
        self.loggers[context.root_dir].info(f'do_status succeed')
        return result['rootname'], result['password'], result['uuid'], result['storage'], \
            result['token'], result['accesskey'], result['secretkey'], \
            result['blockSize'], result['partitions'], result['compress']
    
    def do_dump(self, context:Context, entry, mount, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            result = self.run_cmd(f'sudo -u {user} {mount} dump {abspath}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_dump', abspath)
        self.stats.success('do_dump')
        self.loggers[context.root_dir].info(f'do_dump {abspath} succeed')
        return result

    def do_warmup(self, context:Context, entry, mount, user='root'):
        abspath = os.path.join(context.root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} {mount} warmup {abspath}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_warmup', abspath)
        self.stats.success('do_warmup')
        self.loggers[context.root_dir].info(f'do_warmup {abspath} succeed')
        return True

    def do_import(self, context:Context, mount, src_uri, dest_path, mode, user='root'):
        abspath = os.path.join(context.root_dir, dest_path)
        try:
            self.run_cmd(f'sudo -u {user} {mount} import {src_uri} {abspath} --mode {mode} --conf-dir={context.conf_dir}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_import', abspath, src_uri=src_uri)
        self.stats.success('do_import')
        self.loggers[context.root_dir].info(f'do_import {src_uri} succeed')
        # src_uri is stared with /, so we need to remove the first /
        return self.do_info(context=context, mount=mount, entry=os.path.join(dest_path, src_uri[1:]))
    
    def do_gc(self, context:Context, mount:str, delete:bool, user:str='root'):
        try:
            cmd = f'sudo -u {user} {mount} gc {context.volume} --conf-dir={context.conf_dir}'
            if delete:
                cmd += ' --delete'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_gc', '')
        self.stats.success('do_gc')
        self.loggers[context.root_dir].info(f'do_gc succeed')
        return True
    
    def do_fsck(self, context:Context, mount, repair, user='root'):
        try:
            cmd = f'sudo -u {user} {mount} fsck {context.volume} --conf-dir={context.conf_dir}'
            if repair:
                cmd += ' --repair'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_fsck', '')
        self.stats.success('do_fsck')
        self.loggers[context.root_dir].info(f'do_fsck succeed')
        return True
    
    def do_quota_set(self, context:Context, mount, path, capacity, inodes, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        print(f'relative_path is {relative_path}')
        try:
            cmd = f'sudo -u {user} {mount} quota set {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            if capacity > -1 :
                cmd += f' --capacity {capacity}'
            if inodes > -1 :
                cmd += f' --inodes {inodes}'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_set', abspath)
        self.stats.success('do_quota_set')
        self.loggers[context.root_dir].info(f'do_quota_set {abspath} succeed')
        return self.do_quota_get(context=context, mount=mount, path=path, user=user)
    
    def do_quota_delete(self, context:Context, mount, path, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        try:
            cmd = f'sudo -u {user} {mount} quota delete {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_delete', abspath)
        self.stats.success('do_quota_delete')
        self.loggers[context.root_dir].info(f'do_quota_delete {abspath} succeed')
        return True
    
    def do_quota_get(self, context:Context, mount, path, user='root'):
        abspath = os.path.join(context.root_dir, path)
        relative_path = os.path.relpath(abspath, os.path.join(context.mp))
        try:
            cmd = f'sudo -u {user} {mount} quota get {context.volume} --conf-dir {context.conf_dir} --path /{relative_path}'
            result = self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_get', abspath)
        self.stats.success('do_quota_get')
        self.loggers[context.root_dir].info(f'do_quota_get {abspath} succeed')
        return result
    
    def do_quota_list(self, context:Context, mount, user='root'):
        try:
            cmd = f'sudo -u {user} {mount} quota list {context.volume} --conf-dir {context.conf_dir}'
            result = self.run_cmd(cmd, context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_quota_list', '')
        self.stats.success('do_quota_list')
        self.loggers[context.root_dir].info(f'do_quota_list succeed')
        return result
    
    def do_trash_list(self, context:Context, user='root'):
        abspath = os.path.join(context.mp, '.trash')
        try:
            self.seteuid(user)
            li = os.listdir(abspath) 
            li = sorted(li)
        except Exception as e:
            return self.handleException(e, context.root_dir, 'do_trash_list', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_trash_list')
        self.loggers[context.root_dir].info(f'do_trash_list succeed')
        return tuple(li)
    
    def do_trash_restore(self, context:Context, index, user='root'):
        trash_list = self.do_trash_list(context=context)
        if len(trash_list) == 0:
            return ''
        index = index % len(trash_list)
        trash_file:str = trash_list[index]
        abspath = os.path.join(context.mp, '.trash', shlex.quote(trash_file))
        try:
            self.run_cmd(f'sudo -u {user} mv {abspath} {context.mp}', context.root_dir)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, context.root_dir, 'do_trash_restore', abspath, user=user)
        restored_path = os.path.join(context.mp, '/'.join(trash_file.split('|')[1:]))
        restored_path = os.path.relpath(restored_path, context.root_dir)
        self.stats.success('do_trash_restore')
        self.loggers[context.root_dir].info(f'do_trash_restore succeed')
        return restored_path
    