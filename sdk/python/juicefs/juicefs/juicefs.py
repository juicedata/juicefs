# encoding: utf-8
# JuiceFS, Copyright 2024 Juicedata, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import codecs
import errno
import grp
import io
import json
import locale
import os
import pwd
import six
import struct
import threading
import time
from ctypes import *

# pkg/vfs/helpers.go
MODE_WRITE = 2
MODE_READ = 4

XATTR_CREATE = 1
XATTR_REPLACE = 2

def check_error(r, fn, args):
    if r < 0:
        formatted_args = []
        for arg in args[2:]:
            if isinstance(arg, (bytes, bytearray)) and len(arg) > 1024:
                formatted_args.append(f'bytes(len={len(arg)})')
            else:
                formatted_args.append(repr(arg))

        e = OSError(f'call {fn.__name__} failed: [Errno {-r}] {os.strerror(-r)}: {formatted_args}')
        e.errno = -r
        raise e
    return r

class FileInfo(Structure):
    _fields_ = [
        ('inode', c_uint64),
        ('mode', c_uint32),
        ('uid', c_uint32),
        ('gid', c_uint32),
        ('atime', c_uint32),
        ('mtime', c_uint32),
        ('ctime', c_uint32),
        ('nlink', c_uint32),
        ('length', c_uint64),
    ]

def _tid():
    return threading.current_thread().ident

def _bin(s):
    return six.ensure_binary(s)

def unpack(fmt, buf):
    if not fmt.startswith("!"):
        fmt = "!" + fmt
    return struct.unpack(fmt, buf[: struct.calcsize(fmt)])

class JuiceFSLib(object):
    def __init__(self):
        self.lib = cdll.LoadLibrary(os.path.join(os.path.dirname(__file__), "libjfs.so"))

    def __getattr__(self, n):
        fn = getattr(self.lib, n)
        if n == "jfs_init" or n == "jfs_lseek":
            fn.restype = c_int64
            fn.errcheck = check_error
        elif n.startswith("jfs"):
            fn.restype = c_int32
            fn.errcheck = check_error
        return fn

