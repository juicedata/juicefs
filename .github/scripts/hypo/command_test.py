import unittest
from command import JuicefsCommandMachine

class TestCommand(unittest.TestCase):
    def test_dump(self):
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        files_0 = state.create_file(content='', file_name='aazz', mode='w', parent=folders_0, umask=312, user='root')
        value = ''.join([chr(i) for i in range(256)])
        value = value.encode('latin-1')
        value = b'\x2580q\x2589'
        value = b'M\x25DB'
        state.set_xattr(file=files_0, flag=1, name='\x9d', user='root', value=value)
        state.dump_load_dump(folders_0)
        state.teardown()

    def skip_test_info(self):
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        files_2 = state.create_file(content='0', file_name='mvvd', mode='a', parent=folders_0, umask=293, user='root')
        state.info(entry=folders_0, raw=True, recuisive=True, user='user1')
        state.teardown()

    def test_clone(self):
        state = JuicefsCommandMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content='\x9bcR\xba', file_name='ygbl', mode='x', parent=v1, umask=466, user='root')
        state.chmod(entry=v1, mode=715, user='root')
        state.clone(entry=v2, new_entry_name='drqj', parent=v1, preserve=False, user='user1')
        state.teardown()

    def test_config(self):
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        state.config(capacity=1, enable_acl=True, encrypt_secret=True, force=False, inodes=81, trash_days=0, user='root', yes=True)
        state.teardown()

    def test_clone_4834(self):
        #SEE https://github.com/juicedata/juicefs/issues/4834
        state = JuicefsCommandMachine()
        folders_0 = state.init_folders()
        state.chmod(entry=folders_0, mode=2427, user='root')
        folders_1 = state.mkdir(mode=2931, parent=folders_0, subdir='vhjp', umask=369, user='root')
        state.chmod(entry=folders_1, mode=1263, user='root')
        state.clone(entry=folders_1, new_entry_name='tbim', parent=folders_0, preserve=False, user='user1')
        state.teardown()

if __name__ == '__main__':
    unittest.main()