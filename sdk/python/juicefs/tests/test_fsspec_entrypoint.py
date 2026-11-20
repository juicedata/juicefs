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

from importlib.metadata import entry_points
import subprocess
import sys

import fsspec

from juicefs.spec import JuiceFS


def test_installed_entry_point():
    entry = next(e for e in entry_points(group="fsspec.specs") if e.name == "jfs")
    assert entry.load() is JuiceFS
    assert fsspec.get_filesystem_class("jfs") is JuiceFS


def test_registered_filesystem_read_write():
    # Importing juicefs.spec registers the protocol, hiding a broken entry point.
    code = """
import os
import uuid
import fsspec

fs = fsspec.filesystem(
    "jfs", name="test", meta=os.getenv("JUICEFS_META", "redis://localhost"),
    no_usage_report=True, skip_instance_cache=True,
)
path = "/fsspec-entrypoint-" + uuid.uuid4().hex
try:
    with fs.open(path, "wb") as stream:
        stream.write(b"entrypoint")
    assert fs.cat_file(path) == b"entrypoint"
finally:
    if fs.exists(path):
        fs.rm(path)
    fs.fs.close(terminate=True)
"""
    result = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=30)
    assert result.returncode == 0, result.stdout + result.stderr
