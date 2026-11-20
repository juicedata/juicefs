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

import errno
import os
import uuid

import pytest

from juicefs.spec import JuiceFS


@pytest.fixture
def tree():
    fs = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                 no_usage_report=True, skip_instance_cache=True)
    root = "/fsspec-rm-depth-" + uuid.uuid4().hex
    fs.fs.makedirs(root + "/nested/deep", mode=0o777, exist_ok=True)
    for name, data in [("direct", b"abc"), ("nested/second", b"12345"),
                       ("nested/deep/third", b"must survive bounded deletion")]:
        with fs.open(root + "/" + name, "wb") as stream:
            stream.write(data)
    try:
        yield fs, root
    finally:
        if fs.exists(root):
            fs.fs.rmr(root)
        fs.fs.close(terminate=True)


@pytest.mark.parametrize("depth", [1, 2])
def test_rm_maxdepth_preserves_deeper_files(tree, depth):
    fs, root = tree
    error = None
    try:
        fs.rm(root, recursive=True, maxdepth=depth)
    except OSError as exc:
        error = exc
    assert fs.exists(root + "/nested/deep/third")
    with fs.open(root + "/nested/deep/third", "rb") as stream:
        assert stream.read() == b"must survive bounded deletion"
    assert error is not None and error.errno == errno.ENOTEMPTY


@pytest.mark.parametrize("depth", [0, -1])
def test_rm_invalid_maxdepth(tree, depth):
    fs, root = tree
    with pytest.raises(ValueError, match="maxdepth must be at least 1"):
        fs.rm(root, recursive=True, maxdepth=depth)
    assert fs.exists(root + "/nested/deep/third")


def test_rm_unlimited(tree):
    fs, root = tree
    fs.rm(root, recursive=True)
    assert not fs.exists(root)


def test_rm_sufficient_maxdepth(tree):
    fs, root = tree
    fs.rm(root, recursive=True, maxdepth=3)
    assert not fs.exists(root)


def test_rm_empty_directory(tree):
    fs, root = tree
    empty = root + "/empty"
    fs.fs.mkdir(empty, 0o777)
    fs.rm(empty, recursive=True, maxdepth=1)
    assert not fs.exists(empty)


def test_rm_path_list(tree):
    fs, root = tree
    paths = [root + "/direct", "jfs://" + root + "/nested/second"]
    fs.rm(paths, recursive=True, maxdepth=1)
    assert not fs.exists(root + "/direct")
    assert not fs.exists(root + "/nested/second")
    assert fs.exists(root + "/nested/deep/third")


def test_rm_file(tree):
    fs, root = tree
    fs.rm(root + "/direct", recursive=True, maxdepth=1)
    assert not fs.exists(root + "/direct")
    assert fs.exists(root + "/nested/second")


def test_rm_nonrecursive_directory(tree):
    fs, root = tree
    with pytest.raises(OSError):
        fs.rm(root)
    assert fs.exists(root + "/nested/deep/third")


def test_rm_directory_symlink_does_not_touch_target(tree):
    fs, root = tree
    isolated = root + "/links"
    fs.fs.mkdir(isolated, 0o777)
    fs.symlink(root + "/nested", isolated + "/link")
    fs.rm(isolated, recursive=True, maxdepth=2)
    assert not fs.lexists(isolated)
    with fs.open(root + "/nested/deep/third", "rb") as stream:
        assert stream.read() == b"must survive bounded deletion"
