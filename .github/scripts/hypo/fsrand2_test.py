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

    def test_acl_913(self):
        # See: https://github.com/juicedata/jfs/issues/913
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, user='root')
        v3 = state.set_acl(default=False, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='user1', user_perm=set())
        state.chmod(entry=v1, mode=4, user='root')
        state.set_acl(default=False, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=True, set_mask=False, sudo_user='user1', user='root', user_perm=set())
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

    def test_acl_1004(self):
        # SEE https://github.com/juicedata/jfs/issues/1004
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.listdir(dir=v1, user='root')
        state.change_groups(group='root', groups=[], user='user1')
        v2 = state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='root', user_perm=set())
        state.listdir(dir=v1, user='user1')
        state.teardown()

    def test_acl_1006(self):
        # SEE https://github.com/juicedata/jfs/issues/1006
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, umask=0, user='root')
        state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=True, set_mask=False, sudo_user='root', user='root', user_perm=set())
        state.set_acl(default=False, entry=v1, group='user1', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='root', user_perm=set())
        state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=True, set_mask=False, sudo_user='user1', user='root', user_perm=set())
        state.teardown()

    def test_acl_1011(self):
        # SEE https://github.com/juicedata/jfs/issues/1011
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.chmod(entry=v1, mode=0, user='root')
        state.split_dir(dir=v1, vdirs=2)
        state.change_groups(group='root', groups=[], user='user1')
        v2 = state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='root', user_perm=set())
        v3 = state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, umask=0, user='root')
        state.listdir(dir=v1, user='user1')
        state.teardown()

    def test_acl_1022(self):
        # SEE https://github.com/juicedata/jfs/issues/1022
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.create_file(content=b'\xda\x07', file_name='lbca', mode='w', parent=v1, umask=103, user='root')
        state.set_acl(default=False, entry=v1, group='user1', group_perm={'r', 'w'}, logical=False, mask={'r', 'w', 'x'}, not_recalc_mask=True, other_perm=set(), physical=True, recalc_mask=True, recursive=True, set_mask=True, sudo_user='root', user='root', user_perm={'r', 'w', 'x'})
        state.chmod(entry=v1, mode=0o4004, user='root')
        state.set_acl(default=True, entry=v1, group='group4', group_perm={'x'}, logical=False, mask={'w', 'x'}, not_recalc_mask=False, other_perm=set(), physical=True, recalc_mask=False, recursive=True, set_mask=True, sudo_user='user1', user='user2', user_perm=set())
        state.teardown()

    def test_acl_1044(self):
        # SEE: https://github.com/juicedata/jfs/issues/1044
        state = JuicefsMachine()
        v1 = state.init_folders()
        v3 = state.create_file(content=b'', file_name='aaca', mode='w', parent=v1, umask=0, user='root')
        v4 = state.set_xattr(file=v3, flag=2, name='0', user='root', value=b"abc")
        v5 = state.set_acl(default=False, entry=v3, group='root', group_perm={'r'}, logical=False, mask={'r'}, not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='root', user_perm={'r'})
        state.remove_acl(entry=v3, option='--remove-all', user='root')
        state.list_xattr(file=v3, user='root')
        state.teardown()

    def skip_test_acl_4458(self):
        # SEE: https://github.com/juicedata/juicefs/issues/4458
        state = JuicefsMachine()
        v1 = state.init_folders()
        v3 = state.set_acl(default=True, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=True, recursive=True, set_mask=True, sudo_user='root', user='user1', user_perm={v1, 'r', 'w', 'x'})
        state.create_file(content=b'', file_name='afds', mode='w', parent=v1, umask=295, user='root')
        state.teardown()

    def test_acl_4472(self):
        # SEE: https://github.com/juicedata/juicefs/issues/4472
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='stsn', mode='x', parent=v1, umask=464, user='root')
        v3 = state.set_acl(default=True, entry=v1, group='group4', group_perm={'x'}, logical=False, mask={'w'}, not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=True, recursive=True, set_mask=True, sudo_user='root', user='root', user_perm={'r'})
        v8 = state.create_file(content=b'', file_name='qpyt', mode='w', parent=v1, umask=233, user='root')
        v9 = state.copy_file(entry=v2, follow_symlinks=False, new_entry_name='knmh', parent=v1, umask=23, user='root')
        state.open(file=v8, flags=[512], mode=2579, umask=34, user='root')
        state.teardown()

    def test_acl_4483(self):
        # SEE https://github.com/juicedata/juicefs/issues/4483
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.set_acl(default=True, entry=v1, group='root', group_perm={'r'}, logical=True, mask={'r'}, not_recalc_mask=False, other_perm={'r', 'x'}, physical=False, recalc_mask=False, recursive=True, set_mask=True, sudo_user='user1', user='user2', user_perm={'r', 'w', 'x'})
        v4 = state.create_file(content=b'\xe65', file_name='abha', mode='a', parent=v1, umask=3, user='root')
        v5 = state.set_acl(default=False, entry=v4, group='user3', group_perm={'x'}, logical=False, mask={'x'}, not_recalc_mask=True, other_perm=set(), physical=True, recalc_mask=True, recursive=False, set_mask=False, sudo_user='root', user='user1', user_perm=set())
        state.list_xattr(file=v4, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()