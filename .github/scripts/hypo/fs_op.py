import hashlib
import os
import pwd
import re
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
from stats import Statistics
import common

class FsOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    def __init__(self, name, root_dir:str):
        self.logger =common.setup_logger(f'./{name}.log', name, os.environ.get('LOG_LEVEL', 'INFO'))
        self.root_dir = root_dir.rstrip('/')

    def run_cmd(self, command:str) -> str:
        self.logger.info(f'run_cmd: {command}')
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

    def get_zones(self):
        return common.get_zones(self.root_dir)

    def is_jfs(self):
        return common.is_jfs(self.root_dir)

    def init_rootdir(self):
        if not os.path.exists(self.root_dir):
            os.makedirs(self.root_dir)
        if os.environ.get('PROFILE', 'dev') != 'generate':
            common.clean_dir(self.root_dir)

    def seteuid(self, user):
        os.seteuid(pwd.getpwnam(user).pw_uid)
        os.setegid(pwd.getpwnam(user).pw_gid)
    
    def handleException(self, e, action, path, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = str(e)
        err = '\n'.join([elem.split('<FATAL>:')[-1].split('<ERROR>:')[-1] for elem in err.split('\n')])
        err = re.sub(r'\[\w+\.go:\d+\]', '', err)
        if err.find('setfacl') != -1 and err.find('\n') != -1:
            err = '\n'.join(sorted(err.split('\n')))
        self.stats.failure(action)
        self.logger.info(f'{action} {path} {kwargs} failed: {err}')
        return Exception(err)

    def do_open(self, file, flags, mask, mode, user):
        self.logger.debug(f'do_open {self.root_dir} {file} {flags} {mode} {user}')
        abspath = os.path.join(self.root_dir, file)
        flag = 0
        fd = -1
        for f in flags:
            flag |= f
        try:
            old_mask = os.umask(mask)
            self.seteuid(user)
            fd = os.open(abspath, flags=flag, mode=mode)
        except Exception as e :
            return self.handleException(e, 'do_open', abspath, flags=flags, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
            if fd > 0:
                os.close(fd)
        self.stats.success('do_open')
        self.logger.info(f'do_open {abspath} {flags} {mode} succeed')
        return get_stat(abspath)  
    
    def do_write(self, file, offset, content, flags, whence, user):
        self.logger.debug(f'do_write {self.root_dir} {file} {offset}')
        abspath = os.path.join(self.root_dir, file)
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
            return self.handleException(e, 'do_write', abspath, offset=offset, whence=whence, flag=flag, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_write')
        self.logger.info(f'do_write {abspath} {offset} succeed')
        return get_stat(abspath)
    

    def do_fallocate(self, file, offset, length, mode, user):
        self.logger.debug(f'do_fallocate {self.root_dir} {file} {offset} {length} {mode}')
        abspath = os.path.join(self.root_dir, file)
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
            return self.handleException(e, 'do_fallocate', abspath, offset=offset, length=length, mode=mode, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_fallocate')
        self.logger.info(f'do_fallocate {abspath} {offset} {length} {mode} succeed')
        return get_stat(abspath)
    

    def do_read(self, file, offset, length, user):
        self.logger.debug(f'do_read {self.root_dir} {file} {offset} {length}')
        abspath = os.path.join(self.root_dir, file)
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
            return self.handleException(e, 'do_read', abspath, offset=offset, length=length, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_read')
        self.logger.info(f'do_read {abspath} {offset} {length} succeed')
        return (md5sum, )

    def do_truncate(self, file, size, user):
        self.logger.debug(f'do_truncate {self.root_dir} {file} {size}')
        abspath = os.path.join(self.root_dir, file)
        fd = -1
        try:
            self.seteuid(user)
            fd = os.open(abspath, os.O_WRONLY | os.O_TRUNC)
            os.ftruncate(fd, size)
        except Exception as e :
            return self.handleException(e, 'do_truncate', abspath, size=size, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_truncate')
        self.logger.info(f'do_truncate {abspath} {size} succeed')
        return get_stat(abspath)

    def do_create_file(self, parent, file_name, mode, content, user, umask):
        relpath = os.path.join(parent, file_name)
        abspath = os.path.join(self.root_dir, relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            with open(abspath, mode) as file:
                file.write(str(content))
        except Exception as e :
            return self.handleException(e, 'do_create_file', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.isfile(abspath), f'do_create_file: {abspath} with mode {mode} should be file'
        self.stats.success('do_create_file')
        self.logger.info(f'do_create_file {abspath} with mode {mode} succeed')
        return get_stat(abspath)
    
    def do_mkfifo(self, parent, file_name, mode, user, umask):
        abspath = os.path.join(self.root_dir, parent, file_name)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            os.mkfifo(abspath, mode)
        except Exception as e :
            return self.handleException(e, 'do_mkfifo', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.exists(abspath), f'do_mkfifo: {abspath} should exist'
        assert stat.S_ISFIFO(os.stat(abspath).st_mode), f'do_mkfifo: {abspath} should be fifo'
        self.stats.success('do_mkfifo')
        self.logger.info(f'do_mkfifo {abspath} succeed')
        return get_stat(abspath)
    
    def do_listdir(self, dir, user):
        abspath = os.path.join(self.root_dir, dir)
        try:
            self.seteuid(user)
            li = os.listdir(abspath) 
            li = sorted(list(filter(lambda x: x not in self.JFS_CONTROL_FILES, li)))
        except Exception as e:
            return self.handleException(e, 'do_listdir', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_listdir')
        self.logger.info(f'do_listdir {abspath} succeed')
        return tuple(li)

    def do_unlink(self, file, user):
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            os.unlink(abspath)
        except Exception as e:
            return self.handleException(e, 'do_unlink', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        assert not os.path.exists(abspath), f'do_unlink: {abspath} should not exist'
        self.stats.success('do_unlink')
        self.logger.info(f'do_unlink {abspath} succeed')
        return () 

    def do_rename(self, entry, parent, new_entry_name, user, umask):
        abspath = os.path.join(self.root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(self.root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            os.rename(abspath, new_abspath)
        except Exception as e:
            return self.handleException(e, 'do_rename', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        # if abspath != new_abspath:
        #     assert not os.path.exists(abspath), f'do_rename: {abspath} should not exist'
        assert os.path.lexists(new_abspath), f'do_rename: {new_abspath} should exist'
        self.stats.success('do_rename')
        self.logger.info(f'do_rename {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_copy_file(self, entry, parent, new_entry_name, follow_symlinks, user, umask):
        abspath = os.path.join(self.root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(self.root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            shutil.copy(abspath, new_abspath, follow_symlinks=follow_symlinks)
        except Exception as e:
            return self.handleException(e, 'do_copy_file', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), f'do_copy_file: {new_abspath} should exist'
        self.stats.success('do_copy_file')
        self.logger.info(f'do_copy_file {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_clone_entry(self,  entry, parent, new_entry_name, preserve, user='root', umask=0o022, mount='cmd/mount/mount'):
        root_dir = self.root_dir
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            if is_jfs(abspath):
                if preserve:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath} --preserve')
                else:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath}')
            else:
                if preserve:
                    self.run_cmd(f'sudo -u {user} cp  {abspath} {new_abspath} -L --preserve=all')
                else:
                    self.run_cmd(f'sudo -u {user} cp  {abspath} {new_abspath} -L')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_clone_entry', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), f'do_clone_entry: {new_abspath} should exist'
        self.stats.success('do_clone_entry')
        self.logger.info(f'do_clone_entry {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)
    
    def do_copy_tree(self, entry, parent, new_entry_name, symlinks, ignore_dangling_symlinks, dir_exist_ok, user, umask):
        abspath = os.path.join(self.root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(self.root_dir, new_relpath)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            shutil.copytree(abspath, new_abspath, \
                            symlinks=symlinks, \
                            ignore_dangling_symlinks=ignore_dangling_symlinks, \
                            dirs_exist_ok=dir_exist_ok)
        except Exception as e:
            return self.handleException(e, 'do_copy_tree', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.lexists(new_abspath), f'do_copy_tree: {new_abspath} should exist'
        self.stats.success('do_copy_tree')
        self.logger.info(f'do_copy_tree {abspath} {new_abspath} succeed')
        return get_stat(new_abspath)

    def do_mkdir(self, parent, subdir, mode, user, umask):
        relpath = os.path.join(parent, subdir)
        abspath = os.path.join(self.root_dir, relpath)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.mkdir(abspath, mode)
        except Exception as e:
            return self.handleException(e, 'do_mkdir', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.isdir(abspath), f'do_mkdir: {abspath} should be dir'
        self.stats.success('do_mkdir')
        self.logger.info(f'do_mkdir {abspath} with mode {oct(mode)} succeed')
        return get_stat(abspath)
    
    def do_rmdir(self, dir, user ):
        abspath = os.path.join(self.root_dir, dir)
        try:
            self.seteuid(user)
            os.rmdir(abspath)
        except Exception as e:
            return self.handleException(e, 'do_rmdir', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        assert not os.path.exists(abspath), f'do_rmdir: {abspath} should not exist'
        self.stats.success('do_rmdir')
        self.logger.info(f'do_rmdir {abspath} succeed')
        return ()

    def do_hardlink(self, dest_file, parent, link_file_name, user, umask):
        dest_abs_path = os.path.join(self.root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(self.root_dir, link_rel_path)
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.link(dest_abs_path, link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_hardlink', dest_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        # TODO: fix me
        # time.sleep(0.005)
        assert os.path.lexists(link_abs_path), f'do_hardlink: {link_abs_path} should exist'
        self.stats.success('do_hardlink')
        self.logger.info(f'do_hardlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)

    def do_symlink(self, dest_file, parent, link_file_name, user, umask):
        dest_abs_path = os.path.join(self.root_dir, dest_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(self.root_dir, link_rel_path)
        relative_path = os.path.relpath(dest_abs_path, os.path.dirname(link_abs_path))
        try:
            old_mask = os.umask(umask)
            self.seteuid(user)
            os.symlink(relative_path, link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_symlink', dest_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
            os.umask(old_mask)
        assert os.path.islink(link_abs_path), f'do_symlink: {link_abs_path} should be link'
        self.stats.success('do_symlink')
        self.logger.info(f'do_symlink {dest_abs_path} {link_abs_path} succeed')
        return get_stat(link_abs_path)
    
    def do_set_xattr(self, file, name, value, flag, user):
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            xattr.setxattr(abspath, 'user.'+name, value, flag)
            # self.run_cmd(f'sudo -u {user} setfattr -n user.{name} -v {value} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_set_xattr', abspath, name=name, value=value, flag=flag, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_set_xattr')
        self.logger.info(f"do_set_xattr {abspath} user.{name} {value} {flag} succeed")
        v = xattr.getxattr(abspath, 'user.'+name)
        return (v,)

    def do_list_xattr(self, file, user):
        abspath = os.path.join(self.root_dir, file)
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
            return self.handleException(e, 'do_list_xattr', abspath, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_list_xattr')
        self.logger.info(f"do_list_xattr {abspath} succeed")
        return xattr_list

    def do_remove_xattr(self, file, user):
        abspath = os.path.join(self.root_dir, file)
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
            return self.handleException(e, 'do_remove_xattr', abspath, name=name, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_remove_xattr')
        self.logger.info(f"do_remove_xattr {abspath} {name} succeed")
        assert name not in xattr.listxattr(abspath), f'do_remove_xattr: {name} should not in xattr list'
        return tuple(sorted(xattr.listxattr(abspath)))
    
    def do_change_groups(self, user, group, groups):
        try:
            subprocess.run(['usermod', '-g', group, '-G', ",".join(groups), user], check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        except subprocess.CalledProcessError as e:
            self.stats.failure('do_change_groups')
            self.logger.info(f"do_change_groups {user} {group} {groups} failed: {e.output.decode()}")
            return
        self.stats.success('do_change_groups')
        self.logger.info(f"do_change_groups {user} {group} {groups} succeed")

    def do_chmod(self, entry, mode, user):
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.seteuid(user)
            os.chmod(abspath, mode)
            # self.run_cmd(f'sudo -u {user} chmod {mode} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_chmod', abspath, mode=mode, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_chmod')
        self.logger.info(f"do_chmod {abspath} {oct(mode)} {user} succeed")
        return get_stat(abspath)

    def do_get_acl(self,  entry: str):
        abspath = os.path.join(self.root_dir, entry)
        try:
            acl = get_acl(abspath)
        except Exception as e:
            return self.handleException(e, 'do_get_acl', abspath)
        self.stats.success('do_get_acl')
        self.logger.info(f"do_get_acl {abspath} succeed")
        return acl

    def do_remove_acl(self,  entry: str, option: str, user: str):
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.run_cmd(f'sudo -u {user} setfacl {option} {abspath} ')
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_remove_acl', abspath, option=option,user=user)
        self.stats.success('do_remove_acl')
        self.logger.info(f"do_remove_acl {abspath} with {option} succeed")
        return get_acl(abspath)
    
    def do_set_acl(self, sudo_user, entry, user, user_perm, group, group_perm, other_perm, set_mask, mask, default, recursive, recalc_mask, not_recalc_mask, logical, physical):
        abspath = os.path.join(self.root_dir, entry)
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
            self.run_cmd(f'sudo -u {sudo_user} setfacl {default} {recursive} {recalc_mask} {not_recalc_mask} {logical} {physical} -m {text} {abspath}')
            acl = get_acl(abspath)
        except subprocess.CalledProcessError as e:
            return self.handleException(e, 'do_set_acl', abspath, user_perm=user_perm, group_perm=group_perm, other_perm=other_perm)
        self.stats.success('do_set_acl')
        self.logger.info(f"do_set_acl {abspath} with {text} succeed")
        return (acl,)

    def do_utime(self, entry, access_time, modify_time, follow_symlinks, user):
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.seteuid(user)
            os.utime(abspath, (access_time, modify_time), follow_symlinks=follow_symlinks)
            # self.run_cmd(f'sudo -u {user} touch -a -t {access_time} {abspath}', root_dir)
            # self.run_cmd(f'sudo -u {user} touch -m -t {modify_time} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_utime', abspath, access_time=access_time, modify_time=modify_time, follow_symlinks=follow_symlinks, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_utime')
        self.logger.info(f"do_utime {abspath} {access_time} {modify_time} succeed")
        return get_stat(abspath)

    def do_chown(self, entry, owner, user):
        abspath = os.path.join(self.root_dir, entry)
        info = pwd.getpwnam(owner)
        uid = info.pw_uid
        gid = info.pw_gid
        try:
            self.seteuid(user)
            os.chown(abspath, uid, gid)
            # self.run_cmd(f'sudo -u {user} chown {owner} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_chown', abspath, owner=owner, user=user)
        finally:
            os.seteuid(0)
            os.setegid(0)
        self.stats.success('do_chown')
        self.logger.info(f"do_chown {abspath} {owner} succeed")
        return get_stat(abspath)

    def do_split_dir(self, dir, vdirs):
        relpath = os.path.join(dir, f'.jfs_split#{vdirs}')
        abspath = os.path.join(self.root_dir, relpath)
        if not is_jfs(abspath):
            return 
        try:
            subprocess.check_call(['touch', abspath])
        except Exception as e:
            self.stats.failure('do_split_dir')
            self.logger.info(f"do_split_dir {abspath} {vdirs} failed: {str(e)}")
            return
        self.stats.success('do_split_dir')
        self.logger.info(f"do_split_dir {abspath} {vdirs} succeed")

    def do_merge_dir(self, dir):
        relpath = os.path.join(dir, f'.jfs_split#1')
        abspath = os.path.join(self.root_dir, relpath)
        if not is_jfs(abspath):
            return 
        try:
            subprocess.check_call(['touch', abspath])
        except Exception as e:
            self.stats.failure('do_merge_dir')
            self.logger.info(f"do_merge_dir {abspath} failed: {str(e)}")
            return
        self.stats.success('do_merge_dir')
        self.logger.info(f"do_merge_dir {abspath} succeed")

    def do_rebalance(self, entry, zone, is_vdir):
        if zone == '':
            print(f'{self.root_dir} is not multizoned, skip rebalance')
            return
        abspath = os.path.join(self.root_dir, entry)
        vdir_relpath = os.path.join(entry, '.jfs#1')
        vdir_abspath = os.path.join(self.root_dir, vdir_relpath)
        if is_vdir and os.path.isfile( vdir_abspath ):
            abspath = vdir_abspath
        try :
            dest = os.path.join(get_root(abspath), zone, os.path.basename(abspath.rstrip('/')))
            # print(f'rename {abspath} {dest}')
            os.rename(abspath, dest)
        except Exception as e:
            self.stats.failure('do_rebalance')
            self.logger.info(f"do_rebalance {abspath} {dest} failed: {str(e)}")
            return
        self.stats.success('do_rebalance')
        self.logger.info(f"do_rebalance {abspath} {dest} succeed")