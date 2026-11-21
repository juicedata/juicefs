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
from stat import S_ISLNK

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
                                           ("nested", ["nested/file"]),
                                           ("file", ["file"]),
                                           ("nested/file", ["nested/file"])],
                         ids=["root", "nested", "file", "nested-file"])
def test_dirfs_listing(tree, path, expected):
    fs, root = tree
    wrapped = DirFileSystem(path=root, fs=fs)
    assert sorted(wrapped.ls(path, detail=False)) == expected
    assert sorted(info["name"] for info in wrapped.ls(path, detail=True)) == expected


@pytest.mark.parametrize("path, size", [("file", 5), ("nested/file", 5)])
def test_find_file_keeps_metadata(tree, path, size):
    fs, root = tree
    info = fs.find(root + "/" + path, detail=True)[root + "/" + path]
    assert info["name"] == root + "/" + path
    assert info["type"] == "file" and info["size"] == size
    wrapped = DirFileSystem(path=root, fs=fs)
    info = wrapped.find(path, detail=True)[path]
    assert info["name"] == path
    assert info["type"] == "file" and info["size"] == size


@pytest.mark.parametrize("path", ["missing", "file/child"])
@pytest.mark.parametrize("detail", [False, True])
def test_listing_missing_path_does_not_return_an_entry(tree, path, detail):
    fs, root = tree
    with pytest.raises(OSError):
        fs.ls(root + "/" + path, detail=detail)


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


@pytest.mark.parametrize("protocol", ["", "jfs://", "juicefs://"],
                         ids=["plain", "jfs", "juicefs"])
@pytest.mark.parametrize("target", ["missing", "file/child"])
def test_dangling_link_does_not_hide_directory_contents(tree, protocol, target):
    fs, root = tree
    fs.fs.symlink(target, root + "/broken")
    entries = {info["name"]: info for info in fs.ls(protocol + root, detail=True)}
    assert set(entries) == {root + "/file", root + "/nested", root + "/broken"}
    link = entries[root + "/broken"]
    assert link["type"] == "link"
    assert S_ISLNK(link["mode"])
    assert link["destination"] == target
    assert fs.find(root) == [root + "/broken", root + "/file", root + "/nested/file"]
    assert fs.glob(root + "/*") == [root + "/broken", root + "/file", root + "/nested"]
    assert fs.cat(root + "/file") == b"outer"


@pytest.mark.parametrize("target, target_type", [("file", "file"),
                                                ("nested", "directory")])
def test_listing_preserves_link_metadata_without_following_target(tree, target, target_type):
    fs, root = tree
    path = root + "/link"
    fs.fs.symlink(target, path)
    entry = next(info for info in fs.ls(root, detail=True) if info["name"] == path)
    assert entry["type"] == "link"
    assert S_ISLNK(entry["mode"])
    assert entry["size"] == fs.fs.lstat(path).st_size
    assert entry["destination"] == target
    assert fs.info(path)["type"] == target_type


def test_walk_does_not_recurse_into_directory_symlink(tree):
    fs, root = tree
    fs.fs.symlink(".", root + "/self")
    # Bound both arms, so the old implementation cannot recurse indefinitely.
    listing = list(fs.walk(root, maxdepth=3))
    assert [path for path, _, _ in listing] == [root, root + "/nested"]
    assert listing[0][1] == ["nested"]
    assert sorted(listing[0][2]) == ["file", "self"]
    assert fs.find(root, maxdepth=3) == [root + "/file", root + "/nested/file", root + "/self"]


@pytest.mark.skipif(os.geteuid() == 0, reason="Root bypasses target directory permissions")
def test_unreadable_link_target_does_not_hide_directory_contents(tree):
    fs, root = tree
    private = root + "/private"
    fs.fs.mkdir(private, 0o700)
    fs.pipe_file(private + "/secret", b"secret")
    fs.fs.symlink("private/secret", root + "/restricted")
    fs.fs.chmod(private, 0)
    try:
        for detail in (False, True):
            with pytest.raises(OSError) as error:
                fs.ls(private, detail=detail)
            assert error.value.errno == errno.EACCES
        entries = {info["name"]: info for info in fs.ls(root, detail=True)}
        assert entries[root + "/restricted"]["destination"] == "private/secret"
        assert fs.cat(root + "/file") == b"outer"
        with pytest.raises(OSError) as error:
            fs.info(root + "/restricted")
        assert error.value.errno == errno.EACCES
    finally:
        fs.fs.chmod(private, 0o700)
