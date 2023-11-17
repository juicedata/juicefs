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

if __name__ == '__main__':
    unittest.main()