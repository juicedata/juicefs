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
from fsspec.implementations.dirfs import DirFileSystem

from juicefs.spec import JuiceFS


@pytest.fixture
def tree():
    fs = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                 auto_mkdir=True, no_usage_report=True, skip_instance_cache=True)
    root = "/fsspec-listing-" + uuid.uuid4().hex
    fs.fs.makedirs(root + "/nested", mode=0o777, exist_ok=True)
    fs.pipe_file(root + "/file", b"outer")
    fs.pipe_file(root + "/nested/file", b"inner")
    try:
        yield fs, root
    finally:
        if fs.exists(root):
            fs.fs.rmr(root)
        fs.fs.close(terminate=True)


@pytest.mark.parametrize("protocol", ["", "jfs://", "juicefs://"],
                         ids=["plain", "jfs", "juicefs"])
@pytest.mark.parametrize("suffix", ["", "/"], ids=["no_slash", "trailing_slash"])
def test_listing_returns_full_paths_without_protocol(tree, protocol, suffix):
    fs, root = tree
    path = protocol + root + suffix
    names = sorted(fs.ls(path, detail=False))
    assert names == [root + "/file", root + "/nested"]
    assert sorted(info["name"] for info in fs.ls(path, detail=True)) == names


def test_default_listing_returns_full_paths(tree):
    fs, root = tree
    assert sorted(fs.ls(root)) == [root + "/file", root + "/nested"]


def test_root_listing_returns_absolute_paths(tree):
    fs, root = tree
    names = fs.ls("/", detail=False)
    assert root in names
    assert all(name.startswith("/") for name in names)


@pytest.mark.parametrize("path, expected", [("", ["file", "nested"]),
                                           ("nested", ["nested/file"])], ids=["root", "nested"])
def test_dirfs_listing(tree, path, expected):
    fs, root = tree
    wrapped = DirFileSystem(path=root, fs=fs)
    assert sorted(wrapped.ls(path, detail=False)) == expected
    assert sorted(info["name"] for info in wrapped.ls(path, detail=True)) == expected


def test_empty_directory_listing(tree):
    fs, root = tree
    fs.fs.mkdir(root + "/empty", 0o777)
    assert fs.ls(root + "/empty", detail=False) == []
    assert fs.ls(root + "/empty", detail=True) == []


def test_native_listdir_keeps_basenames(tree):
    fs, root = tree
    assert sorted(fs.fs.listdir(root)) == ["file", "nested"]


def test_listing_keeps_file_contents(tree):
    fs, root = tree
    fs.ls(root, detail=False)
    assert fs.cat(root + "/file") == b"outer"
    assert fs.cat(root + "/nested/file") == b"inner"


def test_detailed_listing_keeps_metadata(tree):
    fs, root = tree
    entries = {info["name"]: info for info in fs.ls(root, detail=True)}
    assert set(entries) == {root + "/file", root + "/nested"}
    assert entries[root + "/file"]["type"] == "file"
    assert entries[root + "/file"]["size"] == 5
    assert entries[root + "/nested"]["type"] == "directory"
