import unittest
import subprocess
try: 
    __import__('xattr')
except ImportError:
    subprocess.check_call(["pip", "install", "xattr"])
import xattr
from fs import JuicefsMachine

class TestPySdk(unittest.TestCase):
    def test_issue_1331(self):
        # SEE: https://github.com/juicedata/jfs/issues/1331
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.mkdir(mode=0o2164, parent=v1, subdir='ouyz', umask=0o022,  user='root')
        state.teardown()

    def test_issue_1339(self):
        # SEE: https://github.com/juicedata/jfs/issues/1339
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.exists(entry=v1,  user='root')
        v2 = state.loop_symlink(link_file_name='kydl', parent=v1)
        state.exists(entry=v2,  user='root')
        state.teardown()
        
    def test_issue_1349(self):
        # SEE: https://github.com/juicedata/jfs/issues/1349
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.loop_symlink(link_file_name='pmjl', parent=v1)
        state.set_xattr(file=v2, flag=xattr.XATTR_CREATE, name='user.abc',  user='root', value=b'def')
        state.teardown()

    def test_issue_1359(self):
        # SEE: https://github.com/juicedata/jfs/issues/1359
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.merge_dir(dir=v1)
        state.create_file(buffering=0, content=b'\x16\x0cu\x01\x01\x01\x01\x01\x01', file_name='bbbb', mode='ab', parent=v1, umask=18, user='root')
        state.teardown()

    def test_issue_1361(self):
        # SEE: https://github.com/juicedata/jfs/issues/1361
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=1, content=b'', file_name='abbc', mode='ab', parent=v1, umask=18, user='root')
        state.hardlink(src_file=v2, link_file_name='aaaa', parent=v1, umask=18, user='root')
        state.teardown()

    def test_issue_1362(self):
        #SEE: https://github.com/juicedata/jfs/issues/1362
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=-1, content=b'abc', file_name='iazj', mode='ab', parent=v1, umask=18, user='root')
        state.read(file=v2, length=4949, mode='w+', offset=1, user='root', whence=2)
        state.teardown()

    def test_issue_1364(self):
        # SEE: https://github.com/juicedata/jfs/issues/1364
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=10, content=b'', file_name='abag', mode='wb', parent=v1, umask=18, user='root')
        state.set_xattr(file=v2, flag=xattr.XATTR_REPLACE, name='user.abc', user='root', value=b'def')
        state.teardown()

    def test_issue_1365(self):
        # SEE: https://github.com/juicedata/jfs/issues/1365
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.mkdir(mode=15, parent=v1, subdir='coue', umask=18, user='root')
        state.rmdir(dir=v2, user='root')
        state.teardown()
    
    def test_issue_1369(self):
        # SEE: https://github.com/juicedata/jfs/issues/1369
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=-1, content=b'', file_name='aaaa', mode='wb', parent=v1, umask=18,  user='root')
        state.write(content=b'abcd', file=v2, mode='rb', offset=0,  user='root', whence=0)
        state.teardown()

    def test_issue_1369_2(self):
        # SEE: https://github.com/juicedata/jfs/issues/1369
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=-1, content=b'\x00', file_name='aaaa', mode='wb', parent=v1, umask=18,  user='root')
        state.write(content=b'', file=v2, mode='xb', offset=0,  user='root', whence=0)
        state.teardown()

    def test_issue_1369_3(self):
        # SEE: https://github.com/juicedata/jfs/issues/1369
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'a', file_name='aaaa', mode='wb', parent=v1, umask=18,  user='root')
        state.write(content=b'b', file=v2, mode='ab', offset=0,  user='root', whence=0)
        state.teardown()

    def skip_test_issue_1370(self):
        # SEE: https://github.com/juicedata/jfs/issues/1370
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(buffering=-1, content=b'', file_name='aaab', mode='wb', parent=v1, umask=18,  user='root')
        v3 = state.symlink(src_file=v2, link_file_name='aaaa', parent=v1, umask=18, user='root')
        state.unlink(file=v2,  user='root')
        state.open2(file=v3, mode='w+',  user='root')
        state.teardown()

    def test_issue_1419(self):
        # SEE: https://github.com/juicedata/jfs/issues/1419
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.lstat(entry=v1,  user='root')
        v2 = state.create_file(content=b'^\x85\n\xa1;1*ek\xc8', file_name='d', parent=v1, umask=18,  user='root')
        state.readline(file=v2, mode='a+', offset=9070,  user='root', whence=0)
        state.teardown()

    def test_issue_1422(self):
        # SEE: https://github.com/juicedata/jfs/issues/1422
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'a', file_name='p', parent=v1, umask=18,  user='root')
        v3 = state.symlink(src_file=v2, link_file_name='ab', parent=v1, umask=18,  user='root')
        state.hardlink(src_file=v3, link_file_name='a', parent=v1, umask=18,  user='root')
        state.teardown()

    def test_issue_1424(self):
        # SEE: https://github.com/juicedata/jfs/issues/1424
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content='²', mode='a', file_name='w', parent=v1, umask=18,  user='root')
        state.readline(file=v2, mode='r', offset=1708,  user='root', whence=0)
        state.teardown()

    # def test_issue_1425(self):
    #     # SEE: https://github.com/juicedata/jfs/issues/1425
    #     state = JuicefsMachine()
    #     v1 = state.init_folders()
    #     v2 = state.create_file(content=b'a', file_name='a', parent=v1, umask=18,  user='root')
    #     v3 = state.mkdir(mode=0, parent=v1, subdir='b', umask=18,  user='root')
    #     state.rename_dir(entry=v3, new_entry_name=v2, parent=v1, umask=18,  user='root')
    #     state.teardown()

    def test_issue_1442(self):
        # SEE: https://github.com/juicedata/jfs/issues/1442
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'aqdranzfk', file_name='fz', parent=v1, umask=18, user='root')
        state.set_xattr(file=v2, flag=0, name='user.0', user='root', value=b'\x01\x01\x00\x01')
        state.teardown()

    # def test_issue_1443(self):
    #     # SEE: https://github.com/juicedata/jfs/issues/1443
    #     state = JuicefsMachine()
    #     v1 = state.init_folders()
    #     v2 = state.create_file(content=b'bcb', file_name='bcba', parent=v1, umask=18, user='root')
    #     v3 = state.hardlink(src_file=v2, link_file_name='a', parent=v1, umask=18, user='root')
    #     state.rename_file(entry=v2, new_entry_name=v3, parent=v1, umask=18, user='root')
    #     state.teardown()

    def test_issue_1449(self):
        # SEE: https://github.com/juicedata/jfs/issues/1449
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'\x1d\x00', file_name='b', parent=v1, umask=18, user='root')
        state.listdir(dir=v1, user='root')
        state.readline(file=v2, mode='r', offset=0, user='root', whence=0)
        state.teardown()

    def test_issue_1450(self):
        # SEE: https://github.com/juicedata/jfs/issues/1450
        state = JuicefsMachine()
        v1 = state.init_folders()
        v6 = state.create_file(content=b'\xa5\x08\xee', file_name='mzeg', parent=v1, umask=18, user='root')
        state.readline(file=v6, mode='r+', offset=1, user='root', whence=0)
        state.teardown()

    def skip_test_issue_1450_2(self):
        # SEE: https://github.com/juicedata/jfs/issues/1450 
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'10', file_name='v', parent=v1, umask=18, user='root')
        state.read(encoding='utf-16', errors='strict', file=v2, length=2, mode='r', offset=0, user='root', whence=0)
        state.teardown()

    def test_issue_1457(self):
        # SEE: https://github.com/juicedata/jfs/issues/1457
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content='\ufeff', mode='x', file_name='v', parent=v1, umask=18, user='root')
        state.teardown()
    
    def skip_test_issue_1465(self):
        # SEE: https://github.com/juicedata/jfs/issues/1465
        state = JuicefsMachine()
        v1 = state.init_folders()
        v38 = state.create_file(content=b'\x05{\xf3\x9bg\x93\x00\ry0', file_name='kfhg', parent=v1, umask=18, user='root')
        state.readline(file=v38, mode='rb', offset=7694, user='root', whence=1)
        state.teardown()

    def test_issue_1481(self):
        # SEE: https://github.com/juicedata/jfs/issues/1481
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='b', parent=v1, umask=18, user='root')
        state.set_xattr(file=v2, flag=0, name='user.\uda5d', user='root', value=b'!')
        state.teardown()

    def test_issue_x(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'\x035\x03\x02\x00', file_name='a', parent=v1, umask=18, user='root')
        state.lstat(entry=v1, user='root')
        v3 = state.create_file(content=b'10', file_name='v', parent=v1, umask=18, user='root')
        state.write(content=b'\x01\x01', encoding='utf-8', errors='ignore', file=v2, mode='r', offset=258, user='root', whence=1)
        state.teardown()

    def test_issue_y(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', parent=v1, umask=18, user='root')
        state.readlines(file=v2, mode='a', offset=0, user='root', whence=0)
        state.teardown()

    def test_issue_z(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'1}26B', file_name='gv', parent=v1, umask=18, user='root')
        # state.write(content='\x04', encoding='utf-8', errors='ignore', file=v2, mode='a', offset=4900, user='root', whence=1)
        state.writelines(file=v2, lines=['hp', 'uwq'], mode='a+b', offset=160, user='root', whence=2)
        state.teardown()

    def test_issue_a(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='a', parent=v1, umask=18, user='root')
        state.writelines(file=v2, lines=[''], mode='rb', offset=9841, user='root', whence=1)
        state.teardown()

    def test_issue_b(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v7 = state.create_file(content=b'', file_name='vkg', parent=v1, umask=18, user='root')
        state.write(content='í', encoding='ascii', errors='strict', file=v7, mode='r', offset=5117, user='root', whence=1)
        state.teardown()

    def test_issue_c(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.create_file(content='\udfc5', mode='xb', file_name='a', parent=v1, umask=18, user='root')
        state.teardown()

    def test_issue_d(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        state.exists(entry=v1, user='root')
        v3 = state.create_file(content=b'\x10\x1ata\xd6', file_name='x', parent=v1, umask=18, user='root')
        # v15 = state.create_file(content=b'', file_name='nbln', parent=v1, umask=18, user='root')
        # v20 = state.create_file(content=b'\x82\xd7\xc0\xff\xac\x94\xe5\x8f\x03\x10', file_name='exc', parent=v1, umask=18, user='root')
        # state.write(content=b'', encoding='utf-8', errors='backslashreplace', file=v20, mode='w+', offset=3658, user='root', whence=1)
        # state.write(content=b'7q\x0b\xe4\x9f\xb4b', encoding='latin-1', errors='namereplace', file=v15, mode='r+b', offset=6691, user='root', whence=2)
        state.write(content='È\U000c3fe7𧶤÷\x89\x00𭊦cç¤Ìk', encoding='latin-1', errors='strict', file=v3, mode='r', offset=10240, user='root', whence=0)
        state.teardown()

    def test_issue_e(self):
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'abc\r', file_name='a', parent=v1, umask=18, user='root')
        state.read(file=v2, mode='r', offset=0, user='root', whence=0, length=4)
        state.teardown()

    def test_issue_f(self):
        state = JuicefsMachine()
        folders_0 = state.init_folders()
        files_0 = state.create_file(content=b'', file_name='b', parent=folders_0, umask=18, user='root')
        state.chown(entry=folders_0, owner='user1', user='root')
        state.create_file(content=b'', file_name='a', parent=folders_0, umask=18, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()
