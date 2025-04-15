import unittest
from file import JuicefsDataMachine

class TestPySdk(unittest.TestCase):
    def test_issue_1522_1(self):
        # SEE https://github.com/juicedata/jfs/issues/1522
        state = JuicefsDataMachine()
        v1 = state.init_folders()
        state.write(fd=v1, content='abc')
        state.seek(fd=v1, offset=0, whence=1)
        state.teardown()

    def test_issue_1522_2(self):
        # SEE https://github.com/juicedata/jfs/issues/1522
        state = JuicefsDataMachine()
        v1 = state.init_folders()
        state.seek(fd=v1, offset=1, whence=0)
        state.write(fd=v1, content='')
        state.seek(fd=v1, offset=0, whence=2)
        state.teardown()

    def test_issue_1523(self):
        # SEE https://github.com/juicedata/jfs/issues/1523
        state = JuicefsDataMachine()
        v1 = state.init_folders()
        state.truncate(fd=v1, size=1)
        state.readline(fd=v1)
        state.teardown()

    def skip_test_issue_1533(self):
        # SEE https://github.com/juicedata/jfs/issues/1533
        state = JuicefsDataMachine()
        v1 = state.init_folders()
        state.write(fd=v1, content='ab')
        state.seek(fd=v1, offset=0, whence=0)
        state.read(fd=v1, length=1)
        state.write(content='', fd=v1)
        state.read(fd=v1, length=1)
        state.teardown()

    def skip_test_issue_1548(self):
        # SEE https://github.com/juicedata/jfs/issues/1548
        state = JuicefsDataMachine()
        fd_0 = state.init_folders()
        state.write(fd=fd_0, content='a')
        state.seek(fd=fd_0, offset=0, whence=0)
        state.write(fd=fd_0, content='b')
        state.read(fd=fd_0, length=1)
        state.teardown()

    def skip_test_issue_1548_2(self):
        # SEE https://github.com/juicedata/jfs/issues/1548
        state = JuicefsDataMachine()
        fd_0 = state.init_folders()
        state.truncate(fd=fd_0, size=3)
        state.write(content='a', fd=fd_0)
        state.readline(fd=fd_0)
        state.teardown()

if __name__ == '__main__':
    unittest.main()