class Client(object):
    """A JuiceFS client."""
    def __init__(self, name, meta, *, bucket="", storage_class="", read_only=False, no_session=False, no_bgjob=True,
                 open_cache="0", backup_meta="3600", backup_skip_trash=False, heartbeat="12",
                 cache_dir="memory", cache_size="100M", free_space_ratio="0.1", cache_partial_only=False,
                 verify_cache_checksum="full", cache_eviction="2-random", cache_scan_interval="3600", cache_expire="0",
                 writeback=False, buffer_size="300M", prefetch=1, max_readahead="0", upload_limit="0",
                 download_limit="0", max_uploads=20, max_deletes=10, skip_dir_nlink=20, skip_dir_mtime="100ms",
                 io_retries=10, get_timeout="5", put_timeout="60", fast_resolve=False, attr_cache="1s",
                 entry_cache="0s", dir_entry_cache="1s", debug=False, no_usage_report=False, access_log="",
                 push_gateway="", push_interval="10", push_auth="", push_labels="", push_graphite="", **kwargs):
        self.lib = JuiceFSLib()
        kwargs["meta"] = meta
        kwargs["bucket"] = bucket
        kwargs["storageClass"] = storage_class
        kwargs["readOnly"] = read_only
        kwargs["noSession"] = no_session
        kwargs["noBGJob"] = no_bgjob
        kwargs["openCache"] = open_cache
        kwargs["backupMeta"] = backup_meta
        kwargs["backupSkipTrash"] = backup_skip_trash
        kwargs["heartbeat"] = heartbeat
        kwargs["cacheDir"] = cache_dir
        kwargs["cacheSize"] = cache_size
        kwargs["freeSpace"] = free_space_ratio
        kwargs["autoCreate"] = True
        kwargs["cacheFullBlock"] = not cache_partial_only
        kwargs["cacheChecksum"] = verify_cache_checksum
        kwargs["cacheEviction"] = cache_eviction
        kwargs["cacheScanInterval"] = cache_scan_interval
        kwargs["cacheExpire"] = cache_expire
        kwargs["writeback"] = writeback
        kwargs["memorySize"] = buffer_size
        kwargs["prefetch"] = prefetch
        kwargs["readahead"] = max_readahead
        kwargs["uploadLimit"] = upload_limit
        kwargs["downloadLimit"] = download_limit
        kwargs["maxUploads"] = max_uploads
        kwargs["maxDeletes"] = max_deletes
        kwargs["skipDirNlink"] = skip_dir_nlink
        kwargs["skipDirMtime"] = skip_dir_mtime
        kwargs["ioRetries"] = io_retries
        kwargs["getTimeout"] = get_timeout
        kwargs["putTimeout"] = put_timeout
        kwargs["fastResolve"] = fast_resolve
        kwargs["attrTimeout"] = attr_cache
        kwargs["entryTimeout"] = entry_cache
        kwargs["dirEntryTimeout"] = dir_entry_cache
        kwargs["debug"] = debug
        kwargs["noUsageReport"] = no_usage_report
        kwargs["accessLog"] = access_log
        kwargs["pushGateway"] = push_gateway
        kwargs["pushInterval"] = push_interval
        kwargs["pushAuth"] = push_auth
        kwargs["pushLabels"] = push_labels
        kwargs["pushGraphite"] = push_graphite
        kwargs["caller"] = 1

        jsonConf = json.dumps(kwargs)
        self.umask = os.umask(0)
        os.umask(self.umask)
        user = pwd.getpwuid(os.geteuid())
        groups = [grp.getgrgid(gid).gr_name for gid in os.getgrouplist(user.pw_name, user.pw_gid)]
        superuser = pwd.getpwuid(0)
        supergroups = [grp.getgrgid(gid).gr_name for gid in os.getgrouplist(superuser.pw_name, superuser.pw_gid)]
        self.h = self.lib.jfs_init(name.encode(), jsonConf.encode(), user.pw_name.encode(), ','.join(groups).encode(), superuser.pw_name.encode(), ''.join(supergroups).encode())

    def stat(self, path):
        """Get the status of a file or a directory."""
        fi = FileInfo()
        self.lib.jfs_stat(c_int64(_tid()), c_int64(self.h), _bin(path), byref(fi))
        return os.stat_result((fi.mode, fi.inode, 0, fi.nlink, fi.uid, fi.gid, fi.length, fi.atime, fi.mtime, fi.ctime))
    
    def exists(self, path):
        """Check if a file exists."""
        try:
            self.stat(path)
            return True
        except OSError as e:
            return False

    def open(self, path, mode='r', buffering=-1, encoding=None, errors=None):
        """Open a file, returns a filelike object."""
        if len(mode) != len(set(mode)):
            raise ValueError(f'invalid mode: {mode}')
        flag = 0
        cnt = 0
        for c in mode:
            if c in 'rwxa':
                cnt += 1
                if c == 'r':
                    flag |= MODE_READ
                else:
                    flag |= MODE_WRITE
            elif c == '+':
                flag |= MODE_READ | MODE_WRITE
            elif c not in 'tb':
                raise ValueError(f'invalid mode: {mode}')
        if cnt != 1:
            raise ValueError('must have exactly one of create/read/write/append mode')
        if 'b' in mode:
            if 't' in mode:
                raise ValueError("can't have text and binary mode at once")
            if encoding:
                raise ValueError("binary mode doesn't take an encoding argument")
            if errors:
                raise ValueError("binary mode doesn't take an errors argument")
        else:
            if not encoding:
                encoding = locale.getpreferredencoding(False).lower()
            if not errors:
                errors = 'strict'
            codecs.lookup(encoding)

        size = 0
        if 'x' in mode:
            fd = self.lib.jfs_create(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint16(0o666), c_uint16(self.umask))
        else:
            try:
                sz = c_uint64()
                fd = self.lib.jfs_open_posix(c_int64(_tid()), c_int64(self.h), _bin(path), byref(sz), c_int32(flag))
                if 'w' in mode:
                    self.lib.jfs_ftruncate(c_int64(_tid()), fd, c_uint64(0))
                else:
                    size = sz.value
            except OSError as e:
                if e.errno != errno.ENOENT:
                    raise e
                if 'r' in mode:
                    raise FileNotFoundError(e)
                fd = self.lib.jfs_create(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint16(0o666), c_uint16(self.umask))
        return File(self.lib, fd, path, mode, flag, size, buffering, encoding, errors)

    def truncate(self, path, size):
        """Truncate a file to a specified size."""
        self.lib.jfs_truncate(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint64(size))

    def remove(self, path):
        """Remove a file."""
        self.lib.jfs_delete(c_int64(_tid()), c_int64(self.h), _bin(path))

    def mkdir(self, path, mode=0o777):
        """Create a directory."""
        self.lib.jfs_mkdir(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint16(mode&0o777), c_uint16(self.umask))

    def makedirs(self, path, mode=0o777, exist_ok=False):
        """Create a directory and all its parent components if they do not exist."""
        self.lib.jfs_mkdirAll(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint16(mode&0o777), c_uint16(self.umask), c_bool(exist_ok))

    def rmdir(self, path):
        """Remove a directory. The directory must be empty."""
        self.lib.jfs_rmdir(c_int64(_tid()), c_int64(self.h), _bin(path))

    def rename(self, old, new):
        """Rename the file or directory old to new."""
        self.lib.jfs_rename0(c_int64(_tid()), c_int64(self.h), _bin(old), _bin(new), c_uint32(0))

    def listdir(self, path, detail=False):
        """Return a list containing the names of the entries in the directory given by path."""
        buf = c_void_p()
        size = c_int()
        # func jfs_listdir(pid int, h int64, cpath *C.char, offset int, buf uintptr, bufsize int) int {

        self.lib.jfs_listdir2(c_int64(_tid()), c_int64(self.h), _bin(path), bool(detail), byref(buf), byref(size))
        data = string_at(buf, size)
        infos = []
        pos = 0
        while pos < len(data):
            nlen, = unpack("H", data[pos:pos+2])
            pos += 2
            name = six.ensure_str(data[pos : pos + nlen], errors='replace')
            pos += nlen
            if detail:
                mode, inode, nlink, uid, gid, length, atime, mtime, ctime = \
                    unpack("IQIIIQIII", data[pos:pos+44])
                infos.append((name, os.stat_result((mode, inode, 0, nlink, uid, gid, length, atime, mtime, ctime))))
                pos += 44
            else:
                infos.append(name)
        self.lib.free(buf)
        return sorted(infos)
    
    def chmod(self, path, mode):
        """Change the mode of a file."""
        self.lib.jfs_chmod(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint16(mode))

    def chown(self, path, uid, gid):
        """Change the owner and group id of a file."""
        self.lib.jfs_chown(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint32(uid), c_uint32(gid))

    def link(self, src, dst):
        """Create a hard link to a file."""
        self.lib.jfs_link(c_int64(_tid()), c_int64(self.h), _bin(src), _bin(dst))

    def lstat(self, path):
        """Like stat(), but do not follow symbolic links."""
        info = FileInfo()
        self.lib.jfs_lstat(c_int64(_tid()), c_int64(self.h), _bin(path), byref(info))
        return os.stat_result((info.mode, info.inode, 0, info.nlink, info.uid, info.gid, info.length, info.atime, info.mtime, info.ctime))

    def readlink(self, path):
        """Return a string representing the path to which the symbolic link points."""
        buf = bytes(1<<16)
        n = self.lib.jfs_readlink(c_int64(_tid()), c_int64(self.h), _bin(path), buf, c_int32(len(buf)))
        return buf[:n].decode()

    def symlink(self, src, dst):
        """Create a symbolic link."""
        self.lib.jfs_symlink(c_int64(_tid()), c_int64(self.h), _bin(src), _bin(dst))

    def unlink(self, path):
        """Remove a file."""
        self.lib.jfs_unlink(c_int64(_tid()), c_int64(self.h), _bin(path))

    def rmr(self, path):
        """Remove a directory and all its contents recursively."""
        self.lib.jfs_rmr(c_int64(_tid()), c_int64(self.h), _bin(path))

    def utime(self, path, times=None):
        """Set the access and modified times of a file."""
        if not times:
            now = time.time()
            times = (now, now)
        self.lib.jfs_utime(c_int64(_tid()), c_int64(self.h), _bin(path), c_int64(int(times[1]*1000)), c_int64(int(times[0]*1000)))

    def walk(self, top, topdown=True, onerror=None, followlinks=False):
        raise NotImplementedError

    def getxattr(self, path, name):
        """Get an extended attribute on a file."""
        size = 64 << 10 # XattrSizeMax
        buf = bytes(size)
        size = self.lib.jfs_getXattr(c_int64(_tid()), c_int64(self.h), _bin(path), _bin(name), buf, c_int32(size))
        return buf[:size]

    def listxattr(self, path):
        """List extended attributes on a file."""
        buf = c_void_p()
        size = c_int()
        self.lib.jfs_listXattr2(c_int64(_tid()), c_int64(self.h), _bin(path), byref(buf), byref(size))
        data = string_at(buf, size).decode()
        self.lib.free(buf)
        if not data:
            return []
        return data.split('\0')[:-1]

    def setxattr(self, path, name, value, flags=0):
        """Set an extended attribute on a file."""
        value = _bin(value)
        self.lib.jfs_setXattr(c_int64(_tid()),  c_int64(self.h), _bin(path), _bin(name), value, c_int32(len(value)), c_int32(flags))

    def removexattr(self, path, name):
        """Remove an extended attribute from a file."""
        self.lib.jfs_removeXattr(c_int64(_tid()), c_int64(self.h), _bin(path), _bin(name))

    def clone(self, src, dst, preserve=False):
        """Clone a file."""
        self.lib.jfs_clone(c_int64(_tid()), c_int64(self.h), _bin(src), _bin(dst), c_bool(preserve))

    def set_quota(self, path, capacity=0, inodes=0, create=False, strict=False):
        """Set the quota of a directory."""
        self._quota(0, path, capacity, inodes, create=create, strict=strict)
    
    def get_quota(self, path):
        """Get the quota of a directory."""
        return self._quota(1, path)
    
    def del_quota(self, path):
        """Delete the quota of a directory."""
        self._quota(2, path)

    def list_quota(self):
        """List the quota of all directories."""
        return self._quota(3)

    def check_quota(self, path, repair=False, strict=False):
        """Check the quota of a directory."""
        return self._quota(4, path, repair=repair, strict=strict)

    def _quota(self, cmd, path="", capacity=0, inodes=0, create=False, repair=False, strict=False):
        """Get the quota of a directory."""
        buf = c_void_p()
        n = self.lib.jfs_quota(c_int64(_tid()), c_int64(self.h), _bin(path), c_uint8(cmd), c_uint64(capacity), c_uint64(inodes), c_bool(strict), c_bool(repair), c_bool(create), byref(buf))
        data = string_at(buf, n)
        res = json.loads(str(data, encoding='utf-8'))
        self.lib.free(buf)
        return res

    def info(self, path, recursive=False, strict=False):
        """Get the information of a file or a directory."""
        buf = c_void_p()
        n = self.lib.jfs_info(c_int64(_tid()), c_int64(self.h), _bin(path), byref(buf), c_bool(recursive), c_bool(strict))
        data = string_at(buf, n)
        res = json.loads(str(data, encoding='utf-8'))

        self.lib.free(buf)
        return res

    def summary(self, path, depth=0, entries=1):
        """Get the summary of a directory."""
        buf = c_void_p()

        n = self.lib.jfs_gettreesummary(_tid(), self.h, _bin(path), c_uint8(depth), c_uint32(entries), byref(buf))
        data = string_at(buf, n)
        res = json.loads(str(data, encoding='utf-8'))

        def parseSummary(entry, removefields):
            for f in removefields:
                entry.pop(f, None)

            if entry["Dirs"] == 0:
                entry.pop("Children", None)
            elif entry.get("Children") is not None:
                for v in entry["Children"]:
                    parseSummary(v, removefields)

        parseSummary(res, ["Inode"])
        self.lib.free(buf)
        return res

    def warmup(self, paths, numthreads=10, background=False, isEvict=False, isCheck=False):
        """Warm up a file or a directory."""
        if type(paths) is not list:
            paths = [paths]

        buf = c_void_p()

        n = self.lib.jfs_warmup(c_int64(_tid()), c_int64(self.h), json.dumps(paths).encode(), c_int32(numthreads), c_bool(background), c_bool(isEvict), c_bool(isCheck), byref(buf))
        res = json.loads(str(string_at(buf, n), encoding='utf-8'))
        self.lib.free(buf)
        return res

    def status(self, trash=False, session=0):
        """Get the status of the volume and client sessions."""
        buf = c_void_p()
        n = self.lib.jfs_status(c_int64(_tid()), c_int64(self.h), c_bool(trash), c_bool(session), byref(buf))
        res = json.loads(str(string_at(buf, n), encoding='utf-8'))
        self.lib.free(buf)
        return res

