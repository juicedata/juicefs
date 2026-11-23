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

import gc
import os
import subprocess
import sys
import uuid
import weakref

import pytest
from fsspec.implementations.dirfs import DirFileSystem

from juicefs.spec import JuiceFS


@pytest.fixture
def tree(tmp_path):
    fs = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                 no_usage_report=True, skip_instance_cache=True,
                 temppath=str(tmp_path))
    root = "/fsspec-" + uuid.uuid4().hex
    fs.fs.mkdir(root, 0o777)
    try:
        yield fs, root
    finally:
        try:
            fs.fs.rmr(root)
        finally:
            fs.fs.close(terminate=True)


def test_registered_filesystem():
    # A fresh process must discover the installed entry point before spec imports.
    code = """
import os, uuid, fsspec
fs = fsspec.filesystem('jfs', name='test',
    meta=os.environ['JUICEFS_META'], no_usage_report=True, skip_instance_cache=True)
path = '/fsspec-entry-' + uuid.uuid4().hex
try:
    with fs.open(path, 'wb') as stream:
        stream.write(b'entrypoint')
    assert fs.cat_file(path) == b'entrypoint'
finally:
    if fs.exists(path):
        fs.fs.remove(path)
    fs.fs.close(terminate=True)
"""
    result = subprocess.run([sys.executable, "-B", "-c", code],
                            capture_output=True, text=True, timeout=30)
    assert result.returncode == 0, result.stdout + result.stderr


@pytest.mark.parametrize("create_parents", [False, True])
def test_default_directory_mode(tree, create_parents):
    fs, root = tree
    fs.mkdir(root + "/directory", create_parents=create_parents)
    assert fs.fs.stat(root + "/directory").st_mode & 0o777 == 0o777 & ~fs.fs.umask


@pytest.mark.parametrize("mode", ["ab", "xb"])
def test_auto_mkdir_write_modes(tree, mode):
    fs, root = tree
    fs.auto_mkdir = True
    with fs.open(root + "/nested/file", mode) as stream:
        stream.write(b"content")
    assert fs.cat_file(root + "/nested/file") == b"content"


def test_auto_mkdir_bare_filename(tree):
    fs, root = tree
    fs.auto_mkdir = True
    path = root.lstrip("/") + "-bare"
    try:
        with fs.open(path, "wb") as stream:
            stream.write(b"content")
        assert fs.cat_file(path) == b"content"
    finally:
        if fs.lexists(path):
            fs.fs.rmr(path)


def test_transaction_commit_and_discard(tree):
    fs, root = tree
    path = root + "/file"
    fs.pipe_file(path, b"old")
    with fs.transaction:
        with fs.open(path, "wb") as stream:
            stream.write(b"new")
        assert fs.cat_file(path) == b"old"
    assert fs.cat_file(path) == b"new"
    with pytest.raises(RuntimeError, match="abort"):
        with fs.transaction:
            with fs.open(path, "wb") as stream:
                stream.write(b"discard")
            raise RuntimeError("abort")
    assert fs.cat_file(path) == b"new"
    assert not os.listdir(fs.temppath)


def test_buffered_write_finalization(tree):
    fs, root = tree
    stream = fs.open(root + "/file", "wb", block_size=8192)
    stream.write(b"buffered bytes")
    reference = weakref.ref(stream)
    del stream
    gc.collect()
    assert reference() is None
    assert fs.cat_file(root + "/file") == b"buffered bytes"


def test_copy_replaces_destination_and_rejects_missing_source(tree):
    fs, root = tree
    fs.pipe_file(root + "/source", b"new")
    fs.pipe_file(root + "/target", b"old")
    fs.copy(root + "/source", root + "/target")
    assert fs.cat_file(root + "/target") == b"new"
    with pytest.raises(FileNotFoundError):
        fs.cp_file(root + "/missing", root + "/unexpected")
    assert not fs.exists(root + "/unexpected")


def test_copy_follows_file_symlink(tree):
    fs, root = tree
    fs.pipe_file(root + "/source", b"payload")
    fs.fs.symlink("source", root + "/alias")
    fs.copy(root + "/alias", root + "/target")
    fs.fs.remove(root + "/source")
    assert not fs.islink(root + "/target")
    assert fs.cat_file(root + "/target") == b"payload"


def test_listing_paths_and_file_metadata(tree):
    fs, root = tree
    fs.pipe_file(root + "/file", b"content")
    assert fs.ls("jfs://" + root, detail=False) == [root + "/file"]
    wrapped = DirFileSystem(path=root, fs=fs)
    assert wrapped.ls("", detail=False) == ["file"]
    info = wrapped.find("file", detail=True)["file"]
    assert info["name"] == "file" and info["size"] == 7


def test_listing_retains_dangling_and_directory_symlinks(tree):
    fs, root = tree
    fs.fs.symlink("missing", root + "/dangling")
    fs.fs.symlink(".", root + "/self")
    entries = {entry["name"]: entry for entry in fs.ls(root, detail=True)}
    for name, target in [("dangling", "missing"), ("self", ".")]:
        assert entries[root + "/" + name]["type"] == "link"
        assert entries[root + "/" + name]["destination"] == target
    assert fs.find(root, maxdepth=3) == [root + "/dangling", root + "/self"]
