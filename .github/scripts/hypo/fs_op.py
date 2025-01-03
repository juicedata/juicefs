import io
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
from common import get_acl, get_root, red
from typing import Dict
try: 
    __import__('fallocate')
except ImportError:
    subprocess.check_call(["pip", "install", "fallocate"])
import fallocate
from stats import Statistics
import common
from os.path import dirname
import sys
sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs

class FsOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    
    def __init__(self, name, root_dir:str, mount_point=None, use_sdk:bool=False, is_jfs=False, volume_name=None, meta_url=None):
        self.logger =common.setup_logger(f'./{name}.log', name, os.environ.get('LOG_LEVEL', 'INFO'))
        self.root_dir = root_dir.rstrip('/')
        self.use_sdk = use_sdk
        self.is_jfs = is_jfs
        self.singlezone = False
        if is_jfs:
            self.singlezone = len(common.get_zones(root_dir)) == 1
        if mount_point:
            self.mount_point = mount_point
        else:
            self.mount_point = common.get_root(self.root_dir)
        self.client = None
        if use_sdk and self.is_jfs:
            if meta_url:
                self.client = juicefs.Client(volume_name, meta_url, access_log="/tmp/jfs.log")
            else:
                self.client = juicefs.Client(volume_name, conf_dir='deploy/docker', access_log="/tmp/jfs.log")
        self.client2 = None

    def get_client_for_rebalance(self):
        if self.client2 == None:
            self.client2 = juicefs.Client(common.get_volume_name(self.root_dir), 
                                          conf_dir='deploy/docker', 
                                          access_log="/tmp/rebalance.log", 
                                          attr_cache="0s",
                                          entry_cache="0s", 
                                          dir_entry_cache="0s",)
        return self.client2

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

    def init_rootdir(self):
        if self.client:
            self.logger.debug(f'init_rootdir {self.root_dir} with use_sdk={self.use_sdk}')
            sdk_root_dir = self.get_sdk_path(self.root_dir)
            if self.client.exists(sdk_root_dir):
                self.client.rmr(sdk_root_dir)
                assert not self.client.exists(sdk_root_dir), red(f'{self.root_dir} should not exist')
            self.client.makedirs(sdk_root_dir)
            assert self.client.exists(sdk_root_dir), red(f'{self.root_dir} should exist')
        else:
            if not os.path.exists(self.root_dir):
                os.makedirs(self.root_dir)
            if os.environ.get('PROFILE', 'dev') != 'generate':
                common.clean_dir(self.root_dir)

    def seteuid(self, user, action=''):
        if self.client:
            return
        uid = pwd.getpwnam(user).pw_uid
        gid = pwd.getpwnam(user).pw_gid
        os.setegid(gid)
        os.seteuid(uid)
        self.logger.debug(f'{action} seteuid uid={uid} gid={gid} succeed')

    def reset_euid(self, action=''):
        if self.client:
            return
        os.setegid(0) 
        os.seteuid(0)
        self.logger.debug(f'{action} reset euid and egid succeed')
        
    def handleException(self, e, action, path, **kwargs):
        if isinstance(e, subprocess.CalledProcessError):
            err = e.output.decode()
        else:
            err = type(e).__name__ + ":" + str(e)
        err = '\n'.join([elem.split('<FATAL>:')[-1].split('<ERROR>:')[-1] for elem in err.split('\n')])
        err = re.sub(r'\[\w+\.go:\d+\]', '', err)
        if err.find('setfacl') != -1 and err.find('\n') != -1:
            err = '\n'.join(sorted(err.split('\n')))
        err = self.parse_pysdk_error(err)
        self.stats.failure(action)
        self.logger.info(f'{action} {path} {kwargs} failed: {err}')
        return Exception(err)
    
    def parse_pysdk_error(self, err:str):
        # error message : call jfs_rename failed: [Errno 22] Invalid argument: (b'/fsrand', b'/fsrand/izsn/rfnn', c_uint(0))
        if not err.startswith("call jfs_"):
            return err
        return re.sub(r'call jfs_\w+ failed: ', '', err)
    
    def get_sdk_path(self, abspath):
        return '/'+os.path.relpath(abspath, self.mount_point)

    def do_remove_dangling_files(self):
        if not self.is_jfs or self.use_sdk:
            self.logger.debug(f'do_remove_dangling_files {self.mount_point} skip')
            return
        self.logger.debug(f'do_remove_dangling_files {self.mount_point}')
        zones = common.get_zones(self.mount_point)
        for zone in zones:
            zone_dir = os.path.join(self.mount_point, zone)
            entries = os.listdir(zone_dir)
            for entry in entries:
                if 'dangling' in entry:
                    abspath = os.path.join(zone_dir, entry)
                    if os.path.isdir(abspath):
                        shutil.rmtree(abspath)
                    elif os.path.isfile(abspath):
                        os.unlink(abspath)
            backup_dir = os.path.join(zone_dir, '.backup')
            if os.path.exists(backup_dir):
                entries = os.listdir(backup_dir)
                for entry in entries:
                    if 'dangling' in entry:
                        abspath = os.path.join(backup_dir, entry)
                        if os.path.isdir(abspath):
                            shutil.rmtree(abspath)
                        elif os.path.isfile(abspath):
                            os.unlink(abspath)

        self.logger.info(f'do_remove_dangling_files {self.mount_point} succeed')

    def do_check_dangling_files(self):
        if not self.is_jfs or self.use_sdk:
            self.logger.debug(f'do_check_dangling_files {self.mount_point} skip')
            return
        self.logger.debug(f'do_check_dangling_files {self.mount_point}')
        zones = common.get_zones(self.mount_point)
        for zone in zones:
            zone_dir = os.path.join(self.mount_point, zone)
            entries = os.listdir(zone_dir)
            for entry in entries:
                if 'dangling' in entry:
                    assert False, red(f'{entry} should not exist in {zone_dir}')
            backup_dir = os.path.join(zone_dir, '.backup')
            if os.path.exists(backup_dir):
                entries = os.listdir(backup_dir)
                for entry in entries:
                    if 'dangling' in entry:
                        assert False, red(f'{entry} should not exist in {backup_dir}')
        self.logger.info(f'do_check_dangling_files {self.mount_point} succeed')

    def do_stat(self, entry, user):
        self.logger.debug(f'do_stat {self.root_dir} {entry}')
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.seteuid(user, action='do_stat')
            if self.client:
                st = self.client.stat(self.get_sdk_path(abspath))
            else:
                st = os.stat(abspath)
        except Exception as e :
            return self.handleException(e, 'do_stat', abspath, entry=entry, user=user)
        finally:
            self.reset_euid(action='do_stat')
        self.stats.success('do_stat')
        self.logger.info(f'do_stat {abspath} with user={user} succeed')
        self.logger.debug(f'do_stat st is {st}')
        return common.get_stat_field(st)
   
    def do_lstat(self, entry, user):
        self.logger.debug(f'do_lstat {self.root_dir} {entry}')
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.seteuid(user)
            if self.client:
                st = self.client.lstat(self.get_sdk_path(abspath))
            else:
                st = os.lstat(abspath)
        except Exception as e :
            return self.handleException(e, 'do_lstat', abspath, entry=entry, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_lstat')
        self.logger.info(f'do_lstat {abspath} with user={user} succeed')
        return common.get_stat_field(st)

    def do_exists(self, entry, user):
        self.logger.debug(f'do_exists {self.root_dir} {entry}')
        abspath = os.path.join(self.root_dir, entry)
        try:
            self.seteuid(user)
            if self.client:
                exists = self.client.exists(self.get_sdk_path(abspath))
            else:
                exists = os.path.exists(abspath)
        except Exception as e :
            return self.handleException(e, 'do_exists', abspath, entry=entry, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_exists')
        self.logger.info(f'do_exists {abspath} with user={user} succeed')
        return exists

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
            self.reset_euid()
            os.umask(old_mask)
            if fd > 0:
                os.close(fd)
        self.stats.success('do_open')
        self.logger.info(f'do_open {abspath} {flags} {mode} succeed')
        return self.do_stat(file, user)
    
    def do_open2(self, file, mode, user):
        self.logger.debug(f'do_open2 {self.root_dir} {file} {mode} {user}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode) as f:
                    pass
            else:
                with open(abspath, mode) as f:
                    pass
        except Exception as e :
            return self.handleException(e, 'do_open2', abspath, mode=mode, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_open2')
        self.logger.info(f'do_open2 {abspath} {mode} succeed')
        return self.do_stat(file, user)

    def do_write(self, file, content, mode:str, encoding, errors, offset, whence, user):
        self.logger.debug(f'do_write {self.root_dir} {file} {offset}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                size = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode, encoding=encoding, errors=errors) as f:
                    f.seek(offset, whence)
                    count=f.write(content)
            else:
                with open(abspath, mode, encoding=encoding, errors=errors) as f:
                    f.seek(offset, whence)
                    count=f.write(content)
        except (io.UnsupportedOperation) as e:
            e = Exception(f'io.UnsupportedOperation: write')
            return self.handleException(e, 'do_write', abspath, offset=offset, whence=whence, mode=mode, user=user)
        except Exception as e :
            return self.handleException(e, 'do_write', abspath, offset=offset, whence=whence, mode=mode, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_write')
        self.logger.info(f'do_write {abspath} offset={offset} whence={whence} mode={mode} user={user} succeed')
        return count, self.do_stat(file, user)
        
    def do_writelines(self, file, lines, mode, offset, whence, user):
        self.logger.debug(f'do_writelines {self.root_dir} {file} {offset}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                size = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode) as f:
                    f.seek(offset, whence)
                    f.writelines(lines)
            else:
                with open(abspath, mode) as f:
                    # f.seek(offset, whence)
                    f.seek(offset, whence)
                    f.writelines(lines)
        except (TypeError,io.UnsupportedOperation) as e:
            self.logger.debug(f'writelines: {str(e)}')
            e = Exception(f'writelines')
            return self.handleException(e, 'do_writelines', abspath, offset=offset, whence=whence, mode=mode, user=user)
        except Exception as e :
            return self.handleException(e, 'do_writelines', abspath, offset=offset, whence=whence, mode=mode, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_writelines')
        self.logger.info(f'do_writelines {abspath} offset={offset} whence={whence} mode={mode} user={user} succeed')
        return self.do_stat(file, user)

    def do_fallocate(self, file, offset, length, mode, user):
        self.logger.debug(f'do_fallocate {self.root_dir} {file} {offset} {length} {mode} {user}')
        abspath = os.path.join(self.root_dir, file)
        fd = -1
        try:
            self.seteuid(user)
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            fd = os.open(abspath, os.O_RDWR)
            fallocate.fallocate(fd, offset, length, mode)
        except Exception as e :
            return self.handleException(e, 'do_fallocate', abspath, offset=offset, length=length, mode=mode, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            self.reset_euid()
        self.stats.success('do_fallocate')
        self.logger.info(f'do_fallocate {abspath} offset={offset} length={length} mode={mode} user={user} succeed')
        return self.do_stat(file, user)

    def do_copy_file_range(self, src, dst, src_offset, dst_offset, count, user):
        self.logger.debug(f'do_copy_file_range {self.root_dir} {src} {dst} {src_offset} {dst_offset} {count} {user}')
        src_abspath = os.path.join(self.root_dir, src)
        dst_abspath = os.path.join(self.root_dir, dst)
        src_fd = -1
        dst_fd = -1
        try:
            self.seteuid(user)
            src_fd = os.open(src_abspath, os.O_RDONLY)
            dst_fd = os.open(dst_abspath, os.O_WRONLY)
            os.copy_file_range(src_fd, dst_fd, count, src_offset, dst_offset)
        except Exception as e :
            return self.handleException(e, 'do_copy_file_range', src_abspath, dst_abspath=dst_abspath, src_offset=src_offset, dst_offset=dst_offset, count=count, user=user)
        finally:
            if src_fd > 0:
                os.close(src_fd)
            if dst_fd > 0:
                os.close(dst_fd)
            self.reset_euid()
        self.stats.success('do_copy_file_range')
        self.logger.info(f'do_copy_file_range {src_abspath} {dst_abspath} src_offset={src_offset} dst_offset={dst_offset} count={count} user={user} succeed')
        return self.do_stat(dst, user)

    def do_read(self, file, length, mode, offset, whence, user, encoding, errors):
        self.logger.debug(f'do_read {self.root_dir} {file} {mode} {length} {offset} {whence}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                size = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode, encoding=encoding, errors=errors) as f:
                    f.seek(offset, whence)
                    result = f.read(length)    
            else:
                with open(abspath, mode, encoding=encoding, errors=errors) as f: 
                    # f.seek(offset, whence)
                    f.seek(offset, whence)
                    result = f.read(length)
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            # result = binascii.hexlify(result)
        except UnicodeDecodeError as e:
            # SEE: https://github.com/juicedata/jfs/issues/1450#issuecomment-2213518638
            self.logger.debug(f'UnicodeDecodeError: {e.encoding} {e.object} {e.start} {e.end} {e.reason}')
            e = UnicodeDecodeError(e.encoding, e.object, 0, 0, e.reason)
            return self.handleException(e, 'do_read', abspath, offset=offset, length=length, whence=whence, user=user)
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: read')
            return self.handleException(e, 'do_read', abspath, offset=offset, length=length, whence=whence, user=user)
        except Exception as e :
            return self.handleException(e, 'do_read', abspath, offset=offset, length=length, whence=whence, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_read')
        self.logger.info(f'do_read {abspath} mode={mode} length={length} offset={offset} whence={whence} user={user} succeed')
        return (result, )

    def do_readlines(self, file, mode, offset, whence, user):
        self.logger.debug(f'do_readlines {self.root_dir} {file} {mode} {offset} {whence}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                size = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            self.logger.debug(f'do_readlines offset={offset} size={size}')
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode) as f:
                    f.seek(offset, whence)
                    result = ''.join(f.readlines())
            else:
                with open(abspath, mode) as f:
                    # f.seek(offset, whence)
                    f.seek(offset, whence)
                    result = ''.join(f.readlines())
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            # result = binascii.hexlify(result)
        except UnicodeDecodeError as e:
            # SEE: https://github.com/juicedata/jfs/issues/1450#issuecomment-2213518638
            self.logger.debug(f'UnicodeDecodeError: {e.encoding} {e.object} {e.start} {e.end} {e.reason}')
            e = UnicodeDecodeError(e.encoding, e.object, 0, 0, e.reason)
            return self.handleException(e, 'do_readlines', abspath, offset=offset, whence=whence, user=user)
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: readlines')
            return self.handleException(e, 'do_readlines', abspath, offset=offset, whence=whence, user=user)
        except Exception as e :
            return self.handleException(e, 'do_readlines', abspath, offset=offset, whence=whence, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_readlines')
        self.logger.info(f'do_readlines {abspath} mode={mode} offset={offset} whence={whence} user={user} succeed')
        return (result, )

    def do_readline(self, file, mode, offset, whence, user):
        self.logger.debug(f'do_readline {self.root_dir} {file} {mode} {offset} {whence}')
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                size = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                size = os.stat(abspath).st_size
            if size == 0:
                offset = 0
            else:
                offset = offset % size
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode) as f:
                    f.seek(offset, whence)
                    result = f.readline()
            else:
                with open(abspath, mode) as f:
                    # f.seek(offset, whence)
                    f.seek(offset, whence)
                    result = f.readline()
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            # result = binascii.hexlify(result)
        except UnicodeDecodeError as e:
            # SEE: https://github.com/juicedata/jfs/issues/1450#issuecomment-2213518638
            self.logger.debug(f'UnicodeDecodeError: {e.encoding} {e.object} {e.start} {e.end} {e.reason}')
            e = UnicodeDecodeError(e.encoding, e.object, 0, 0, e.reason)
            return self.handleException(e, 'do_readline', abspath, offset=offset, whence=whence, user=user)
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: readline')
            return self.handleException(e, 'do_readline', abspath, offset=offset, whence=whence, user=user)
        except Exception as e :
            return self.handleException(e, 'do_readline', abspath, offset=offset, whence=whence, user=user)
        finally:
            self.reset_euid()

        self.stats.success('do_readline')
        self.logger.info(f'do_readline {abspath} mode={mode} offset={offset} whence={whence} user={user} succeed')
        return (result, )

    def do_truncate(self, file, size, user):
        self.logger.debug(f'do_truncate {self.root_dir} {file} {size}')
        abspath = os.path.join(self.root_dir, file)
        fd = -1
        try:
            self.seteuid(user, action='do_truncate')
            if self.client:
                st = self.client.stat(self.get_sdk_path(abspath))
            else:
                st = os.stat(abspath)
            if st.st_size == 0:
                size = 0
            else:
                size = size % st.st_size
            if self.client:
                self.client.truncate(self.get_sdk_path(abspath), size)
                st = self.client.stat(self.get_sdk_path(abspath))
            else:
                os.truncate(abspath, size)
                st = os.stat(abspath)
        except Exception as e :
            return self.handleException(e, 'do_truncate', abspath, size=size, user=user)
        finally:
            if fd > 0:
                os.close(fd)
            self.reset_euid()
        assert st.st_size == size, red(f'do_truncate: {abspath} size should be {size} but {st.st_size}')
        self.stats.success('do_truncate')
        self.logger.info(f'do_truncate {abspath} size={size} user={user} succeed')
        return self.do_stat(file, user)

    def do_create_file(self, parent, file_name, content, mode='xb', user='root', umask=0o022, buffering=-1):
        relpath = os.path.join(parent, file_name)
        abspath = os.path.join(self.root_dir, relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user, action='do_create_file')
            if self.client:
                with self.client.open(self.get_sdk_path(abspath), mode, buffering=buffering) as f:
                    f.write(content)
                    count=f.write(content)
            else:
                with open(abspath, mode, buffering=buffering) as f:
                    f.write(content)
                    count=f.write(content)
        except Exception as e :
            return self.handleException(e, 'do_create_file', abspath, mode=mode, user=user)
        finally:
            self.reset_euid(action='do_create_file')
            os.umask(old_umask)
        self.stats.success('do_create_file')
        self.logger.info(f'do_create_file {abspath} with mode {mode} succeed')
        return count, self.do_stat(relpath, user)
    
    def do_listdir(self, dir, user):
        abspath = os.path.join(self.root_dir, dir)
        try:
            self.seteuid(user)
            if self.client:
                li = self.client.listdir(self.get_sdk_path(abspath))
            else:
                li = os.listdir(abspath) 
            li = sorted(list(filter(lambda x: x not in self.JFS_CONTROL_FILES, li)))
        except Exception as e:
            return self.handleException(e, 'do_listdir', abspath, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_listdir')
        self.logger.info(f'do_listdir {abspath} with user={user} succeed')
        return tuple(li)

    def do_unlink(self, file, user):
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                self.client.unlink(self.get_sdk_path(abspath))
            else:
                os.unlink(abspath)
        except Exception as e:
            return self.handleException(e, 'do_unlink', abspath, user=user)
        finally:
            self.reset_euid()
        assert not os.path.exists(abspath), red(f'do_unlink: {abspath} should not exist')
        self.stats.success('do_unlink')
        self.logger.info(f'do_unlink {abspath} with user={user} succeed')
        return True 

    def do_rename(self, entry, parent, new_entry_name, user, umask):
        abspath = os.path.join(self.root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(self.root_dir, new_relpath)
        try:
            self.seteuid(user)
            old_umask = os.umask(umask)
            if self.client:
                path = self.get_sdk_path(abspath)
                new_path = self.get_sdk_path(new_abspath)
                self.client.rename(path, new_path)
            else:
                os.rename(abspath, new_abspath)
        except Exception as e:
            return self.handleException(e, 'do_rename', abspath, new_abspath=new_abspath, user=user)
        finally:
            self.reset_euid()
            os.umask(old_umask)
        if not self.use_sdk:
            assert os.path.lexists(new_abspath), red(f'do_rename: {new_abspath} should exist')
        self.stats.success('do_rename')
        self.logger.info(f'do_rename {abspath} {new_abspath} with user={user} succeed')
        return self.do_stat(new_relpath, user)

    def do_copy_file(self, entry, parent, new_entry_name, follow_symlinks, user, umask):
        abspath = os.path.join(self.root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(self.root_dir, new_relpath)
        try:
            old_umask = os.umask(umask)
            self.seteuid(user)
            shutil.copy(abspath, new_abspath, follow_symlinks=follow_symlinks)
        except Exception as e:
            return self.handleException(e, 'do_copy_file', abspath, new_abspath=new_abspath, user=user, follow_symlinks=follow_symlinks, umask=umask)
        finally:
            self.reset_euid()
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), red(f'do_copy_file: {new_abspath} should exist')
        self.stats.success('do_copy_file')
        self.logger.info(f'do_copy_file {abspath} {new_abspath} with follow_symlinks={follow_symlinks} user={user} umask={umask} succeed')
        return self.do_stat(new_relpath, user)

    def can_clone(self, src_dir, dst_dir):
        if os.path.commonpath([src_dir]) == os.path.commonpath([src_dir, dst_dir]) or \
              os.path.commonpath([dst_dir]) == os.path.commonpath([src_dir, dst_dir]):
            return False
        if os.path.exists(dst_dir):
            return False
        return True
        
    def do_clone_entry(self,  entry, parent, new_entry_name, preserve, user='root', umask=0o022, mount='cmd/mount/mount'):
        root_dir = self.root_dir
        abspath = os.path.join(root_dir, entry)
        new_relpath = os.path.join(parent, new_entry_name)
        new_abspath = os.path.join(root_dir, new_relpath)
        if not self.can_clone(abspath, new_abspath):
            return self.handleException(Exception(f'can not clone {abspath} to {new_abspath}'), 'do_clone_entry', abspath, new_abspath=new_abspath, user=user)
        try:
            old_umask = os.umask(umask)
            if self.is_jfs:
                if preserve:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath} --preserve')
                else:
                    self.run_cmd(f'sudo -u {user} {mount} clone {abspath} {new_abspath}')
            else:
                if preserve:
                    self.run_cmd(f'sudo -u {user} cp -r {abspath} {new_abspath} -L --preserve=all')
                else:
                    self.run_cmd(f'sudo -u {user} cp -r {abspath} {new_abspath} -L')
        except subprocess.CalledProcessError as e:
            self.logger.error(f'run command failed: {e.output.decode()}')
            return self.handleException(Exception(f'do_clone_entry failed'), 'do_clone_entry', abspath, new_abspath=new_abspath, user=user)
        finally:
            os.umask(old_umask)
        assert os.path.lexists(new_abspath), red(f'do_clone_entry: {new_abspath} should exist')
        self.stats.success('do_clone_entry')
        self.logger.info(f'do_clone_entry {abspath} {new_abspath} succeed')
        return self.do_stat(new_relpath, user)
    
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
            self.reset_euid()
            os.umask(old_mask)
        assert os.path.lexists(new_abspath), red(f'do_copy_tree: {new_abspath} should exist')
        self.stats.success('do_copy_tree')
        self.logger.info(f'do_copy_tree {abspath} {new_abspath} succeed')
        return self.do_stat(new_relpath, user)

    def do_mkdir(self, parent, subdir, mode, user, umask):
        relpath = os.path.join(parent, subdir)
        abspath = os.path.join(self.root_dir, relpath)
        try:
            self.seteuid(user)
            old_mask = os.umask(umask)
            if self.client:
                sdk_path = self.get_sdk_path(abspath)
                self.client.mkdir(sdk_path, mode)
                st = self.client.stat(sdk_path)
            else:
                os.mkdir(abspath, mode)
                st = os.stat(abspath)
        except Exception as e:
            return self.handleException(e, 'do_mkdir', abspath, mode=mode, user=user)
        finally:
            self.reset_euid()
            os.umask(old_mask)
        assert stat.S_ISDIR(st.st_mode), red(f'do_mkdir: {abspath} should be dir')
        self.stats.success('do_mkdir')
        self.logger.info(f'do_mkdir {abspath} with mode={oct(mode)} user={user} succeed')
        return self.do_stat(entry=relpath, user=user)
    
    def do_rmdir(self, dir, user):
        abspath = os.path.join(self.root_dir, dir)
        try:
            self.seteuid(user)
            if self.client:
                self.client.rmdir(self.get_sdk_path(abspath))
                exist = self.client.exists(self.get_sdk_path(abspath))
            else:
                os.rmdir(abspath)
                exist = os.path.exists(abspath)
        except Exception as e:
            return self.handleException(e, 'do_rmdir', abspath, user=user)
        finally:
            self.reset_euid()
        assert not exist, red(f'do_rmdir: {abspath} should not exist')
        self.stats.success('do_rmdir')
        self.logger.info(f'do_rmdir {abspath} with user={user} succeed')
        return True

    def do_hardlink(self, src_file, parent, link_file_name, user, umask):
        src_abs_path = os.path.join(self.root_dir, src_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(self.root_dir, link_rel_path)
        try:
            self.seteuid(user)
            old_mask = os.umask(umask)
            if self.client:
                path = self.get_sdk_path(src_abs_path)
                link_path = self.get_sdk_path(link_abs_path)
                self.client.link(path, link_path)
            else:
                os.link(src_abs_path, link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_hardlink', src_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            self.reset_euid()
            os.umask(old_mask)
        # time.sleep(0.005)
        # assert st.st_nlink > 1, red(f'do_hardlink: nlink({st.st_nlink}) of {link_abs_path} should greater than 1')
        self.stats.success('do_hardlink')
        self.logger.info(f'do_hardlink {src_abs_path} {link_abs_path} with user={user} umask={oct(umask)} succeed')
        return self.do_stat(link_rel_path, user)

    def do_symlink(self, src_file, parent, link_file_name, user, umask):
        src_abs_path = os.path.join(self.root_dir, src_file)
        link_rel_path = os.path.join(parent, link_file_name)
        link_abs_path = os.path.join(self.root_dir, link_rel_path)
        relative_path = os.path.relpath(src_abs_path, os.path.dirname(link_abs_path))
        try:
            self.seteuid(user)
            old_mask = os.umask(umask)
            if self.client:
                path = self.get_sdk_path(src_abs_path)
                link_path = self.get_sdk_path(link_abs_path)
                self.client.symlink(path, link_path)
                st = self.client.lstat(link_path)
            else:
                os.symlink(relative_path, link_abs_path)
                st = os.lstat(link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_symlink', src_abs_path, link_abs_path=link_abs_path, user=user)
        finally:
            self.reset_euid()
            os.umask(old_mask)
        assert stat.S_ISLNK(st.st_mode), red(f'do_symlink: {link_abs_path} should be link')
        self.stats.success('do_symlink')
        self.logger.info(f'do_symlink {src_abs_path} {link_abs_path} with user={user} umask={oct(umask)} succeed')
        return self.do_stat(link_rel_path, user)
    
    def do_readlink(self, file, user):
        link_abs_path = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                dest = self.client.readlink(self.get_sdk_path(link_abs_path))
            else:
                dest = os.readlink(link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_read_link', link_abs_path, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_read_link')
        self.logger.info(f'do_read_link {link_abs_path} with user={user} succeed')
        return (dest,)

    def do_loop_symlink(self, parent, link_file_name, user='root'):
        link_abs_path = os.path.join(self.root_dir, parent, link_file_name)
        try:
            self.seteuid(user)
            if self.client:
                sdk_path = self.get_sdk_path(link_abs_path)
                self.client.symlink(sdk_path, sdk_path)
            else:
                os.symlink(link_file_name, link_abs_path)
        except Exception as e:
            return self.handleException(e, 'do_loop_symlink', link_abs_path)
        finally:
            self.reset_euid()
        self.stats.success('do_loop_symlink')
        self.logger.info(f'do_loop_symlink {link_abs_path} succeed')
        return True
    
    def do_set_xattr(self, file, name, value, flag, user):
        xattr_map = {0:0, xattr.XATTR_CREATE: juicefs.XATTR_CREATE, xattr.XATTR_REPLACE: juicefs.XATTR_REPLACE}
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                flag = xattr_map[flag]
                self.client.setxattr(self.get_sdk_path(abspath), name, value, flag)
            else:
                xattr.setxattr(abspath, name, value, flag)
        except Exception as e:
            return self.handleException(e, 'do_set_xattr', abspath, name=name, value=value, flag=flag, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_set_xattr')
        self.logger.info(f"do_set_xattr {abspath} with name={name} value={value} flag={flag} user={user} succeed")
        return 'succeed'

    def do_get_xattr(self, file, name, user):
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                value = self.client.getxattr(self.get_sdk_path(abspath), name)
            else:
                value = xattr.getxattr(abspath, name)
        except Exception as e:
            return self.handleException(e, 'do_get_xattr', abspath, name=name, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_get_xattr')
        self.logger.info(f"do_get_xattr {abspath} with name={name} user={user} succeed")
        return (value,)

    def do_list_xattr(self, file, user):
        abspath = os.path.join(self.root_dir, file)
        xattr_list = []
        try:
            self.seteuid(user)    
            if self.client:
                path = self.get_sdk_path(abspath)
                xattrs = self.client.listxattr(path)
            else:
                xattrs = xattr.listxattr(abspath)
            xattr_list = []
            for attr in xattrs:
                if self.client:
                    path = self.get_sdk_path(abspath)
                    value = self.client.getxattr(path, attr)
                else:
                    value = xattr.getxattr(abspath, attr)
                xattr_list.append((attr, value))
            xattr_list.sort()  # Sort the list based on xattr names
        except Exception as e:
            return self.handleException(e, 'do_list_xattr', abspath, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_list_xattr')
        self.logger.info(f"do_list_xattr {abspath} with user={user} succeed")
        return xattr_list

    def do_remove_xattr(self, file, name, user):
        abspath = os.path.join(self.root_dir, file)
        try:
            self.seteuid(user)
            if self.client:
                self.client.removexattr(self.get_sdk_path(abspath), name)
            else:
                xattr.removexattr(abspath, name)
            # self.run_cmd(f'sudo -u {user} setfattr -x {name} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_remove_xattr', abspath, name=name, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_remove_xattr')
        self.logger.info(f"do_remove_xattr {abspath} name={name} user={user} succeed")
        return 'succeed'
    
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
            if self.client:
                self.client.chmod(self.get_sdk_path(abspath), mode)
            else:
                os.chmod(abspath, mode)
            # self.run_cmd(f'sudo -u {user} chmod {mode} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_chmod', abspath, mode=mode, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_chmod')
        self.logger.info(f"do_chmod {abspath} mode={oct(mode)} user={user} succeed")
        return self.do_stat(entry, user)

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
            if self.client:
                self.client.utime(self.get_sdk_path(abspath), (access_time, modify_time))
            else:
                os.utime(abspath, (access_time, modify_time), follow_symlinks=follow_symlinks)
                # self.run_cmd(f'sudo -u {user} touch -a -t {access_time} {abspath}', root_dir)
                # self.run_cmd(f'sudo -u {user} touch -m -t {modify_time} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_utime', abspath, access_time=access_time, modify_time=modify_time, follow_symlinks=follow_symlinks, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_utime')
        self.logger.info(f"do_utime {abspath} with access_time={access_time} modify_time={modify_time} follow_symlinks={follow_symlinks} user={user} succeed")
        return self.do_stat(entry, user)

    def do_chown(self, entry, owner, user):
        abspath = os.path.join(self.root_dir, entry)
        info = pwd.getpwnam(owner)
        uid = info.pw_uid
        gid = info.pw_gid
        try:
            self.seteuid(user)
            if self.client:
                self.client.chown(self.get_sdk_path(abspath), uid, gid)
            else:
                os.chown(abspath, uid, gid)
                # self.run_cmd(f'sudo -u {user} chown {owner} {abspath}', root_dir)
        except Exception as e:
            return self.handleException(e, 'do_chown', abspath, owner=owner, user=user)
        finally:
            self.reset_euid()
        self.stats.success('do_chown')
        self.logger.info(f"do_chown {abspath} with owner={owner} user={user} succeed")
        return self.do_stat(entry, user)

    def do_split_dir(self, dir, vdirs):
        relpath = os.path.join(dir, f'.jfs_split#{vdirs}')
        abspath = os.path.join(self.root_dir, relpath)
        if not self.is_jfs:
            return 
        try:
            subprocess.check_call(['touch', abspath], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except Exception as e:
            self.stats.failure('do_split_dir')
            self.logger.info(f"do_split_dir {abspath} {vdirs} failed: {str(e)}")
            return
        self.stats.success('do_split_dir')
        self.logger.info(f"do_split_dir {abspath} {vdirs} succeed")

    def do_merge_dir(self, dir):
        relpath = os.path.join(dir, f'.jfs_split#1')
        abspath = os.path.join(self.root_dir, relpath)
        if not self.is_jfs:
            return 
        try:
            subprocess.run(['touch', abspath], check=True, capture_output=True, text=True)
        except subprocess.CalledProcessError as e:
            error = f'{e.cmd} exit with {e.returncode}, {e.stderr}'.strip()
            self.stats.failure('do_merge_dir')
            self.logger.info(f"do_merge_dir {abspath} failed: {error}")
            return
        self.stats.success('do_merge_dir')
        self.logger.info(f"do_merge_dir {abspath} succeed")

    def do_rebalance_with_pysdk(self, entry, zone, is_vdir):
        if zone == '':
            # print(f'{self.root_dir} is not multizoned, skip rebalance')
            return
        abspath = os.path.join(self.root_dir, entry)
        vdir_relpath = os.path.join(entry, '.jfs#1')
        vdir_abspath = os.path.join(self.root_dir, vdir_relpath)
        if is_vdir and os.path.isfile( vdir_abspath ):
            abspath = vdir_abspath
        try :
            dest = os.path.join(get_root(abspath), zone, os.path.basename(abspath.rstrip('/')))
            os.rename(abspath, dest)
        except Exception as e:
            self.stats.failure('do_rebalance')
            self.logger.info(f"do_rebalance {abspath} {dest} failed: {str(e)}")
            return
        self.stats.success('do_rebalance')
        self.logger.info(f"do_rebalance {abspath} {dest} succeed")

    def do_rebalance(self, entry, zone, is_vdir, pysdk=True):
        if zone == '':
            # print(f'{self.root_dir} is not multizoned, skip rebalance')
            return
        abspath = os.path.join(self.root_dir, entry)
        vdir_relpath = os.path.join(entry, '.jfs#1')
        vdir_abspath = os.path.join(self.root_dir, vdir_relpath)
        if is_vdir and os.path.isfile( vdir_abspath ):
            abspath = vdir_abspath
        try :
            dest = os.path.join(get_root(abspath), zone, os.path.basename(abspath.rstrip('/')))
            if pysdk:
                client = self.get_client_for_rebalance()
                if client.exists(self.get_sdk_path(abspath)):
                    client.rename(self.get_sdk_path(abspath), self.get_sdk_path(dest))
            else:
                if os.path.exists(abspath):
                    os.rename(abspath, dest)
        except OSError as e:
            self.stats.failure('do_rebalance')
            self.logger.info(f"do_rebalance {abspath} {dest} failed: {str(e)}")
        self.stats.success('do_rebalance')
        self.logger.info(f"do_rebalance {abspath} {dest} succeed")