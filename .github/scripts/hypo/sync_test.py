import unittest
from sync import SyncMachine

class TestFsrand2(unittest.TestCase):

    def test_sync1(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.mkdir(mode=0, parent=v1, subdir='a', umask=0)
        v3 = state.create_file(content=b'', file_name=v2, mode='w', parent=v2, umask=0)
        state.sync(options=[{'option': '--include', 'pattern': 'aa/***'},
        {'option': '--exclude', 'pattern': 'a?**'}])
        state.teardown()

    def test_sync2(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': '**/***'}])
        state.teardown()

    def test_sync3(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': '/***'}])
        state.teardown()

    def test_sync4(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': '*/***'}])
        state.teardown()

    def test_sync5(self):
        state = SyncMachine()
        v1 = state.init_folders()
        state.sync(options=[{'option': '--include', 'pattern': 'a'}])
        v2 = state.mkdir(mode=0, parent=v1, subdir='a', umask=0)
        v3 = state.create_file(content=b'', file_name=v2, mode='w', parent=v2, umask=0)
        state.sync(options=[{'option': '--include', 'pattern': 'aa'},
        {'option': '--exclude', 'pattern': 'a?**'}])
        state.teardown()

    def test_sync6(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': '**a'}])
        state.teardown()

    def test_sync7(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aa', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': 'aa**a'}])
        state.teardown()
    
    def test_sync8(self):
        # SEE: https://github.com/juicedata/juicefs/issues/4471
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.mkdir(mode=8, parent=v1, subdir='a', umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': 'a/**/a'}])
        state.teardown()

    def test_sync9(self):
        # SEE: https://github.com/juicedata/juicefs/issues/4471
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.mkdir(mode=8, parent=v1, subdir='aa', umask=0) 
        v3 = state.create_file(content=b'', file_name='a', mode='w', parent=v2, umask=0)
        state.sync(options=[{'option': '--include', 'pattern': '**aa**'},
        {'option': '--exclude', 'pattern': 'a'}])
        state.teardown()

if __name__ == '__main__':
    unittest.main()