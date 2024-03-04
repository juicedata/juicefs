import unittest
from syncrand import SyncMachine

class TestFsrand2(unittest.TestCase):
    def test_create_hardlink(self):
        state = SyncMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', mode='w', parent=v1, umask=0)
        state.sync(options=[{'option': '--exclude', 'pattern': '**/***'}])
        state.teardown()

if __name__ == '__main__':
    unittest.main()