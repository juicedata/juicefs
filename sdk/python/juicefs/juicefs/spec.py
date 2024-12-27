# encoding: utf-8
# JuiceFS, Copyright 2020 Juicedata, Inc.
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

import datetime
import logging
import uuid
import os
from stat import S_ISDIR, S_ISLNK, S_ISREG

from fsspec.spec import AbstractFileSystem, AbstractBufferedFile

from .juicefs import Client

logger = logging.getLogger("fsspec.jfs")


class JuiceFS(AbstractFileSystem):
    """
    A JuiceFS file system.
    """
    protocol = "jfs", "juicefs"
    def __init__(self, name, auto_mkdir=False, **kwargs):
        if self._cached:
            return
        super().__init__(**kwargs)
        self.auto_mkdir = auto_mkdir
        self.temppath = kwargs.pop("temppath", "/tmp")
        self.fs = Client(name, **kwargs)

    @property
    def fsid(self):
        return "jfs_" + self.fs.name

    def makedirs(self, path, exist_ok=False, mode=511):
        if self.exists(path) and not exist_ok:
            raise FileExistsError(f"File exists: {path}")
        self.fs.makedirs(self._strip_protocol(path), mode)

    def mkdir(self, path, create_parents=True, mode=0o511):
        if self.exists(path):
            raise FileExistsError(f"File exists: {path}")
        if create_parents:
            self.fs.makedirs(self._strip_protocol(path), mode=mode)
        else:
            self.fs.mkdir(self._strip_protocol(path), mode)

    def rmdir(self, path):
        self.fs.rmdir(self._strip_protocol(path))

    def ls(self, path, detail=False, **kwargs):
        infos = self.fs.listdir(self._strip_protocol(path), detail)
        if not detail:
            return infos
        stats = []
        for name, st in infos:
            info = {
                "name": os.path.join(path, name),
                "size": st.st_size,
                "type": "directory" if S_ISDIR(st.st_mode) else "link" if S_ISLNK(st.st_mode) else "file",
                "mode": st.st_mode,
                "ino": st.st_ino,
                "nlink": st.st_nlink,
                "uid": st.st_uid,
                "gid": st.st_gid,
                "created": st.st_atime,
                "mtime": st.st_mtime,
            }
            if S_ISLNK(st.st_mode):
                info.update(**self.info(f"{path}/{name}"))
            stats.append(info)
        return stats

    def du(self, path, total=True, maxdepth=None, withdirs=False, **kwargs):
        if total:
            info = self.info(path)
            return info["size"]
        return super().du(path, total=total, maxdepth=maxdepth, withdirs=withdirs, **kwargs)

    def info(self, path):
        path = self._strip_protocol(path)
        try:
            st = self.fs.lstat(path)
        except OSError:
            raise FileNotFoundError(path)
        info = {
            "name": path,
        }
        if S_ISLNK(st.st_mode):
            info['destination'] = self.fs.readlink(path)
            st = self.fs.stat(path)
        info.update({
            "type": "directory" if S_ISDIR(st.st_mode) else "file" if S_ISREG(st.st_mode) else "other",
            "size": st.st_size,
            "uid": st.st_uid,
            "gid": st.st_gid,
            "created": st.st_atime,
            "mtime": st.st_mtime,
        })
        return info

    def lexists(self, path, **kwargs):
        try:
            self.fs.lstat(self._strip_protocol(path))
            return True
        except OSError:
            return False

    def cp_file(self, path1, path2, **kwargs):
        if self.isfile(path1):
            if self.auto_mkdir:
                self.makedirs(self._parent(path2), exist_ok=True)
            self.fs.clone(self._strip_protocol(path1), self._strip_protocol(path2))
        else:
            self.mkdirs(path2, exist_ok=True)

    def rm(self, path, recursive=False, maxdepth=None):
        if not isinstance(path, list):
            path = [path]
        for p in path:
            if recursive:
                self.fs.rmr(self._strip_protocol(p))
            else:
                self.fs.remove(self._strip_protocol(p))

    def _rm(self, path):
        self.fs.remove(self._strip_protocol(path))

    def mv(self, old, new, recursive=False, maxdepth=None, **kwargs):
        self.fs.rename(self._strip_protocol(old), self._strip_protocol(new))

    def link(self, src, dst, **kwargs):
        src = self._strip_protocol(src)
        dst = self._strip_protocol(dst)
        self.fs.link(src, dst, **kwargs)

    def symlink(self, src, dst, **kwargs):
        src = self._strip_protocol(src)
        dst = self._strip_protocol(dst)
        self.fs.symlink(src, dst, **kwargs)

    def islink(self, path) -> bool:
        try:
            self.fs.readlink(self._strip_protocol(path))
            return True
        except OSError:
            return False

    def _open(self, path, mode="rb", block_size=None, autocommit=True, **kwargs):
        path = self._strip_protocol(path)
        if self.auto_mkdir and "w" in mode:
            self.makedirs(self._parent(path), exist_ok=True)
        return JuiceFile(self, path, mode, block_size, autocommit, **kwargs)

    def touch(self, path, truncate=True, **kwargs):
        path = self._strip_protocol(path)
        if self.auto_mkdir:
            self.makedirs(self._parent(path), exist_ok=True)
        if truncate or not self.exists(path):
            with self.open(path, "wb", **kwargs):
                pass
        else:
            self.fs.utime(self._strip_protocol(path))

    @classmethod
    def _parent(cls, path):
        path = cls._strip_protocol(path)
        if os.sep == "/":
            # posix native
            return path.rsplit("/", 1)[0] or "/"
        else:
            # NT
            path_ = path.rsplit("/", 1)[0]
            if len(path_) <= 3:
                if path_[1:2] == ":":
                    # nt root (something like c:/)
                    return path_[0] + ":/"
            # More cases may be required here
            return path_

    def created(self, path):
        return datetime.datetime.fromtimestamp(
            self.info(path)["created"], tz=datetime.timezone.utc
        )

    def modified(self, path):
        return datetime.datetime.fromtimestamp(
            self.info(path)["mtime"], tz=datetime.timezone.utc
        )

    def _isfilestore(self):
        # Inheriting from DaskFileSystem makes this False (S3, etc. were)
        # the original motivation. But we are a posix-like file system.
        # See https://github.com/dask/dask/issues/5526
        return True

    def chmod(self, path, mode):
        path = self._strip_protocol(path)
        return self.fs.chmod(path, mode)


