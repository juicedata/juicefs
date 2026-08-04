import os
import unittest
from hypothesis.stateful import RuleBasedStateMachine
from fs import JuicefsMachine

class TestFsrand2(unittest.TestCase):
    def test_issue_7315(self):
        state = RuleBasedStateMachine.__new__(JuicefsMachine)
        RuleBasedStateMachine.__init__(state)

        bundle_values = {
            'files': ('parent/child.txt', 'parentX/other.txt'),
            'folders': ('', 'parent/sub'),
            'entry_with_acl': ('parent', 'parent/child.txt'),
            'xattrs': (('parent/child.txt', 'user.attr'),),
        }
        for name, values in bundle_values.items():
            state._add_results_to_targets((name,), values)

        class FsOp:
            root_dir = '/tmp/fs'

            def do_rename(self, **kwargs):
                return None

        state.fsop1 = FsOp()
        state.fsop2 = FsOp()
        result = state.rename_dir('parent', '', 'renamed')
        self.assertEqual(result, 'renamed')

        def get_bundle_values(name):
            return [state.names_to_values[ref.name]
                    for ref in state.bundles[name]]

        self.assertEqual(get_bundle_values('files'),
                         ['renamed/child.txt', 'parentX/other.txt'])
        self.assertEqual(get_bundle_values('folders'), ['', 'renamed/sub'])
        self.assertEqual(get_bundle_values('entry_with_acl'),
                         ['renamed', 'renamed/child.txt'])
        self.assertEqual(get_bundle_values('xattrs'),
                         [('renamed/child.txt', 'user.attr')])

        state._update_paths_after_rename('', 'not-root')
        self.assertEqual(get_bundle_values('folders'), ['', 'renamed/sub'])

        state.fsop1.do_rename = lambda **kwargs: OSError('rename failed')
        state.fsop2.do_rename = lambda **kwargs: OSError('rename failed')
        result = state.rename_dir('renamed', '', 'failed')
        self.assertEqual(result, 'renamed')
        self.assertEqual(get_bundle_values('files'),
                         ['renamed/child.txt', 'parentX/other.txt'])

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

    def test_x(self):
        # See: https://github.com/juicedata/jfs/issues/918
        state = JuicefsMachine()
        v1 = state.init_folders()
        v2 = state.create_file(content=b'', file_name='lcka', mode='wb', parent=v1, user='root')
        state.teardown()

if __name__ == '__main__':
    unittest.main()
