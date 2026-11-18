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
