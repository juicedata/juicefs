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
import uuid

import pytest

from juicefs.spec import JuiceFS


@pytest.fixture
def tree():
    fs = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                 no_usage_report=True, skip_instance_cache=True)
    root = "/fsspec-du-" + uuid.uuid4().hex
    fs.fs.makedirs(root + "/nested/deep", mode=0o777, exist_ok=True)
    fs.fs.mkdir(root + "/empty", 0o777)
    for name, data in [("direct", b"abc"), ("nested/second", b"12345"),
                       ("nested/deep/third", b"1234567")]:
        with fs.open(root + "/" + name, "wb") as stream:
            stream.write(data)
    try:
        yield fs, root
    finally:
        fs.rm(root, recursive=True)
        fs.fs.close(terminate=True)


def test_du_file_total(tree):
    fs, root = tree
    assert fs.du(root + "/direct") == 3


def test_du_recursive_total(tree):
    fs, root = tree
    assert fs.du(root) == 15


@pytest.mark.parametrize("depth,expected", [(1, 3), (2, 8)])
def test_du_maxdepth(tree, depth, expected):
    fs, root = tree
    assert fs.du(root, maxdepth=depth) == expected


@pytest.mark.parametrize("withdirs", [False, True])
def test_du_total_matches_breakdown(tree, withdirs):
    fs, root = tree
    sizes = fs.du(root, total=False, withdirs=withdirs)
    assert sizes[root + "/direct"] == 3
    assert sizes[root + "/nested/second"] == 5
    assert sizes[root + "/nested/deep/third"] == 7
    assert (root in sizes) is withdirs
    assert fs.du(root, withdirs=withdirs) == sum(sizes.values())


def test_du_empty_directory(tree):
    fs, root = tree
    assert fs.du(root + "/empty") == 0


def test_du_protocol_path(tree):
    fs, root = tree
    assert fs.du("jfs://" + root) == 15


def test_du_missing_path(tree):
    fs, root = tree
    with pytest.raises(FileNotFoundError):
        fs.du(root + "/missing")
