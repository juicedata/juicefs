# encoding: utf-8
# JuiceFS, Copyright 2025 Juicedata, Inc.
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

import sys
sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs
# import juicefs
import zlib
from typing import Union
import struct
import os
import struct
import zlib
from typing import List, Tuple, Optional
import io
import pickle
import numpy as np

MAX_SIZE = 512 * (1 << 20)  # 512 MB

def ffcrc32(code: int, data: Union[bytes, bytearray], length: int) -> int:
    start = 0
    while start < length:
        chunk_size = min(MAX_SIZE, length - start)
        code = zlib.crc32(data[start:start + chunk_size], code)
        start += chunk_size
    return code

class FileHeader:
    def __init__(self, jfscli: juicefs.Client, fname: str, check_data: bool = True):
        self.fname = fname
        self.fd = jfscli.open(fname, mode='rb')

        self.fd.seek(0)
        self.checksum_meta = self._read_uint32()
        self.n = self._read_uint64()

        self.checksums = [self._read_uint32() for _ in range(self.n)]
        self.fd.seek(4+8+4*self.n)
        self.offsets = [self._read_uint64() for _ in range(self.n + 1)]

        self.offsets[self.n] = jfscli.stat(fname).st_size

        if check_data:
            self.validate()
        self.fd.close()
        self.fd = jfscli.open(fname, mode='rb', buffering=0)
        self.aiofd = self.fd


    def _read_uint32(self) -> int:
        return struct.unpack('<I', self.fd.read(4))[0]

    def _read_uint64(self) -> int:
        return struct.unpack('<Q', self.fd.read(8))[0]

    def close_fd(self):
        if self.fd:
            self.fd.close()
            self.fd = None

    def validate(self):
        if self.checksum_meta == 0:
            print("Warning: you are using an old version ffrecord file, please update the file")
            return

        checksum = 0
        checksum = ffcrc32(checksum, struct.pack('<Q', self.n), 8)
        checksum = ffcrc32(checksum, struct.pack(f'<{len(self.checksums)}I', *self.checksums), 4 * len(self.checksums))
        checksum = ffcrc32(checksum, struct.pack(f'<{len(self.offsets)}Q', *self.offsets), 8 * len(self.offsets) - 8)
        assert checksum == self.checksum_meta, f"{self.fname}: checksum of metadata mismatched!"

    def access(self, index: int, use_aio: bool = False) -> Tuple[int, int, int, int]:
        fd = self.aiofd if use_aio else self.fd
        offset = self.offsets[index]
        length = self.offsets[index + 1] - self.offsets[index]
        checksum = self.checksums[index]
        return fd, offset, length, checksum


class FileReader:
    def __init__(self, fnames: List[str], check_data: bool = True):
        self.fnames = fnames
        self.check_data = check_data
        self.nfiles = len(fnames)
        self.n = 1000
        self.nsamples = [0]
        self.headers = []

    def close_fd(self):
        for header in self.headers:
            header.close_fd()
        self.headers = []
        self.n = 0
        self.nsamples = [0]
        return
    
    def open_fd(self):
        self.v = juicefs.Client("myjfs", "redis://localhost", cache_dir="/tmp/data", cache_size="0", debug=False)

        for fname in self.fnames:
            header = FileHeader(self.v, fname, self.check_data)
            self.headers.append(header)
            self.n += header.n
            self.nsamples.append(self.n)

    def validate(self):
        for header in self.headers:
            header.validate()

    def validate_sample(self, index: int, buf: bytes, checksum: int):
        if self.check_data:
            checksum2 = ffcrc32(0, buf, len(buf))
            assert checksum2 == checksum, f"Sample {index}: checksum mismatched!"

    def read(self, indices: List[int]):
        return self.read_batch(indices)

    def read_batch(self, indices: List[int]):
        assert not any(index >= self.n for index in indices), "Index out of range"
        results = []

        for index in indices:
            results.append(self.read_one(index))

        return results

    def read_one(self, index: int):
        assert index < self.n, "Index out of range"

        fid = 0
        while index >= self.nsamples[fid + 1]:
            fid += 1

        header = self.headers[fid]
        fd, offset, length, checksum = header.access(index - self.nsamples[fid], use_aio=False)

        fd.seek(offset)
        buf = fd.read(length)
        self.validate_sample(index, buf, checksum)
        res = pickle.loads(buf)
        return res
    
    def close(self):
        self.close_fd()

if __name__ == "__main__":
    fnames = ["/demo.ffr"]
    reader = FileReader(fnames, check_data=True)
    reader.open_fd()
    data = reader.read_one(0)
    print(data)
    data = pickle.loads(data)
    print(data["index"])
    print(data["txt"])
