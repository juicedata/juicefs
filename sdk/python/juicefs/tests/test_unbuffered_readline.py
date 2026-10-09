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

import ctypes
import io
import os

import pytest

from juicefs import Client


class LocalReadLib:
    """Exercise the Python stream with local pread, without a metadata service."""

    def jfs_open_posix(self, tid, handle, path, size, flags):
        fd = os.open(path, os.O_RDONLY)
        ctypes.cast(size, ctypes.POINTER(ctypes.c_uint64))[0] = os.fstat(fd).st_size
        return fd

    def jfs_pread(self, tid, fd, buffer, size, offset):
        data = os.pread(fd.value, size.value, offset.value)
        ctypes.memmove(buffer, data, len(data))
        return len(data)

    def jfs_close(self, tid, fd):
        os.close(fd.value)


@pytest.fixture
def open_file(tmp_path):
    client = Client.__new__(Client)
    client.lib = LocalReadLib()
    client.h = 0
    client.umask = 0

    def open_content(data, buffering=0):
        path = tmp_path / "lines"
        path.write_bytes(data)
        return client.open(str(path), 'rb', buffering=buffering)

    return open_content


LINE_CONTENTS = [
    b"first\nsecond\nlast", b"\n\nlast", b"one\r\ntwo\r\n", b"last",
    b"one\rtwo\nlast", b"\r", b"\r\n\r",
]


@pytest.mark.parametrize("data", LINE_CONTENTS)
def test_unbuffered_readline_preserves_remaining_lines(open_file, data):
    expected = io.BytesIO(data)
    with open_file(data) as stream:
        while expected.tell() < len(data):
            assert stream.readline() == expected.readline()
            assert stream.tell() == expected.tell()
        assert stream.readline() == b''


@pytest.mark.parametrize("data", LINE_CONTENTS + [b""])
@pytest.mark.parametrize("buffering", [0, -1])
def test_iteration_preserves_all_lines(open_file, data, buffering):
    with open_file(data, buffering) as stream:
        assert list(stream) == list(io.BytesIO(data))


def test_unbuffered_readlines_one_leaves_the_next_line(open_file):
    with open_file(b"first\nsecond\n") as stream:
        assert stream.readlines(1) == [b"first\n"]
        assert stream.tell() == len(b"first\n")
        assert stream.read() == b"second\n"


@pytest.mark.parametrize("hint", [1, 2, 3, 4])
def test_unbuffered_positive_hint_counts_lines(open_file, hint):
    lines = [b"first long line\n", b"\n", b"last"]
    with open_file(b"".join(lines)) as stream:
        assert stream.readlines(hint) == lines[:hint]
        assert stream.read() == b"".join(lines[hint:])


@pytest.mark.parametrize("buffering", [0, -1])
@pytest.mark.parametrize("data", LINE_CONTENTS + [b""])
def test_readlines_without_hint_preserves_all_lines(open_file, buffering, data):
    with open_file(data, buffering) as stream:
        assert stream.readlines() == list(io.BytesIO(data))


@pytest.mark.parametrize("buffering", [0, -1])
@pytest.mark.parametrize("hint", [-2, 0, None])
@pytest.mark.parametrize("offset", [0, len(b"first\n")])
def test_readlines_nonpositive_or_none_hint_reads_remaining_lines(open_file, buffering, hint, offset):
    data = b"first\nsecond\nlast"
    expected = io.BytesIO(data)
    expected.seek(offset)
    with open_file(data, buffering) as stream:
        stream.seek(offset)
        assert stream.readlines(hint) == expected.readlines(hint)
        assert stream.tell() == expected.tell()
        assert stream.read() == b''


@pytest.mark.parametrize("buffering", [0, -1])
@pytest.mark.parametrize("hint", [-2, 0, None])
def test_readlines_nonpositive_or_none_hint_at_eof(open_file, buffering, hint):
    with open_file(b"", buffering) as stream:
        assert stream.readlines(hint) == []
        assert stream.tell() == 0


def test_unbuffered_readline_rejects_closed_stream(open_file):
    stream = open_file(b"first\nsecond\n")
    stream.close()
    with pytest.raises(ValueError):
        stream.readline()


def test_binary_line_boundaries_match_native_file(open_file):
    for value in range(256):
        data = b"prefix" + bytes([value]) + b"suffix\nlast"
        with open_file(data) as stream, open(stream.name, 'rb', buffering=0) as expected:
            assert list(stream) == list(expected), value
