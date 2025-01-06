import hashlib
import io
import mmap
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

class FileOperation:
    JFS_CONTROL_FILES=['.accesslog', '.config', '.stats']
    stats = Statistics()
    Files = {}
    
    def __init__(self, name, root_dir:str, mount_point=None, use_sdk:bool=False, is_jfs=False, volume_name=None, meta_url=None):
        self.logger =common.setup_logger(f'./{name}.log', name, os.environ.get('LOG_LEVEL', 'INFO'))
        self.root_dir = root_dir.rstrip('/')
        self.use_sdk = use_sdk
        self.is_jfs = is_jfs
        if mount_point:
            self.mount_point = mount_point
        else:
            self.mount_point = common.get_root(self.root_dir)
        self.client = None
        if use_sdk and self.is_jfs:
            if meta_url:
                self.client = juicefs.Client(volume_name, meta=meta_url, access_log="/tmp/jfs.log")
            else:
                self.client = juicefs.Client(volume_name, conf_dir='deploy/docker', access_log="/tmp/jfs.log")

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

    def do_stat(self, entry):
        self.logger.debug(f'do_stat {self.root_dir} {entry}')
        abspath = os.path.join(self.root_dir, entry)
        try:
            if self.client:
                st = self.client.stat(self.get_sdk_path(abspath))
            else:
                st = os.stat(abspath)
        except Exception as e :
            return self.handleException(e, 'do_stat', abspath, entry=entry)
        finally:
            pass
        self.stats.success('do_stat')
        self.logger.info(f'do_stat {abspath} with succeed')
        self.logger.debug(f'do_stat st is {st}')
        return common.get_stat_field(st)
    
    def do_create_file(self, file, content, mode, encoding, errors):
        self.logger.debug(f'do_create_file {self.root_dir} {file} {mode}')
        abspath = os.path.join(self.root_dir, file)
        f = None
        try:
            if self.client:
                f = self.client.open(self.get_sdk_path(abspath), mode=mode, encoding=encoding, errors=errors)           
            else:
                f = open(abspath, mode=mode, encoding=encoding, errors=errors)
            f.write(content)
            f.flush()
        except Exception as e :
            return f, self.handleException(e, 'do_create_file', abspath, mode=mode)
        finally:
            pass
        self.stats.success('do_create_file')
        self.logger.info(f'do_create_file {abspath} {mode} succeed')
        return f, 'succeed'

    def do_open(self, file, mode, encoding, errors):
        self.logger.debug(f'do_open {self.root_dir} {file} {mode}')
        abspath = os.path.join(self.root_dir, file)
        f = None
        try:
            if self.client:
                f = self.client.open(self.get_sdk_path(abspath), mode=mode, encoding=encoding, errors=errors)           
            else:
                f = open(abspath, mode=mode, encoding=encoding, errors=errors)
        except Exception as e :
            return f, self.handleException(e, 'do_open', abspath, mode=mode)
        finally:
            pass
        self.stats.success('do_open')
        self.logger.info(f'do_open {abspath} {mode} succeed')
        return f, 'succeed'

    def do_write(self, fd, file, content):
        self.logger.debug(f'do_write {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                fd.write(content)
            else:
                fd.write(content)
        except (io.UnsupportedOperation) as e:
            e = Exception(f'io.UnsupportedOperation: write')
            return self.handleException(e, 'do_write', abspath)
        except Exception as e :
            return self.handleException(e, 'do_write', abspath)
        finally:
            pass
        self.stats.success('do_write')
        self.logger.info(f'do_write {abspath} succeed')
        return 'succeed'

    def do_writelines(self, fd, file, lines):
        self.logger.debug(f'do_writelines {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                fd.writelines(lines)
            else:
                fd.writelines(lines)
        except (TypeError,io.UnsupportedOperation) as e:
            self.logger.debug(f'writelines: {str(e)}')
            e = Exception(f'writelines')
            return self.handleException(e, 'do_writelines', abspath)
        except Exception as e :
            return self.handleException(e, 'do_writelines', abspath)
        finally:
            pass
        self.stats.success('do_writelines')
        self.logger.info(f'do_writelines {abspath} succeed')
        return 'succeed'


    def do_seek(self, fd, file, offset, whence):
        self.logger.debug(f'do_seek {self.root_dir} file={file} offset={offset} whence={whence}')
        abspath = os.path.join(self.root_dir, file)
        try:
            pos = fd.seek(offset, whence)
        except Exception as e :
            return self.handleException(e, 'do_seek', abspath, offset=offset, whence=whence)
        finally:
            pass
        self.stats.success('do_seek')
        self.logger.info(f'do_seek {abspath} offset={offset} whence={whence} succeed, pos={pos}')
        return pos

    def do_tell(self, fd, file):
        self.logger.debug(f'do_tell {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            offset = fd.tell()
        except Exception as e :
            return self.handleException(e, 'do_tell', abspath)
        finally:
            pass
        self.stats.success('do_tell')
        self.logger.info(f'do_tell {abspath} succeed, offset={offset}')
        return offset
    
    def do_close(self, fd, file):
        self.logger.debug(f'do_close {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            fd.close()
        except Exception as e :
            return self.handleException(e, 'do_close', abspath)
        finally:
            pass
        self.stats.success('do_close')
        self.logger.info(f'do_close {abspath} succeed')
        return self.do_stat(file)
    
    def do_flush_and_fsync(self, fd, file):
        self.logger.debug(f'do_flush {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                fd.flush()
                fd.fsync()
            else:
                fd.flush()
                os.fsync(fd.fileno())
        except Exception as e :
            return self.handleException(e, 'do_flush', abspath)
        finally:
            pass
        self.stats.success('do_flush')
        self.logger.info(f'do_flush {abspath} succeed')
        return self.do_stat(file)

    def do_fallocate(self, fd, file, offset, length):
        self.logger.debug(f'do_fallocate {self.root_dir} {file} {offset} {length}')
        abspath = os.path.join(self.root_dir, file)
        try:
            file_size = os.stat(abspath).st_size
            if file_size == 0:
                offset = 0
            else:
                offset = offset % file_size
            fallocate.fallocate(fd.fileno(), offset, length)
        except Exception as e :
            return self.handleException(e, 'do_fallocate', abspath, offset=offset, length=length)
        finally:
            pass
        self.stats.success('do_fallocate')
        self.logger.info(f'do_fallocate {abspath} offset={offset} length={length} succeed')
        return self.do_stat(file)
    

    def do_read(self, fd, file, length):
        self.logger.debug(f'do_read {self.root_dir} {file} {length}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                result = fd.read(length)    
            else:
                result = fd.read(length)
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            self.logger.debug(f'do_read result is {result}')
            result = hashlib.md5(result).hexdigest()
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: read')
            return self.handleException(e, 'do_read', abspath, length=length)
        except Exception as e :
            return self.handleException(e, 'do_read', abspath, length=length)
        finally:
            pass
        self.stats.success('do_read')
        self.logger.info(f'do_read {abspath} length={length} succeed')
        return (result, )

    def do_readlines(self, fd, file):
        self.logger.debug(f'do_readlines {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                result = ''.join(fd.readlines())
            else:
                result = ''.join(fd.readlines())
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            self.logger.debug(f'do_readlines result is {result}')
            result = hashlib.md5(result).hexdigest()
        except UnicodeDecodeError as e:
            # SEE: https://github.com/juicedata/jfs/issues/1450#issuecomment-2213518638
            self.logger.debug(f'UnicodeDecodeError: {e.encoding} {e.object} {e.start} {e.end} {e.reason}')
            e = UnicodeDecodeError(e.encoding, e.object, 0, 0, e.reason)
            return self.handleException(e, 'do_readlines', abspath)
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: readlines')
            return self.handleException(e, 'do_readlines', abspath)
        except Exception as e :
            return self.handleException(e, 'do_readlines', abspath)
        finally:
            pass
        self.stats.success('do_readlines')
        self.logger.info(f'do_readlines {abspath} succeed')
        return (result, )

    def do_readline(self, fd, file):
        self.logger.debug(f'do_readline {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                result = fd.readline()
            else:
                result = fd.readline()
            if isinstance(result, str):
                result = result.replace('\r', '\n') # SEE: https://github.com/juicedata/jfs/issues/1472
                result = result.encode()
            self.logger.debug(f'do_readline result is {result}')
            result = hashlib.md5(result).hexdigest()
        except UnicodeDecodeError as e:
            # SEE: https://github.com/juicedata/jfs/issues/1450#issuecomment-2213518638
            self.logger.debug(f'UnicodeDecodeError: {e.encoding} {e.object} {e.start} {e.end} {e.reason}')
            e = UnicodeDecodeError(e.encoding, e.object, 0, 0, e.reason)
            return self.handleException(e, 'do_readline', abspath)
        except io.UnsupportedOperation as e:
            e = Exception(f'io.UnsupportedOperation: readline')
            return self.handleException(e, 'do_readline', abspath)
        except Exception as e :
            return self.handleException(e, 'do_readline', abspath)
        finally:
            pass

        self.stats.success('do_readline')
        self.logger.info(f'do_readline {abspath} succeed')
        return (result, )

    def do_truncate(self, fd, file, size):
        self.logger.debug(f'do_truncate {self.root_dir} {file} {size}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                fd.flush()
                fd.truncate(size)
                st = self.client.stat(self.get_sdk_path(abspath))
            else:
                fd.flush()
                os.ftruncate(fd.fileno(), size)
                st = os.stat(abspath)
        except Exception as e :
            return self.handleException(e, 'do_truncate', abspath, size=size)
        finally:
            pass
        assert st.st_size == size, red(f'do_truncate: {abspath} size should be {size} but {st.st_size}')
        self.stats.success('do_truncate')
        self.logger.info(f'do_truncate {abspath} size={size} succeed')
        return 'succeed'

    def do_copy_file_range(self, src_file, dst_file, src_fd, dst_fd, src_offset, dst_offset, length):
        self.logger.debug(f'do_copy_file_range from {self.root_dir}/{src_file} to {self.root_dir}/{dst_file} {src_offset} {dst_offset} {length}')
        src_abspath = os.path.join(self.root_dir, src_file)
        dst_abspath = os.path.join(self.root_dir, dst_file)
        try:
            os.copy_file_range(src_fd, dst_fd, length, src_offset, dst_offset)
        except Exception as e :
            return self.handleException(e, 'do_copy_file_range', src_abspath, src_offset=src_offset, dst_offset=dst_offset, length=length)
        finally:
            pass
        self.stats.success('do_copy_file_range')
        self.logger.info(f'do_copy_file_range {src_abspath} to {dst_abspath} {src_offset} {dst_offset} {length} succeed')
        return os.stat(dst_abspath).st_size

    def do_mmap_create(self, file, fd):
        self.logger.debug(f'do_mmap_create {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            mm = mmap.mmap(fd.fileno(), 0)
        except Exception as e :
            return None, self.handleException(e, 'do_mmap_create', abspath)
        finally:
            pass
        self.stats.success('do_mmap_create')
        self.logger.info(f'do_mmap_create {abspath} succeed')
        return mm, len(mm)

    def do_mmap_read(self, file, mm: mmap.mmap, length):
        self.logger.debug(f'do_mmap_read {self.root_dir} {file} {length}')
        abspath = os.path.join(self.root_dir, file)
        try:
            length = length % mm.size()
            result = mm.read(length)
        except Exception as e :
            return self.handleException(e, 'do_mmap_read', abspath, length=length)
        finally:
            pass
        self.stats.success('do_mmap_read')
        self.logger.info(f'do_mmap_read {abspath} {length} succeed')
        return result

    def do_mmap_read_byte(self, file, mm:mmap.mmap):
        self.logger.debug(f'do_mmap_read_byte {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            result = mm.read_byte()
        except Exception as e :
            return self.handleException(e, 'do_mmap_read_byte', abspath)
        finally:
            pass
        self.stats.success('do_mmap_read_byte')
        self.logger.info(f'do_mmap_read_byte {abspath} succeed')
        return result
    
    def do_mmap_read_line(self, file, mm:mmap.mmap):
        self.logger.debug(f'do_mmap_read_line {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            result = mm.readline()
        except Exception as e :
            return self.handleException(e, 'do_mmap_read_line', abspath)
        finally:
            pass
        self.stats.success('do_mmap_read_line')
        self.logger.info(f'do_mmap_read_line {abspath} succeed')
        return result

    def do_mmap_write(self, file, mm:mmap.mmap, content):
        self.logger.debug(f'do_mmap_write {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            mm.write(content)
        except Exception as e :
            return self.handleException(e, 'do_mmap_write', abspath)
        finally:
            pass
        self.stats.success('do_mmap_write')
        self.logger.info(f'do_mmap_write {abspath} succeed')
        return mm.size(), mm.tell()

    def do_mmap_write_byte(self, file, mm: mmap.mmap, byte):
        self.logger.debug(f'do_mmap_write_byte {self.root_dir}')
        abspath = os.path.join(self.root_dir, file)
        try:
            mm.write_byte(byte)
        except Exception as e :
            return self.handleException(e, 'do_mmap_write_byte', abspath)
        finally:
            pass
        self.stats.success('do_mmap_write_byte')
        self.logger.info(f'do_mmap_write_byte {abspath} succeed')
        return 'succeed'

    def do_mmap_move(self, file, mm: mmap.mmap, dest, src, count):
        self.logger.debug(f'do_mmap_move {self.root_dir} {file} {dest} {src} {count}')
        abspath = os.path.join(self.root_dir, file)
        try:
            dest = dest % mm.size()
            src = src % mm.size()
            count = count % mm.size()
            mm.move(dest, src, count)
        except Exception as e :
            return self.handleException(e, 'do_mmap_move', abspath, dest=dest, src=src, count=count)
        finally:
            pass
        self.stats.success('do_mmap_move')
        self.logger.info(f'do_mmap_move {abspath} {dest} {src} {count} succeed')
        return mm.size(), mm.tell()

    def do_mmap_resize(self, file, mm: mmap.mmap):
        self.logger.debug(f'do_mmap_resize {self.root_dir}')
        abspath = os.path.join(self.root_dir, file)
        try:
            if self.client:
                newsize = self.client.stat(self.get_sdk_path(abspath)).st_size
            else:
                newsize = os.stat(abspath).st_size
            mm.resize(newsize)
        except Exception as e :
            return self.handleException(e, 'do_mmap_resize', self.root_dir)
        finally:
            pass
        self.stats.success('do_mmap_resize')
        self.logger.info(f'do_mmap_resize succeed')
        return mm.size()

    def do_mmap_seek(self, file, mm: mmap.mmap, offset, whence):
        self.logger.debug(f'do_mmap_seek {self.root_dir} {file} {offset} {whence}')
        abspath = os.path.join(self.root_dir, file)
        try:
            assert mm.size() != 0, red(f'do_mmap_seek size should not be 0')
            offset = offset % mm.size()
            mm.seek(offset, whence)
            pos = mm.tell()
        except Exception as e :
            return self.handleException(e, 'do_mmap_seek', abspath, offset=offset, whence=whence)
        finally:
            pass
        self.stats.success('do_mmap_seek')
        self.logger.info(f'do_mmap_seek {abspath} {offset} {whence} succeed')
        return pos
    
    def do_mmap_size(self, file, mm: mmap.mmap):
        self.logger.debug(f'do_mmap_size {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            size = mm.size()
        except Exception as e :
            return self.handleException(e, 'do_mmap_size', abspath)
        finally:
            pass
        self.stats.success('do_mmap_size')
        self.logger.info(f'do_mmap_size {abspath} succeed')
        return size

    def do_mmap_tell(self, file, mm: mmap.mmap):
        self.logger.debug(f'do_mmap_tell {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            pos = mm.tell()
        except Exception as e :
            return self.handleException(e, 'do_mmap_tell', abspath)
        finally:
            pass
        self.stats.success('do_mmap_tell')
        self.logger.info(f'do_mmap_tell {abspath} succeed')
        return pos

    def do_mmap_flush(self, file, mm: mmap.mmap):
        self.logger.debug(f'do_mmap_flush {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            mm.flush()
        except Exception as e :
            return self.handleException(e, 'do_mmap_flush', abspath)
        finally:
            pass
        self.stats.success('do_mmap_flush')
        self.logger.info(f'do_mmap_flush {abspath} succeed')
        return 'succeed'
    
    def do_mmap_close(self, file, mm: mmap.mmap):
        self.logger.debug(f'do_mmap_close {self.root_dir} {file}')
        abspath = os.path.join(self.root_dir, file)
        try:
            mm.close()
        except Exception as e :
            return self.handleException(e, 'do_mmap_close', abspath)
        finally:
            pass
        self.stats.success('do_mmap_close')
        self.logger.info(f'do_mmap_close {abspath} succeed')
        return 'succeed'