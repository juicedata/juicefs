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

    def test_abc(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'\x11', file_name='pssw', mode='x', parent=v1, umask=208, user='root')
        state.open(file=v2, flags=[64, 1052672, 0, 2], mode=778, umask=503, user='root')
        v5 = state.hardlink(dest_file=v2, link_file_name='qtgl', parent=v1, umask=360, user='root')
        v6 = state.hardlink(dest_file=v2, link_file_name='uixm', parent=v1, umask=430, user='root')
        state.open(file=v2, flags=[1, 1052672, 512], mode=2570, umask=488, user='root')
        v10 = state.create_file(content=b'rH\xb4\xe5', file_name='xptk', mode='x', parent=v1, umask=258, user='root')
        state.open(file=v2, flags=[0, 1, 2, 512, 64], mode=724, umask=241, user='root')
        v11 = state.create_file(content=b'\x19C', file_name='nnxx', mode='x', parent=v1, umask=54, user='root')
        v17 = state.hardlink(dest_file=v6, link_file_name='fifn', parent=v1, umask=110, user='root')
        v19 = state.create_file(content=b'x-a\xfc', file_name='aekf', mode='w', parent=v1, umask=145, user='root')
        state.open(file=v6, flags=[4096, 0, 1052672, 1024, 2, 128, 512, 64, 1], mode=161, umask=26, user='root')
        v22 = state.rename_file(entry=v5, new_entry_name='brct', parent=v1, umask=507, user='root')
        state.open(file=v6, flags=[0, 512, 64, 1052672, 2, 4096, 1, 1024], mode=1617, umask=340, user='root')
        v23 = state.hardlink(dest_file=v6, link_file_name='fpkc', parent=v1, umask=60, user='root')
        state.open(file=v10, flags=[1052672, 0, 2], mode=3556, umask=177, user='root')
        state.open(file=v11, flags=[1052672, 1, 64, 1024, 4096, 128, 2, 0, 512], mode=4094, umask=381, user='root')
        state.open(file=v22, flags=[64, 1052672, 128], mode=3825, umask=412, user='root')
        v26 = state.create_file(content=b'\xa5\x9e', file_name='vpbg', mode='a', parent=v1, umask=214, user='root')
        v30 = state.hardlink(dest_file=v10, link_file_name='bvfd', parent=v1, umask=391, user='root')
        state.open(file=v17, flags=[2, 128, 1052672, 0, 4096, 512, 64, 1024, 1], mode=1940, umask=370, user='root')
        state.open(file=v22, flags=[128, 1, 0, 512, 1052672, 64, 1024, 4096, 2], mode=679, umask=0, user='root')
        v32 = state.create_file(content=b'', file_name='vrim', mode='a', parent=v1, umask=444, user='root')
        v36 = state.create_file(content=b'', file_name='mexk', mode='x', parent=v1, umask=438, user='root')
        v37 = state.create_file(content=b'|\xc9nN\xb0\x16', file_name='pfjn', mode='a', parent=v1, umask=424, user='root')
        v39 = state.create_file(content=b'', file_name='ebtc', mode='x', parent=v1, umask=111, user='root')
        v41 = state.hardlink(dest_file=v39, link_file_name='rxlx', parent=v1, umask=112, user='root')
        v42 = state.rename_file(entry=v39, new_entry_name='scsg', parent=v1, umask=192, user='root')
        v45 = state.create_file(content=b'\t\xc3\x01W^&\x00n', file_name='kpyr', mode='x', parent=v1, umask=394, user='root')
        v47 = state.hardlink(dest_file=v42, link_file_name='myqu', parent=v1, umask=67, user='root')
        state.open(file=v17, flags=[0, 1, 128, 4096], mode=3049, umask=285, user='root')
        state.open(file=v36, flags=[1052672, 2, 64, 1, 1024], mode=1225, umask=100, user='root')
        v54 = state.rename_file(entry=v30, new_entry_name='frbf', parent=v1, umask=133, user='root')
        v57 = state.hardlink(dest_file=v54, link_file_name='aibl', parent=v1, umask=256, user='root')
        state.open(file=v10, flags=[1024, 4096, 1, 0, 1052672], mode=3840, umask=97, user='root')
        state.set_acl(default=True, entry=v57, group='group4', group_perm=set(), logical=True, mask={'r', 'w', 'x'}, not_recalc_mask=True, other_perm={'r', 'w', 'x'}, physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='user2', user_perm=set())
        state.open(file=v37, flags=[512], mode=3155, umask=273, user='root')
        v62 = state.create_file(content=b'0\x8c', file_name='gxht', mode='a', parent=v1, umask=402, user='root')
        v65 = state.rename_file(entry=v62, new_entry_name='jpaz', parent=v1, umask=406, user='root')
        v70 = state.rename_file(entry=v65, new_entry_name='fatg', parent=v1, umask=1, user='root')
        v72 = state.create_file(content=b'\x92K\xf6@\x04\x1c\xed\x047\xad\xc4\xba\\\xccI', file_name='stjt', mode='w', parent=v1, umask=115, user='root')
        state.open(file=v47, flags=[2, 1052672, 1, 0], mode=3846, umask=333, user='root')
        state.open(file=v36, flags=[1052672, 1, 64, 2], mode=0, umask=23, user='root')
        state.open(file=v45, flags=[512, 2, 128, 0, 1052672, 1024, 64, 1, 4096], mode=1348, umask=455, user='root')
        v78 = state.set_acl(default=True, entry=v57, group='group2', group_perm={'r', 'w', 'x'}, logical=True, mask={'r', 'w', 'x'}, not_recalc_mask=False, other_perm={'r'}, physical=True, recalc_mask=False, recursive=True, set_mask=True, sudo_user='root', user='root', user_perm=set())
        v81 = state.set_acl(default=False, entry=v41, group=v1, group_perm=set(), logical=True, mask=set(), not_recalc_mask=True, other_perm=set(), physical=False, recalc_mask=True, recursive=False, set_mask=True, sudo_user='root', user='root', user_perm=set())
        state.open(file=v19, flags=[512, 0], mode=1487, umask=360, user='root')
        v82 = state.hardlink(dest_file=v32, link_file_name='ahhw', parent=v1, umask=52, user='root')
        v84 = state.set_acl(default=False, entry=v1, group='root', group_perm=set(), logical=False, mask={'r', 'w', 'x'}, not_recalc_mask=True, other_perm=set(), physical=False, recalc_mask=False, recursive=False, set_mask=False, sudo_user='root', user='root', user_perm={'r'})
        v88 = state.create_file(content=b'\x15', file_name='fuod', mode='a', parent=v1, umask=132, user='root')
        v89 = state.create_file(content=b'\x03\x07\x01', file_name='aeza', mode='a', parent=v1, umask=21, user='root')
        v90 = state.set_acl(default=True, entry=v88, group='user2', group_perm=set(), logical=True, mask=set(), not_recalc_mask=False, other_perm={'r'}, physical=False, recalc_mask=True, recursive=True, set_mask=True, sudo_user='root', user='user2', user_perm=set())
        state.open(file=v89, flags=[2], mode=0, umask=1, user='root')
        state.open(file=v89, flags=[2, 512, 1052672], mode=4029, umask=336, user='root')
        state.open(file=v11, flags=[64, 128, 1052672, 512, 0, 1, 1024, 2, 4096], mode=2253, umask=386, user='root')
        state.open(file=v72, flags=[4096], mode=2416, umask=79, user='root')
        v98 = state.set_acl(default=False, entry=v1, group='root', group_perm={'r'}, logical=True, mask=set(), not_recalc_mask=True, other_perm={'w'}, physical=False, recalc_mask=True, recursive=False, set_mask=True, sudo_user='root', user='root', user_perm=set())
        state.open(file=v45, flags=[4096, 512, 64, 1, 0, 1052672, 1024, 128, 2], mode=3793, umask=245, user='root')
        v99 = state.hardlink(dest_file=v37, link_file_name='kyhx', parent=v1, umask=125, user='root')
        state.open(file=v82, flags=[1052672, 4096, 64], mode=2939, umask=252, user='root')
        v121 = state.set_acl(default=False, entry=v1, group='user3', group_perm={'r'}, logical=True, mask={'x'}, not_recalc_mask=True, other_perm=set(), physical=False, recalc_mask=True, recursive=True, set_mask=True, sudo_user='root', user='root', user_perm={'r', 'w', 'x'})
        state.open(file=v37, flags=[2], mode=3423, umask=362, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()