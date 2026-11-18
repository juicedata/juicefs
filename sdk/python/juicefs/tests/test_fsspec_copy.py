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
                 auto_mkdir=True, no_usage_report=True, skip_instance_cache=True)
    root = "/fsspec-copy-" + uuid.uuid4().hex
    fs.fs.makedirs(root + "/src", mode=0o777, exist_ok=True)
    fs.pipe(root + "/src/one", b"new payload")
    fs.pipe(root + "/src/two", b"second payload")
    fs.pipe(root + "/existing", b"old payload")
    try:
        yield fs, root
    finally:
        if fs.exists(root):
            fs.fs.rmr(root)
        fs.fs.close(terminate=True)


def test_copy_replaces_existing_destination(tree):
    fs, root = tree
    fs.copy(root + "/src/one", root + "/existing")
    assert fs.cat(root + "/existing") == b"new payload"
    assert fs.cat(root + "/src/one") == b"new payload"


def test_cp_file_replaces_existing_destination(tree):
    fs, root = tree
    fs.cp_file(root + "/src/one", root + "/existing")
    assert fs.cat(root + "/existing") == b"new payload"


def test_cp_file_missing_source_does_not_create_destination(tree):
    fs, root = tree
    with pytest.raises(FileNotFoundError):
        fs.cp_file(root + "/absent", root + "/unexpected")
    assert not fs.exists(root + "/unexpected")


def test_copy_list_missing_source_raises(tree):
    fs, root = tree
    with pytest.raises(FileNotFoundError):
        fs.copy([root + "/absent"], [root + "/unexpected"], on_error="raise")
    assert not fs.exists(root + "/unexpected")


def test_copy_list_missing_source_ignored(tree):
    fs, root = tree
    fs.copy([root + "/absent"], [root + "/unexpected"], on_error="ignore")
    assert not fs.exists(root + "/unexpected")


def test_copy_file_new_destination(tree):
    fs, root = tree
    fs.copy(root + "/src/one", root + "/fresh")
    assert fs.cat(root + "/fresh") == b"new payload"


def test_copy_list_valid_sources(tree):
    fs, root = tree
    fs.copy([root + "/src/one", root + "/src/two"],
            [root + "/out-one", root + "/out-two"])
    assert fs.cat(root + "/out-one") == b"new payload"
    assert fs.cat(root + "/out-two") == b"second payload"


def test_cp_directory_creates_empty_directory(tree):
    fs, root = tree
    fs.cp_file(root + "/src", root + "/new-directory")
    assert fs.isdir(root + "/new-directory")
    assert fs.ls(root + "/new-directory", detail=False) == []


def test_copy_missing_scalar_source(tree):
    fs, root = tree
    with pytest.raises(FileNotFoundError):
        fs.copy(root + "/absent", root + "/unexpected")
    assert not fs.exists(root + "/unexpected")


def test_copy_protocol_paths_with_ignored_missing_source(tree):
    fs, root = tree
    fs.copy(["jfs://" + root + "/src/one", "jfs://" + root + "/absent"],
            ["jfs://" + root + "/out-one", "jfs://" + root + "/unexpected"],
            on_error="ignore")
    assert fs.cat(root + "/out-one") == b"new payload"
    assert not fs.exists(root + "/unexpected")


def test_copy_missing_source_preserves_existing_destination(tree):
    fs, root = tree
    fs.copy([root + "/absent"], [root + "/existing"], on_error="ignore")
    assert fs.cat(root + "/existing") == b"old payload"


def test_copy_clone_failure_preserves_destination(tree, monkeypatch):
    fs, root = tree
    clone = fs.fs.clone

    def fail_after_clone(source, destination):
        clone(source, destination)
        raise OSError(errno.EIO, "injected clone failure")

    monkeypatch.setattr(fs.fs, "clone", fail_after_clone)
    with pytest.raises(OSError) as error:
        fs.cp_file(root + "/src/one", root + "/existing")
    assert error.value.errno == errno.EIO
    assert fs.cat(root + "/existing") == b"old payload"
    assert not any(name.startswith(".jfs-copy-") for name in fs.fs.listdir(root))


def test_copy_rename_failure_preserves_destination(tree, monkeypatch):
    fs, root = tree

    def fail_rename(source, destination):
        raise OSError(errno.EIO, "injected rename failure")

    monkeypatch.setattr(fs.fs, "rename", fail_rename)
    with pytest.raises(OSError) as error:
        fs.cp_file(root + "/src/one", root + "/existing")
    assert error.value.errno == errno.EIO
    assert fs.cat(root + "/existing") == b"old payload"
    assert not any(name.startswith(".jfs-copy-") for name in fs.fs.listdir(root))


def test_copy_does_not_replace_destination_directory(tree):
    fs, root = tree
    with pytest.raises(OSError):
        fs.cp_file(root + "/src/one", root + "/src")
    assert fs.isdir(root + "/src")
    assert fs.cat(root + "/src/two") == b"second payload"
    assert not any(name.startswith(".jfs-copy-") for name in fs.fs.listdir(root))
