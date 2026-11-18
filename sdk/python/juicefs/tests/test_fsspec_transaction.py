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
from pathlib import Path
import uuid

import pytest

from juicefs.spec import JuiceFS


@pytest.fixture(params=["default", "custom"])
def filesystem(request, tmp_path):
    options = {}
    if request.param == "custom":
        staging = tmp_path / "staging"
        staging.mkdir()
        options["temppath"] = str(staging)
    fs = JuiceFS("test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
                 no_usage_report=True, skip_instance_cache=True, **options)
    root = "/fsspec-transaction-" + uuid.uuid4().hex
    fs.fs.makedirs(root, mode=0o777, exist_ok=True)
    streams = []
    try:
        yield fs, root, streams
    finally:
        for stream in streams:
            stream.close()
            temporary = Path(stream.temp)
            if temporary.exists():
                assert temporary.is_file() and not temporary.is_symlink()
                temporary.unlink()
        fs.rm(root, recursive=True)
        fs.fs.close(terminate=True)


def test_direct_write(filesystem):
    fs, root, _ = filesystem
    path = root + "/ordinary"
    with fs.open(path, "wb") as stream:
        stream.write(b"ordinary")
    assert fs.cat_file(path) == b"ordinary"


def test_transaction_commit(filesystem):
    fs, root, streams = filesystem
    path = root + "/committed"
    error = None
    try:
        with fs.transaction:
            with fs.open(path, "wb") as stream:
                streams.append(stream)
                stream.write(b"committed")
            assert not fs.exists(path)
    except OSError as caught:
        error = caught
    assert error is None, f"Transaction commit failed: {error}"
    assert fs.cat_file(path) == b"committed"
    assert not Path(stream.temp).exists()


def test_transaction_abort(filesystem):
    fs, root, streams = filesystem
    path = root + "/aborted"

    class Abort(Exception):
        pass

    error = None
    try:
        with fs.transaction:
            with fs.open(path, "wb") as stream:
                streams.append(stream)
                stream.write(b"aborted")
            raise Abort("caller requested rollback")
    except Exception as caught:
        error = caught
    assert isinstance(error, Abort), f"Rollback masked caller error: {error}"
    assert not fs.exists(path)
    assert not Path(stream.temp).exists()


def test_transaction_closes_open_stream(filesystem):
    fs, root, streams = filesystem
    path = root + "/open-stream"
    with fs.transaction:
        stream = fs.open(path, "wb")
        streams.append(stream)
        stream.write(b"buffered until commit")
    assert stream.closed
    assert fs.cat_file(path) == b"buffered until commit"
    assert not Path(stream.temp).exists()


def test_transaction_replaces_existing_file_only_at_commit(filesystem):
    fs, root, streams = filesystem
    path = root + "/replacement"
    with fs.open(path, "wb") as stream:
        stream.write(b"old contents")
    with fs.transaction:
        with fs.open(path, "wb") as stream:
            streams.append(stream)
            stream.write(b"new contents")
        assert fs.cat_file(path) == b"old contents"
    assert fs.cat_file(path) == b"new contents"


def test_transaction_upload_does_not_rejoin_transaction(filesystem):
    fs, root, streams = filesystem
    with fs.transaction as transaction:
        for name in ["first", "second"]:
            with fs.open(root + "/" + name, "wb") as stream:
                streams.append(stream)
                stream.write(name.encode())
        assert len(transaction.files) == 2
        assert not fs.exists(root + "/first")
        assert not fs.exists(root + "/second")
    assert len(transaction.files) == 0
    assert fs.cat_file(root + "/first") == b"first"
    assert fs.cat_file(root + "/second") == b"second"
    assert set(fs.fs.listdir(root)) == {"first", "second"}


def test_failed_commit_cleans_remote_temporary_file(filesystem):
    fs, root, streams = filesystem
    path = root + "/directory"
    fs.fs.mkdir(path, 0o777)
    # Call commit directly: Transaction.complete may discard after a failure.
    stream = fs.open(path, "wb", autocommit=False)
    streams.append(stream)
    stream.write(b"recoverable staged contents")
    with pytest.raises(OSError) as error:
        stream.commit()
    assert error.value.errno == errno.EISDIR
    assert fs.isdir(path)
    assert set(fs.fs.listdir(root)) == {"directory"}
    assert Path(stream.temp).read_bytes() == b"recoverable staged contents"
    stream.discard()
    assert not Path(stream.temp).exists()


def test_autocommit_guards_are_preserved(filesystem):
    fs, root, _ = filesystem
    with fs.open(root + "/direct", "wb") as stream:
        stream.write(b"direct")
        with pytest.raises(RuntimeError):
            stream.commit()
        with pytest.raises(RuntimeError):
            stream.discard()
    assert fs.cat_file(root + "/direct") == b"direct"
