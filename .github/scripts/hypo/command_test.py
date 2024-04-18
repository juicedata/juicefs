import unittest
from command import JuicefsCommandMachine

class TestCommand(unittest.TestCase):
    def test_dump(self):
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        files_0 = state.create_file(content=b'', file_name='aazz', mode='w', parent=folders_0, umask=312, user='root')
        value = ''.join([chr(i) for i in range(256)])
        value = value.encode('latin-1')
        value = b'\x2580q\x2589'
        value = b'M\x25DB'
        state.set_xattr(file=files_0, flag=1, name='\x9d', user='root', value=value)
        state.dump_load_dump(folders_0)
        state.teardown()

    def test_info(self):
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        files_2 = state.create_file(content=b'0', file_name='mvvd', mode='a', parent=folders_0, umask=293, user='root')
        state.info(entry=folders_0, raw=True, recuisive=True, user='user1')
        state.teardown()

    def test_info2(self):
        state = JuicefsCommandMachine()
        v1 = state.init_folders()
        state.info(entry=v1, raw=True, recuisive=False, strict=True, user='user1')
        v2 = state.create_file(content=b'*', file_name='fzlc', mode='a', parent=v1, umask=368, user='root')
        state.fallocate(file=v2, length=98993, mode=0, offset=354, user='root')
        v3 = state.hardlink(dest_file=v2, link_file_name='xtkb', parent=v1, umask=196, user='root')
        state.truncate(file=v3, size=117319, user='root')
        v4 = state.create_file(content=b'\xf7', file_name='nfyb', mode='w', parent=v1, umask=289, user='root')
        state.truncate(file=v2, size=109323, user='root')
        v5 = state.create_file(content=b'e\xc8p', file_name='aujn', mode='x', parent=v1, umask=292, user='root')
        state.info(entry=v1, raw=True, recuisive=True, strict=True, user='root')
        v6 = state.create_file(content=b'\x06', file_name='cgac', mode='w', parent=v1, umask=257, user='root')
        state.fsck(entry=v1, recuisive=True, repair=False, user='root')
        v7 = state.create_file(content=b'^\xda', file_name='litc', mode='w', parent=v1, umask=16, user='root')
        state.fsck(entry=v1, recuisive=True, repair=True, user='user1')
        state.fsck(entry=v1, recuisive=True, repair=True, user='user1')
        state.fsck(entry=v1, recuisive=True, repair=False, user='user1')
        state.fsck(entry=v6, recuisive=True, repair=True, user='user1')
        v8 = state.create_file(content=b'', file_name='bbab', mode='w', parent=v1, umask=257, user='root')
        v9 = state.create_file(content=b'\x01\x00\x01\x01\x01', file_name='bbbb', mode='x', parent=v1, umask=257, user='root')
        state.fsck(entry=v1, recuisive=True, repair=True, user='user1')
        state.truncate(file=v8, size=65793, user='root')
        state.fsck(entry=v8, recuisive=True, repair=True, user='user1')
        state.fsck(entry=v8, recuisive=True, repair=True, user='user1')
        state.fsck(entry=v1, recuisive=False, repair=True, user='root')
        state.truncate(file=v3, size=4, user='root')
        state.fsck(entry=v8, recuisive=True, repair=True, user='user1')
        v10 = state.hardlink(dest_file=v7, link_file_name='hgea', parent=v1, umask=57, user='root')
        state.truncate(file=v3, size=32516, user='root')
        state.info(entry=v10, raw=True, recuisive=True, strict=True, user='user1')
        state.teardown()
        
if __name__ == '__main__':
    unittest.main()