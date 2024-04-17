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

if __name__ == '__main__':
    unittest.main()