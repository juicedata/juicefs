import json
import os
import sys

from common import ReplayTestCase, get_acl, write


class ACLTests(ReplayTestCase):
    def test_rules_inheritance_and_access(self):
        path = self.src / 'acl-seed'
        path.mkdir()
        # Leave unused rules in the source; JSON only carries rules referenced by inodes.
        for uid in range(14000, 14016):
            rule = f'u::rwx,u:{uid}:r-x,g::---,m::r-x,o::---'
            self.command('setfacl', '--set', rule, str(path))
        path.rmdir()
        base = self.src / 'acl'
        base.mkdir(parents=True)
        rule = 'u::rw-,u:12001:rw-,g::---,g:13002:r--,m::rw-,o::---'
        for name in ['baseline', 'removed', 'duplicate-a', 'duplicate-b']:
            write(base / name, b'ACL replay\n')
            self.command('setfacl', '--set', rule, str(base / name))
        for name in ['inherit', 'remove-default']:
            directory = base / name
            directory.mkdir()
            rule = 'u::rwx,u:12001:rwx,g::---,g:13002:r-x,m::rwx,o::---'
            self.command('setfacl', '--set', rule, str(directory))
            self.command('setfacl', '-d', '--set', rule, str(directory))
        (base / 'inherit' / 'sub').mkdir()
        write(base / 'inherit' / 'before', b'inherited before backup\n')
        self.bootstrap()
        os.chmod(base / 'baseline', 0o600)
        assert 'user:12001:rw-' in get_acl(base / 'baseline')
        assert 'mask::---' in get_acl(base / 'baseline')
        self.command('setfacl', '-b', str(base / 'removed'))
        os.chmod(base / 'removed', 0o600)
        assert 'user:12001:' not in get_acl(base / 'removed')
        self.command('setfacl', '-k', str(base / 'remove-default'))
        write(base / 'remove-default' / 'after', b'no default ACL\n')
        assert 'default:' not in get_acl(base / 'remove-default')
        assert 'user:12001:' not in get_acl(base / 'remove-default' / 'after')

        self.command('setfacl', '-d', '-m', 'u:12001:r-x,g:13002:---,m::r-x', str(base / 'inherit'))
        write(base / 'inherit' / 'after', b'inherited after backup\n')
        (base / 'inherit' / 'after-dir').mkdir()
        assert 'user:12001:r-x' in get_acl(base / 'inherit' / 'after')
        assert 'default:user:12001:r-x' in get_acl(base / 'inherit' / 'after-dir')

        rule = 'u::rw-,u:12004:r--,g::---,g:13004:r--,m::r--,o::---'
        for name in ['duplicate-a', 'duplicate-b']:
            self.command('setfacl', '--set', rule, str(base / name))
        assert get_acl(base / 'duplicate-a') == get_acl(base / 'duplicate-b')
        tree = base / 'live-tree'
        tree.mkdir()
        rule = 'u::rwx,u:12005:r-x,g::---,g:13005:r-x,m::r-x,o::---'
        self.command('setfacl', '--set', rule, str(tree))
        self.command('setfacl', '-d', '--set', rule, str(tree))
        (tree / 'sub').mkdir()
        write(tree / 'sub' / 'file', b'ACL clone\n')
        self.jfs('clone', '-p', tree, base / 'cloned')
        assert get_acl(tree) == get_acl(base / 'cloned')
        os.link(base / 'baseline', base / 'hardlink')
        os.sync()
        self.jfs('fsck', self.src_meta, '--path', '/case/acl/inherit', '--repair')
        self.finish({'SETFACL', 'SETATTR', 'CLONE'})
        for root in (self.src, self.dst):
            acl = root / 'acl'
            self.assertIn('mask::---', get_acl(acl / 'baseline'))
            self.assertNotIn('user:12001:', get_acl(acl / 'removed'))
            self.assertNotIn('default:', get_acl(acl / 'remove-default'))
            self.assertEqual(get_acl(acl / 'duplicate-a'), get_acl(acl / 'duplicate-b'))
            for uid, gid, expected in [(12001, 13001, [False, False, True, True]),
                                       (12002, 13002, [False, False, True, False]),
                                       (12003, 13003, [False, False, False, False])]:
                script = ('import json,os,sys; '
                          'print(json.dumps([os.access(p,os.R_OK) for p in sys.argv[1:]]))')
                paths = [acl / name for name in ('baseline', 'removed', 'inherit/before', 'inherit/after')]
                result = self.command(sys.executable, '-c', script, *paths, user=uid, group=gid, extra_groups=[])
                self.assertEqual(json.loads(result.stdout), expected)
        source = {str(p.relative_to(self.src)): get_acl(p) for p in (self.src / 'acl').rglob('*')}
        target = {str(p.relative_to(self.dst)): get_acl(p) for p in (self.dst / 'acl').rglob('*')}
        self.assertEqual(source, target)
