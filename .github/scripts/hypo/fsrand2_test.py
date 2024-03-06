import unittest
from fsrand2 import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_create_hardlink(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaab', parent=v1)
        v3 = state.hardlink(dest_file=v2, link_file_name='aaaa', parent=v1)
        state.rename_file(entry=v3, new_entry_name='aaab', parent=v1)
        state.teardown()
    def test2(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        # v2 = state.create_file(content=b'', file_name='haay', mode='w', parent=v1, rootdir1='/tmp/fsrand', rootdir2='/tmp/jfs/fsrand', umask=376, user='root')
        v3 = state.set_acl(default=True, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=True, recursive=True, rootdir1='/tmp/fsrand', rootdir2='/tmp/jfs/fsrand', set_mask=True, sudo_user='root', user='user1', user_perm={v1, 'r', 'w', 'x'})
        state.create_file(content=b'', file_name='afds', mode='w', parent=v1, rootdir1='/tmp/fsrand', rootdir2='/tmp/jfs/fsrand', umask=295, user='root')
        state.teardown()
if __name__ == '__main__':
    unittest.main()