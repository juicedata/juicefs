import unittest
from fsrand2 import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_acl_913(self):
        # See: https://github.com/juicedata/jfs/issues/913
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, user='root')
        v3 = state.set_acl(default=False, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='user1', user_perm=set())
        state.chmod(entry=v1, mode=4, user='root')
        state.set_acl(default=False, entry=v1, group='root', group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=True, set_mask=False, sudo_user='user1', user='root', user_perm=set())
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

    def test_acl_1015(self):
        # SEE: https://github.com/juicedata/jfs/issues/1015
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='aaaa', mode='w', parent=v1, umask=0, user='root')
        state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm={'r', 'w', 'x'}, physical=False, recalc_mask=False, recursive=True, set_mask=True, sudo_user='root', user='user1', user_perm={'r', 'w', 'x'})
        state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=True, set_mask=False, sudo_user='user1', user='root', user_perm=set())
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

    def test_acl_4458(self):
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

    def test_acl_4496(self):
        # SEE https://github.com/juicedata/juicefs/issues/4496
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.chmod(entry=v1, mode=3291, user='root')
        state.remove_acl(entry=v1, option='--remove-default', user='user1')
        v40 = state.mkdir(mode=1122, parent=v1, subdir='uopt', umask=367, user='root')
        state.chown(entry=v40, owner='user1', user='root')
        state.change_groups(group='group4', groups=['group2'], user='user1')
        state.set_acl(default=False, entry=v40, group='group2', group_perm={'r', 'w', 'x'}, logical=True, mask={'r', 'w', 'x'}, not_recalc_mask=True, other_perm={'x'}, physical=False, recalc_mask=True, recursive=False, set_mask=False, sudo_user='user1', user=v1, user_perm=set())
        state.teardown()

    def test_acl_4663(self):
        #SEE https://github.com/juicedata/juicefs/issues/4663
        state = JuicefsMachine()
        v1 = state.init_folders()
        v3 = state.set_acl(default=True, entry=v1, group=v1, group_perm=set(), logical=False, mask=set(), not_recalc_mask=False, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user=v1, user_perm={'r'})
        state.mkdir(mode=0, parent=v1, subdir='aaaa', umask=0, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()