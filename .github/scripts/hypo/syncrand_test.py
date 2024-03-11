import unittest
from syncrand import SyncMachine

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

    def test_sync10(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'\xdf"\x18\x11f\xbb\xef\xe3P', file_name='c', mode='a', parent=v1, umask=248)
        v3 = state.mkdir(mode=2647, parent=v1, subdir='*c', umask=93)
        v4 = state.mkdir(mode=2981, parent=v1, subdir='***', umask=98)
        v5 = state.create_file(content=b'\xff\x8f', file_name='b', mode='w', parent=v3, umask=63)
        state.create_file(content=b'=\xeb\xad\xd2\t\x7f6', file_name='?/?b', mode='a', parent=v3, umask=176)
        v6 = state.mkdir(mode=765, parent=v1, subdir='cc', umask=332)
        v7 = state.create_file(content=b'k+\x82', file_name='*', mode='a', parent=v4, umask=304)
        state.create_file(content=b'\x81\x0b', file_name='*b//', mode='w', parent=v1, umask=227)
        state.create_file(content=b'\xb2BUw', file_name='/ab', mode='x', parent=v1, umask=228)
        state.teardown()

if __name__ == '__main__':
    unittest.main()