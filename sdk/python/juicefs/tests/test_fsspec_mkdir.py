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
