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

import zlib
import os
import struct
from typing import List, Tuple, Union
import numpy as np

MAX_SIZE = 512 * (1 << 20)  # 512 MB
DIRECTIO_BLOCK_SIZE = 1 * (1 << 20)  # 1 MB

def ffcrc32(code: int, data: Union[bytes, bytearray], length: int) -> int:
    start = 0
    while start < length:
        chunk_size = min(MAX_SIZE, length - start)
        code = zlib.crc32(data[start:start + chunk_size], code)
        start += chunk_size
    return code

class FileHeader:
    def __init__(self, fname: str, check_data: bool = True):
        print(f"__init__ self: {hex(id(self))}")
        print(f"pid: {os.getpid()}")
        self.fname = fname
        self.fd = os.open(fname, os.O_RDONLY | os.O_DIRECT)
        self.aiofd = self.fd 

        self.file_obj = os.fdopen(self.fd, 'rb', buffering=0)

        self.checksum_meta = self._read_uint32()
        self.n = self._read_uint64()

        checksums_size = 4 * self.n
        offsets_size = 8 * (self.n + 1)
        combined_data = self.file_obj.read(checksums_size + offsets_size)
        self.checksums = list(struct.unpack(f'<{self.n}I', combined_data[:checksums_size]))
        self.offsets = list(struct.unpack(f'<{self.n + 1}Q', combined_data[checksums_size:checksums_size + offsets_size]))

        self.offsets[self.n] = os.path.getsize(fname)
        if check_data:
            self.validate()

        print("FileHeader initialized for:", fname, "fd:", self.fd)

    def _read_uint32(self) -> int:
        return struct.unpack('<I', self.file_obj.read(4))[0]

    def _read_uint64(self) -> int:
        return struct.unpack('<Q', self.file_obj.read(8))[0]

    def close_fd(self):
        print("close fd: ", self.fd)
        if self.fd != -1:
            os.close(self.fd)
            self.fd = -1
            self.file_obj = None
    
    def open_fd(self):
        if self.fd == -1:
            self.fd = os.open(self.fname, os.O_RDONLY | os.O_DIRECT)
            self.aiofd = self.fd
            print(f"header.open_fd: {self.fd} address: {hex(id(self))} pid: {os.getpid()}")

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
        self.n = 0
        self.nsamples = [0]
        self.headers = []

        for fname in fnames:
            header = FileHeader(fname, check_data)
            self.headers.append(header)
            self.n += header.n
            self.nsamples.append(self.n)

    def close_fd(self):
        for header in self.headers:
            header.close_fd()
    
    def open_fd(self):
      print(f"open_fd address: {hex(id(self))} pid: {os.getpid()}")
      for header in self.headers:
          header.open_fd()

    def validate(self):
        for header in self.headers:
            header.validate()

    def validate_sample(self, index: int, buf: bytes, checksum: int):
        if self.check_data:
            checksum2 = ffcrc32(0, buf, len(buf))
            assert checksum2 == checksum, f"Sample {index}: checksum mismatched!"

    def read_batch(self, indices: List[int]) -> List[np.array]:
        assert not any(index >= self.n for index in indices), "Index out of range"
        results = []

        for index in indices:
            results.append(self.read_one(index))

        return results

    def read_one(self, index: int) -> np.array:
        assert index < self.n, "Index out of range"

        fid = 0
        while index >= self.nsamples[fid + 1]:
            fid += 1

        header = self.headers[fid]
        fd, offset, length, checksum = header.access(index - self.nsamples[fid], use_aio=False)

        buf = bytearray(length)
        start = 0
        while start < length:
            chunk_size = min(DIRECTIO_BLOCK_SIZE, length - start)
            read_bytes = os.pread(fd, chunk_size, offset + start)
            buf[start:start + chunk_size] = read_bytes
            start += chunk_size

        self.validate_sample(index, buf, checksum)
        array = np.frombuffer(buf, dtype=np.uint8)

        return array


if __name__ == "__main__":
    fnames = ["/demo.ffr"]
    reader = FileReader(fnames, check_data=True)
    data = reader.read_one(0)
    print(data)
