import pytest

from fsspec import filesystem
import fsspec.tests.abstract as abstract

from juicefs.spec import JuiceFS

class JuiceFSFixtures(abstract.AbstractFixtures):
    @pytest.fixture(scope="class")
    def fs(self):
        m = filesystem("jfs", auto_mkdir=True, name="test", meta="redis://localhost")
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