class File(object):
    """A JuiceFS file."""
    def __init__(self, lib, fd, path, mode, flag, length, buffering, encoding, errors):
        self.lib = lib
        self.fd = fd
        self.name = path
        self.append = 'a' in mode
        self.flag = flag
        self.length = length
        self.encoding = encoding
        self.errors = errors
        self.newlines = None
        self.closed = False
        self._buffering = buffering
        if self._buffering < 0:
            self._buffering = 128 << 10
        if flag == MODE_READ | MODE_WRITE:
            self._buffering = 0
        self._readbuf = None
        self._readbuf_off = 0
        self._writebuf = []
        self.off = 0
        if self.append:
            self.off = self.length

    def __fspath__(self):
        return self.name

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        self.close()

    def __iter__(self):
        return self

    def __next__(self):
        return self.next()

    def next(self):
        lines = self.readlines(1)
        if lines:
            return lines[0]
        raise StopIteration

    def fileno(self):
        return self.fd

    def isatty(self):
        return False

    def _read(self, size):
        self._check_closed()
        if self.flag & MODE_READ == 0:
            raise io.UnsupportedOperation('not readable')
        # fill buffer
        if (not self._readbuf or self._readbuf_off == len(self._readbuf)) and size < self._buffering:
            if not self._readbuf or len(self._readbuf) < self._buffering:
                self._readbuf = bytes(self._buffering)
            n = self.lib.jfs_pread(c_int64(_tid()), c_int32(self.fd), self._readbuf, c_int32(self._buffering), c_int64(self.off))
            if n < self._buffering:
                self._readbuf = self._readbuf[:n]
            self._readbuf_off = 0
        # read from buffer
        rs = []
        got = 0
        if self._readbuf and self._readbuf_off < len(self._readbuf):
            n = len(self._readbuf) - self._readbuf_off
            if size >= 0 and size < n:
                n = size
            rs.append(self._readbuf[self._readbuf_off:self._readbuf_off+n])
            self._readbuf_off += n
            got += n
            size -= n
        # read directly
        if size > 0:
            while size > 0:
                n = min(size, 4 << 20)
                buf = bytes(n)
                n = self.lib.jfs_pread(c_int64(_tid()), c_int32(self.fd), buf, c_int32(n), c_int64(self.off+got))
                if n == 0:
                    break
                if n < len(buf):
                    buf = buf[:n]
                rs.append(buf)
                got += n
                size -= n
        elif size < 0:
            while True:
                buf = bytes(128 << 10)
                n = self.lib.jfs_pread(c_int64(_tid()), c_int32(self.fd), buf, c_int32(len(buf)), c_int64(self.off+got))
                if n == 0:
                    break
                if n < len(buf):
                    buf = buf[:n]
                rs.append(buf)
                got += n
        if len(rs) == 1:
            buf = rs[0]
        else:
            buf = b''.join(rs)
        self.off += len(buf)
        return buf

    def read(self, size=-1):
        """Read at most size bytes, returned as a string."""
        buf = self._read(size)
        if self.encoding:
            return buf.decode(self.encoding, self.errors)
        else:
            return buf

    def write(self, data):
        """Write the string data to the file."""
        self._check_closed()
        # TODO: buffer for small write
        if self.encoding and not isinstance(data, six.text_type):
            raise TypeError(f'write() argument must be str, not {type(data).__name__}')
        if not self.encoding and not isinstance(data, six.binary_type):
            raise TypeError(f"a bytes-like object is required, not '{type(data).__name__}'")
        if self.flag & MODE_WRITE == 0:
            raise io.UnsupportedOperation('not writable')

        if not data:
            return 0
        n = len(data)
        if self.encoding:
            data = data.encode(self.encoding, self.errors)
        if self.append:
            self.off = self.length
        total = len(data)
        for b in self._writebuf:
            total += len(b)
        if total >= self._buffering:
            self.flush()
            if len(data) < self._buffering:
                self._writebuf.append(data)
            else:
                self.lib.jfs_pwrite(c_int64(_tid()), c_int32(self.fd), data, c_int32(len(data)), c_int64(self.off))
        else:
            self._writebuf.append(data)
        self.off += len(data)
        if self.off > self.length:
            self.length = self.off
        return n

    def seek(self, offset, whence=0):
        """Set the stream position to the given byte offset.
        offset is interpreted relative to the position indicated by whence.
        The default value for whence is SEEK_SET."""
        self._check_closed()
        if whence not in (os.SEEK_SET, os.SEEK_CUR, os.SEEK_END):
            raise ValueError(f'invalid whence ({whence}, should be {os.SEEK_SET}, {os.SEEK_CUR} or {os.SEEK_END})')
        if self.encoding:
            if whence == os.SEEK_CUR and offset != 0:
                raise io.UnsupportedOperation("can't do nonzero cur-relative seeks")
            if whence == os.SEEK_END and offset != 0:
                raise io.UnsupportedOperation("can't do nonzero end-relative seeks")
        self.flush()
        if whence == os.SEEK_SET:
            self.off = offset
            self._readbuf = None
        elif whence == os.SEEK_CUR:
            self.off += offset
            self._readbuf_off += offset
            if self._readbuf and (self._readbuf_off < 0 or self._readbuf_off >= len(self._readbuf)):
                self._readbuf = None
        else:
            self.off = self.length + offset
            self._readbuf = None
        return self.off

    def tell(self):
        """Return the current stream position."""
        self._check_closed()
        return self.off

    def truncate(self, size=None):
        """Truncate the file to at most size bytes.
        Size defaults to the current file position, as returned by tell()."""
        self._check_closed()
        if self.flag & MODE_WRITE == 0:
            raise io.UnsupportedOperation('File not open for writing')
        self.flush()
        if size is None:
            size = self.tell()
        self.lib.jfs_ftruncate(c_int64(_tid()), c_int32(self.fd), c_uint64(size))
        self.length = size
        return size

    def flush(self):
        """Flush the write buffers of the file if applicable.
        This does nothing for read-only and non-blocking streams."""
        if self._writebuf:
            data = b''.join(self._writebuf)
            self.lib.jfs_pwrite(c_int64(_tid()), c_int32(self.fd), data, c_int32(len(data)), c_int64(self.off-len(data)))
            self._writebuf = []

    def fsync(self):
        """Force write file data to the backend storage."""
        self.flush()
        self.lib.jfs_fsync(c_int64(_tid()), c_int32(self.fd))

    def close(self):
        """Close the file. A closed file cannot be used for further I/O operations."""
        if self.closed:
            return
        self.flush()
        self.lib.jfs_close(c_int64(_tid()), c_int32(self.fd))
        self.closed = True

    def __del__(self):
        if not self.closed:
            self.close()

    def _check_closed(self):
        if self.closed:
            raise ValueError('I/O operation on closed file.')

    def readline(self): # TODO: add parameter `size=-1`
        """Read until newline or EOF."""
        ls = self.readlines(1)
        if ls:
            return ls[0]
        return '' if self.encoding else b''

    def xreadlines(self):
        return self

    def readlines(self, hint=-1):
        """Return a list of lines from the stream."""
        self._check_closed()
        if hint == -1:
            data = self._read(-1)
        else:
            rs = []
            while hint > 0:
                r = self._read(1)
                if not r:
                    break
                rs.append(r)
                if r[0] == b'\n':
                    hint -= 1
            data = b''.join(rs)
        if self.encoding:
            return [l.decode(self.encoding, self.errors) for l in data.splitlines(True)]
        return data.splitlines(True)

    def writelines(self, lines):
        """Write a list of lines to the file."""
        self._check_closed()
        self.write(''.join(lines) if self.encoding else b''.join(lines))
        self.flush()

