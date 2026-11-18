import pytest

from fsspec import filesystem
import fsspec.tests.abstract as abstract

from juicefs.spec import JuiceFS
import os

class JuiceFSFixtures(abstract.AbstractFixtures):
    @pytest.fixture(scope="class")
    def fs(self):
        meta = os.getenv("JUICEFS_META", "redis://localhost")
        m = filesystem("jfs", auto_mkdir=True, name="test", meta=meta)
        return m

    @pytest.fixture
    def fs_path(self, tmpdir):
        return str(tmpdir)


class TestJuiceFSGet(abstract.AbstractGetTests, JuiceFSFixtures):
    pass


class TestJuiceFSPut(abstract.AbstractPutTests, JuiceFSFixtures):
    pass


class TestJuiceFSCopy(abstract.AbstractCopyTests, JuiceFSFixtures):
    pass


class TestJuiceFSMkdir(JuiceFSFixtures):
    @pytest.mark.parametrize("create_parents", [False, True])
    @pytest.mark.parametrize("umask", [0o022, 0o077])
    def test_default_mode(self, fs, fs_path, create_parents, umask):
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
    def test_explicit_mode(self, fs, fs_path, create_parents, mode):
        fs.makedirs(fs_path, exist_ok=True)
        path = f"{fs_path}/directory"
        fs.mkdir(path, create_parents=create_parents, mode=mode)
        assert fs.fs.stat(path).st_mode & 0o777 == mode & ~fs.fs.umask
