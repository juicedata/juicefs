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

import os

import pytest

from juicefs.spec import JuiceFS


@pytest.fixture(scope="module")
def fs():
    filesystem = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                         no_usage_report=True)
    yield filesystem
    filesystem.fs.close(terminate=True)


@pytest.fixture
def fs_path(fs, tmp_path):
    path = str(tmp_path)
    yield path
    fs.rm(path, recursive=True)


@pytest.mark.parametrize("create_parents", [False, True])
@pytest.mark.parametrize("umask", [0o022, 0o077])
def test_default_mode(fs, fs_path, create_parents, umask):
    fs.makedirs(fs_path, exist_ok=True)
    path = f"{fs_path}/directory"
    previous_umask = fs.fs.umask
    try:
        fs.fs.umask = umask
        fs.mkdir(path, create_parents=create_parents)
    finally:
        fs.fs.umask = previous_umask
    with fs.open(f"{path}/file", "wb") as stream:
        stream.write(b"content")
    assert fs.cat_file(f"{path}/file") == b"content"
    assert fs.fs.stat(path).st_mode & 0o777 == 0o777 & ~umask


@pytest.mark.parametrize("create_parents", [False, True])
@pytest.mark.parametrize("mode", [0o700, 0o750])
def test_explicit_mode(fs, fs_path, create_parents, mode):
    fs.makedirs(fs_path, exist_ok=True)
    path = f"{fs_path}/directory"
    fs.mkdir(path, create_parents=create_parents, mode=mode)
    assert fs.fs.stat(path).st_mode & 0o777 == mode & ~fs.fs.umask


@pytest.mark.parametrize("mode", ["wb", "ab", "xb"])
@pytest.mark.parametrize("auto_mkdir", [False, True])
def test_auto_mkdir_write_modes(fs, fs_path, mode, auto_mkdir):
    fs.makedirs(fs_path, exist_ok=True)
    path = f"{fs_path}/nested/file"
    previous = fs.auto_mkdir
    fs.auto_mkdir = auto_mkdir
    try:
        if auto_mkdir:
            with fs.open(path, mode) as stream:
                stream.write(b"content")
            assert fs.cat_file(path) == b"content"
        else:
            with pytest.raises(OSError):
                fs.open(path, mode)
            assert not fs.exists(f"{fs_path}/nested")
    finally:
        fs.auto_mkdir = previous


def test_auto_mkdir_does_not_create_read_paths(fs, fs_path):
    fs.makedirs(fs_path, exist_ok=True)
    previous = fs.auto_mkdir
    fs.auto_mkdir = True
    try:
        with pytest.raises(OSError):
            fs.open(f"{fs_path}/nested/file", "rb")
        assert not fs.exists(f"{fs_path}/nested")
    finally:
        fs.auto_mkdir = previous


def test_auto_mkdir_append_existing(fs, fs_path):
    fs.makedirs(fs_path, exist_ok=True)
    path = f"{fs_path}/file"
    fs.pipe_file(path, b"original")
    previous = fs.auto_mkdir
    fs.auto_mkdir = True
    try:
        with fs.open(path, "ab") as stream:
            stream.write(b"-appended")
        assert fs.cat_file(path) == b"original-appended"
    finally:
        fs.auto_mkdir = previous


def test_auto_mkdir_exclusive_preserves_existing(fs, fs_path):
    fs.makedirs(fs_path, exist_ok=True)
    path = f"{fs_path}/file"
    fs.pipe_file(path, b"original")
    previous = fs.auto_mkdir
    fs.auto_mkdir = True
    try:
        with pytest.raises(OSError):
            fs.open(path, "xb")
        assert fs.cat_file(path) == b"original"
    finally:
        fs.auto_mkdir = previous


@pytest.mark.parametrize("protocol", ["", "jfs://", "juicefs://"],
                         ids=["plain", "jfs", "juicefs"])
@pytest.mark.parametrize("mode", ["wb", "ab", "xb"])
def test_auto_mkdir_bare_filename(fs, fs_path, protocol, mode):
    fs.makedirs(fs_path, exist_ok=True)
    path = fs_path.rsplit("/", 1)[-1] + "-bare"
    previous = fs.auto_mkdir
    fs.auto_mkdir = True
    try:
        with fs.open(protocol + path, mode) as stream:
            stream.write(b"content")
        assert fs.isfile(path)
        assert fs.cat_file(path) == b"content"
    finally:
        fs.auto_mkdir = previous
        if fs.lexists(path):
            fs.rm(path, recursive=True)
