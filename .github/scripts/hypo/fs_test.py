import unittest
from fsrand2 import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_hardlink_795(self):
        # reproduce https://github.com/juicedata/jfs/issues/795
        for i in range(10):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.create_file(content=b'', file_name='aaac', parent=v1, user='root')
            state.rebalance_dir(dir=v1, is_vdir=False, zone1=v1, zone2='.jfszone0')
            state.hardlink(dest_file=v2, link_file_name='aaaa', parent=v1, user='root')
            state.teardown()

    def test_hardlink_769(self):
        # reproduce nlink issue: https://github.com/juicedata/jfs/issues/769
        for i in range(10):
            state = JuicefsMachine()
            v1 = state.init_folders()
            state.split_dir(dir=v1, vdirs=2)
            v2 = state.create_file(content='a', file_name='aaab', parent=v1, user='root')
            state.rebalance_file(file=v2, zone1=v1, zone2='.jfszone0')
            state.hardlink(dest_file=v2, link_file_name='aaaa', parent=v1, user='root')
            state.teardown()

    def test_listdir_910(self):
        # See: https://github.com/juicedata/jfs/issues/910
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, user='root')
        state.chmod(entry=v1, mode=32, user='root')
        state.listdir(dir=v1, user='root')
        state.change_groups(group='root', groups=['root'], user='user1')
        state.listdir(dir=v1, user='user1')
        state.teardown()

    def test_fallocate_914(self):
        # See: https://github.com/juicedata/jfs/issues/914
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'yl\xff{', file_name='tadj', mode='x', parent=v1, user='root')
        state.fallocate(file=v2, length=22911, mode=0, offset=7849, user='root')
        state.copy_file(entry=v2, follow_symlinks=True, new_entry_name='npyn', parent=v1, user='root')
        state.teardown()

    def test_clone_918(self):
        # See: https://github.com/juicedata/jfs/issues/918
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='lcka', mode='w', parent=v1, user='root')
        v3 = state.clone_cp_file(entry=v2, new_entry_name='bbbb', parent=v1, preserve=True, user='root')
        state.chmod(entry=v3, mode=258, user='root')
        v5 = state.clone_cp_file(entry=v3, new_entry_name='mbbb', parent=v1, preserve=True, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()