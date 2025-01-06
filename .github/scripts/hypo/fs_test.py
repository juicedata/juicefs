import os
import unittest
from fs import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_issue_910(self):
        # See: https://github.com/juicedata/jfs/issues/910
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaaa', mode='wb', parent=v1, user='root')
        state.chmod(entry=v1, mode=32, user='root')
        state.listdir(dir=v1, user='root')
        state.change_groups(group='root', groups=['root'], user='user1')
        state.listdir(dir=v1, user='user1')
        state.teardown()

    def test_issue_914(self):
        # See: https://github.com/juicedata/jfs/issues/914
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'yl\xff{', file_name='tadj', mode='xb', parent=v1, user='root')
        state.fallocate(file=v2, length=22911, mode=0, offset=7849, user='root')
        state.copy_file(entry=v2, follow_symlinks=True, new_entry_name='npyn', parent=v1, user='root')
        state.teardown()

    def skip_test_issue_918(self):
        # See: https://github.com/juicedata/jfs/issues/918
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='lcka', mode='wb', parent=v1, user='root')
        v3 = state.clone_cp_file(entry=v2, new_entry_name='bbbb', parent=v1, preserve=True, user='root')
        state.chmod(entry=v3, mode=258, user='root')
        v5 = state.clone_cp_file(entry=v3, new_entry_name='mbbb', parent=v1, preserve=True, user='root')
        state.teardown()

    def test_x(self):
        # See: https://github.com/juicedata/jfs/issues/918
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='lcka', mode='wb', parent=v1, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()