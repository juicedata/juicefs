# JuiceFS, Copyright 2026 Juicedata, Inc.
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

import contextlib
import io
from pathlib import Path
import pickle
import runpy
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib

import filereader


def write_record(path, samples):
    payloads = [pickle.dumps(sample) for sample in samples]
    position = 12 + 12 * len(payloads)
    offsets = []
    for payload in payloads:
        offsets.append(position)
        position += len(payload)
    metadata = (
        struct.pack('<Q', len(payloads))
        + struct.pack(f'<{len(payloads)}I', *(zlib.crc32(p) for p in payloads))
        + struct.pack(f'<{len(payloads)}Q', *offsets)
    )
    path.write_bytes(struct.pack('<I', zlib.crc32(metadata)) + metadata + b''.join(payloads))


class FileReaderTests(unittest.TestCase):
    def setUp(self):
        stack = self.enterContext(contextlib.ExitStack())
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.path = root / 'demo.ffr'
        self.samples = [
            {'index': 7, 'txt': 'first sample', 'jpg': b'\x00\xff'},
            {'index': 8, 'txt': 'second sample', 'jpg': b'\x01\xfe'},
        ]
        write_record(self.path, self.samples)
        path = self.path

        class LocalFiles:
            def open(self, name, mode='rb', **kwargs):
                if name != '/demo.ffr':
                    raise ValueError(name)
                return stack.enter_context(path.open(mode, **kwargs))

            def stat(self, name):
                if name != '/demo.ffr':
                    raise ValueError(name)
                return path.stat()

        self.enterContext(patch.object(filereader.juicefs, 'Client', return_value=LocalFiles()))

    def reader(self):
        reader = filereader.FileReader(['/demo.ffr'], check_data=True)
        self.addCleanup(reader.close)
        reader.open_fd()
        return reader

    def test_read_one_and_batch_return_deserialized_samples(self):
        reader = self.reader()
        self.assertEqual(reader.read_one(0), self.samples[0])
        self.assertEqual(reader.read_batch([1, 0]), self.samples[::-1])

    def test_main_uses_deserialized_sample(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            state = runpy.run_path(filereader.__file__, run_name='__main__')
        self.addCleanup(state['reader'].close)
        self.assertEqual(state['data'], self.samples[0])
        self.assertEqual(output.getvalue().splitlines()[-2:], ['7', 'first sample'])

    def test_corrupt_metadata_is_rejected(self):
        data = bytearray(self.path.read_bytes())
        data[0] ^= 1
        self.path.write_bytes(data)
        with self.assertRaisesRegex(AssertionError, 'checksum of metadata mismatched'):
            self.reader()

    def test_corrupt_sample_is_rejected(self):
        data = bytearray(self.path.read_bytes())
        data[-1] ^= 1
        self.path.write_bytes(data)
        reader = self.reader()
        with self.assertRaisesRegex(AssertionError, 'Sample 1: checksum mismatched'):
            reader.read_one(1)


if __name__ == '__main__':
    unittest.main()