class JuiceFile(AbstractBufferedFile):
    def __init__(self, fs, path, mode="rb", block_size=None, autocommit=True, cache_options=None, **kwargs):
        super().__init__(fs, path, mode, block_size, autocommit, cache_options=cache_options, **kwargs)
        if autocommit:
            self.temp = path
        self.f = None
        self._open()

    def _open(self):
        if self.f is None or self.f.closed:
            if self.autocommit or "w" not in self.mode:
                self.f = self.fs.fs.open(self.path, self.mode, buffering=self.blocksize)
            else:
                self.temp = "/".join([self.fs.temppath, str(uuid.uuid4())])
                self.f = open(self.temp, self.mode, buffering=self.blocksize)
            if "w" not in self.mode:
                self.size = self.f.seek(0, 2)
                self.f.seek(0)

    def _fetch_range(self, start, end):
        # probably only used by cached FS
        if "r" not in self.mode:
            raise ValueError
        self._open()
        self.f.seek(start)
        return self.f.read(end - start)

    def __setstate__(self, state):
        self.f = None
        loc = state.pop("loc", None)
        self.__dict__.update(state)
        if "r" in state["mode"]:
            self.f = None
            self._open()
            self.f.seek(loc)

    def __getstate__(self):
        d = self.__dict__.copy()
        d.pop("f")
        if "r" in self.mode:
            d["loc"] = self.f.tell()
        else:
            if not self.f.closed:
                raise ValueError("Cannot serialise open write-mode local file")
        return d

    def commit(self):
        if self.autocommit:
            raise RuntimeError("Can only commit if not already set to autocommit")
        self.fs.fs.rename(self.temp, self.path)

    def discard(self):
        if self.autocommit:
            raise RuntimeError("Can only commit if not already set to autocommit")
        self.fs.fs.remove(self.temp)

    def tell(self):
        return self.f.tell()

    def seek(self, loc, whence=0):
        return self.f.seek(loc, whence)

    def write(self, data):
        return self.f.write(data)

    def read(self, length=-1):
        return self.f.read(length)

    def flush(self, force=True):
        return self.f.flush()

    def truncate(self, size=None):
        return self.f.truncate(size)

    def close(self):
        super().close()
        if getattr(self, "_unclosable", False):
            return
        self.f.close()

    def __getattr__(self, item):
        return getattr(self.f, item)

    def __del__(self):
        pass

from fsspec.registry import register_implementation
register_implementation("jfs", JuiceFS, True)
register_implementation("juicefs", JuiceFS, True)