def test():
    volume = os.getenv("JFS_VOLUME", "test")
    meta = os.getenv("JFS_META", "redis://localhost")
    v = Client(volume, meta, access_log="/tmp/jfs.log")
    print(v.status())
    st = v.stat("/")
    print(st)
    if v.exists("/d"):
        v.rmr("/d")
    v.makedirs("/d")
    if v.exists("/d/file"):
        v.remove("/d/file")
    with v.open("/d/file", "w") as f:
        f.write("hello")
    with v.open("/d/file", "a+") as f:
        f.write("world")
    with v.open("/d/file") as f:
        data = f.read()
        assert data == "helloworld"
    with v.open("/d/file", "w") as f:
        f.write("hello")
    with v.open("/d/file", 'rb', 5) as f:
        data = f.readlines()
        assert data == [b"hello"]
    print(list(v.open("/d/file")))
    assert list(v.open("/d/file")) == ['hello']
    try:
        v.open("/d/d/file", "w")
    except OSError as e:
        if e.errno != errno.ENOENT:
            raise e
    else:
        raise AssertionError
    v.chmod("/d/file", 0o777)
    # v.chown("/d/file", 0, 0)
    v.symlink("/d/file", "/d/link")
    assert v.readlink("/d/link") == "file"
    v.unlink("/d/link")
    v.link("/d/file", "/d/link")
    v.rename("/d/link", "/d/link2")
    names = sorted(v.listdir("/d"))
    assert names == ["file", "link2"]
    v.setxattr("/d/file", "user.key", b"value\0")
    xx = v.getxattr("/d/file", "user.key")
    assert xx == b"value\0"
    print(v.listxattr("/d/file"))
    assert v.listxattr("/d/file") == ["user.key"]
    v.removexattr("/d/file", "user.key")
    assert v.listxattr("/d/file") == []
    with v.open("/d/file", "a") as f:
        f.seek(0, 0)
        f.write("world")
        assert f.truncate(2) == 2
        assert f.seek(0, 2) == 2
    assert v.open("/d/file").read() == "he"
    k=1024
    start = time.time()
    size = 0
    with v.open("/bigfile", mode="wb") as f:
        for i in range(4000):
            f.write(b"!"*(k*k))
            size += k*k
    print("write time:", time.time()-start, size>>20)
    start = time.time()
    size = 0
    with v.open("/bigfile",mode='rb') as f:
        while True:
            t = f.read(4*k)
            if not t: break
            size += len(t)
    print("read time:", time.time()-start, size>>20)

if __name__ == '__main__':
    test()

