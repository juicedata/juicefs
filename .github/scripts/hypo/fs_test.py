import os
import unittest
from fs import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_issue_1296(self):
        # reproduce https://github.com/juicedata/jfs/issues/1296
        for i in range(100):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.create_file(content=b'', file_name='aaac', parent=v1, user='root')
            state.rebalance_dir(dir=v1, is_vdir=False, zone1=v1, zone2='.jfszone0')
            state.hardlink(src_file=v2, link_file_name='aaaa', parent=v1, user='root')
            state.teardown()

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

    def test_issue_1539(self):
        # SEE https://github.com/juicedata/jfs/issues/1539
        for i in range(30):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.mkdir(mode=0, parent=v1, subdir='a', umask=18, user='root')
            v3 = state.rename_dir(entry=v2, new_entry_name=v2, parent=v1, umask=18, user='root')
            v4 = state.mkdir(mode=0, parent=v2, subdir=v2, umask=18, user='root')
            state.rebalance_dir(dir=v4, is_vdir=False, zone1=v1, zone2='.jfszone0')
            v5 = state.rename_dir(entry=v4, new_entry_name=v2, parent=v1, umask=18, user='root')
            state.teardown()

    def test_issue_1540(self):
        # SEE https://github.com/juicedata/jfs/issues/1540
        for i in range(30):
            state = JuicefsMachine()
            v1 = state.init_folders()
            state.rebalance_dir(dir=v1, is_vdir=False, zone1=v1, zone2='.jfszone0')
            v2 = state.create_file(content=b'[e=', file_name='k', parent=v1, umask=18, user='root')
            state.stat(entry=v1, user='root')
            v3 = state.hardlink(src_file=v2, link_file_name='ia', parent=v1, umask=18, user='root')
            state.split_dir(dir=v1, vdirs=29)
            state.rebalance_dir(dir=v1, is_vdir=True, zone1=v1, zone2='.jfszone1')
            state.stat(entry=v2, user='root')
            state.teardown()

    def test_issue_1659(self):
        # SEE https://github.com/juicedata/jfs/issues/1659
        for i in range(100):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.create_file(content=b'\xf0\x9e\x86\xb5\xf4\x8e\xab\xb0V\xf1\xa1\xb6\xad-', file_name='qt', parent=v1, umask=18, user='root')
            state.rebalance_dir(dir=v1, is_vdir=False, zone1=v1, zone2='.jfszone1')
            v3 = state.create_file(content=b'1\xc4\x99', file_name='a', parent=v1, umask=18, user='root')
            v4 = state.hardlink(src_file=v3, link_file_name='wedl', parent=v1, umask=18, user='root')
            v5 = state.rename_file(entry=v2, new_entry_name='zv', parent=v1, umask=18, user='root')
            v7 = state.mkdir(mode=0, parent=v1, subdir='o', umask=18, user='root')
            v8 = state.copy_file(entry=v5, follow_symlinks=True, new_entry_name='pi', parent=v7, umask=18, user='root')
            v10 = state.copy_file(entry=v4, follow_symlinks=True, new_entry_name='cb', parent=v7, umask=18, user='root')
            v11 = state.hardlink(src_file=v8, link_file_name='ulix', parent=v1, umask=18, user='root')
            v12 = state.mkdir(mode=464, parent=v7, subdir='s', umask=18, user='root')
            v15 = state.mkdir(mode=454, parent=v12, subdir='xmrcszwe', umask=18, user='root')
            state.split_dir(dir=v1, vdirs=21)
            v16 = state.mkdir(mode=373, parent=v7, subdir='i', umask=18, user='root')
            state.split_dir(dir=v1, vdirs=29)
            state.unlink(file=v10, user='root')
            v20 = state.create_file(content=b'', file_name='tgct', parent=v16, umask=18, user='root')
            state.rebalance_dir(dir=v7, is_vdir=True, zone1=v1, zone2='.jfszone0')
            v21 = state.hardlink(src_file=v4, link_file_name='pr', parent=v16, umask=18, user='root')
            state.unlink(file=v4, user='root')
            v24 = state.rename_file(entry=v8, new_entry_name='sks', parent=v15, umask=18, user='root')
            state.split_dir(dir=v12, vdirs=4)
            state.rebalance_dir(dir=v15, is_vdir=False, zone1=v1, zone2='.jfszone0')
            v26 = state.rename_file(entry=v24, new_entry_name='t', parent=v1, umask=18, user='root')
            state.split_dir(dir=v16, vdirs=8)
            v27 = state.mkdir(mode=58, parent=v12, subdir='z', umask=18, user='root')
            v28 = state.mkdir(mode=380, parent=v27, subdir='r', umask=18, user='root')
            state.open(file=v20, flags=[64], mode=397, umask=18, user='root')
            v34 = state.create_file(content=b'_Ea7A', file_name='sqm', parent=v12, umask=18, user='root')
            state.rebalance_dir(dir=v1, is_vdir=True, zone1=v1, zone2='.jfszone1')
            v36 = state.mkdir(mode=118, parent=v15, subdir=v3, umask=18, user='root')
            state.unlink(file=v34, user='root')
            state.unlink(file=v26, user='root')
            state.split_dir(dir=v28, vdirs=30)
            state.rebalance_dir(dir=v1, is_vdir=True, zone1=v1, zone2='.jfszone1')
            # v44 = state.mkdir(mode=47, parent=v36, subdir='ortafrfz', umask=18, user='root')
            state.rebalance_dir(dir=v15, is_vdir=True, zone1=v1, zone2='.jfszone0')
            state.rebalance_dir(dir=v16, is_vdir=False, zone1=v1, zone2='.jfszone0')
            state.rename_file(entry=v21, new_entry_name='r', parent=v1, umask=18, user='root')
            state.teardown()
            
    def test_issue_1709(self):
        for i in range(100):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.mkdir(mode=433, parent=v1, subdir='pbf', umask=18, user='root')
            v3 = state.create_file(content=b'C', file_name='hn', parent=v1, umask=18, user='root')
            state.rebalance_file(file=v3, zone1=v1, zone2='.jfszone0')
            v6 = state.hardlink(link_file_name='fxok', parent=v2, src_file=v3, umask=18, user='root')
            state.fallocate(file=v3, length=10, mode=0, offset=10, user='root')
            v7 = state.rename_file(entry=v6, new_entry_name='jj', parent=v2, umask=18, user='root')
            state.read(encoding='ascii', errors='replace', file=v7, length=10, mode='w', offset=0, user='root', whence=0)
            state.stat(entry=v3, user='root')
            state.teardown()
            
    def skip_test_issue_1767(self):
        for i in range(30):
            state = JuicefsMachine()
            v1 = state.init_folders()
            v2 = state.create_file(content=b'', file_name='a', parent=v1, umask=18, user='root')
            state.rebalance_dir(dir=v1, is_vdir=False, zone1=v1, zone2='.jfszone0')
            v3 = state.hardlink(link_file_name='b', parent=v1, src_file=v2, umask=18, user='root')
            state.get_acl(entry=v2)
            state.chown(entry=v3, owner='user1', user='root')
            state.get_acl(entry=v2)
            state.teardown()

    def skip_test_issue_2045(self):
        state = JuicefsMachine()
        folders_0 = state.init_folders()
        state.listdir(dir=folders_0, user='root')
        files_0 = state.create_file(content=b'\xc3\xa6', file_name='rvlo', parent=folders_0, umask=18, user='root')
        state.rebalance_dir(dir=folders_0, is_vdir=False, zone1=folders_0, zone2='.jfszone1')
        state.read(encoding='utf-8', errors='replace', file=files_0, length=8876, mode='rb', offset=3106, user='root', whence=2)
        state.rebalance_dir(dir=folders_0, is_vdir=True, zone1=folders_0, zone2='.jfszone0')
        state.exists(entry=folders_0, user='root')
        state.copy_file_range(count=4576, dst=files_0, dst_offset=8754, src=files_0, src_offset=3036, user='root')
        state.chown(entry=files_0, owner='root', user='root')
        state.rebalance_dir(dir=folders_0, is_vdir=False, zone1=folders_0, zone2='.jfszone1')
        state.rebalance_dir(dir=folders_0, is_vdir=False, zone1=folders_0, zone2='.jfszone1')
        files_1 = state.hardlink(link_file_name='r', parent=folders_0, src_file=files_0, umask=18, user='root')
        state.write(content=b'u\xc2\x91\xc3\x95b\xc2\x85\xc3\x9bG\xc2\x91\xc2\x94\xf1\x94\x8d\xb6\xc3\x9a', encoding='latin-1', errors='backslashreplace', file=files_0, mode='a+b', offset=9899, user='root', whence=2)
        state.change_groups(group='user3', groups=['user3', 'group4', 'user2', 'root', 'group3', 'group2'], user='user1')
        state.change_groups(group='root', groups=[], user='user3')
        state.copy_file_range(count=1465, dst=files_0, dst_offset=156, src=files_0, src_offset=5656, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()