import errno
import os

from common import ReplayTestCase, expect_errno, payload, write


class QuotaTests(ReplayTestCase):
    pass

#     def test_inode_limit(self):
#         (self.src / 'limited').mkdir()
#         self.unmount(self.src_mount)
#         self.jfs('quota', 'set', self.src_meta, '--path', '/case/limited', '--inodes', '2')
#         self.bootstrap()
#         limited = self.src / 'limited'
#         (limited / 'first').touch()
#         (limited / 'second').touch()
#         expect_errno(errno.EDQUOT, (limited / 'third').touch)
#         (limited / 'first').unlink()
#         (limited / 'third').touch()
#         self.finish({'CREATE', 'UNLINK'})
#         self.assertEqual({path.name for path in (self.dst / 'limited').iterdir()}, {'second', 'third'})
#         for meta in (self.src_meta, self.dst_meta):
#             self.jfs('quota', 'check', meta, '--path', '/case/limited', '--strict')

#     def test_capacity_limit(self):
#         (self.src / 'limited').mkdir()
#         write(self.src / 'limited' / 'file', b'x' * 4096)
#         self.unmount(self.src_mount)
#         self.jfs('quota', 'set', self.src_meta, '--path', '/case/limited', '--capacity', '8K')
#         self.bootstrap()
#         with open(self.src / 'limited' / 'file', 'r+b', buffering=0) as stream:
#             stream.truncate(8192)
#             expect_errno(errno.EDQUOT, os.ftruncate, stream.fileno(), 12288)
#             os.fsync(stream.fileno())
#         self.finish({'TRUNCATE'})
#         self.assertEqual((self.dst / 'limited' / 'file').read_bytes(), b'x' * 4096 + b'\0' * 4096)
#         for meta in (self.src_meta, self.dst_meta):
#             self.jfs('quota', 'check', meta, '--path', '/case/limited', '--strict')

#     def test_set_update_delete_and_usage(self):
#         for name in ('existing', 'new', 'removed'):
#             (self.src / name).mkdir()
#         write(self.src / 'existing' / 'before', b'baseline quota usage')
#         self.unmount(self.src_mount)
#         for name in ('existing', 'removed'):
#             self.jfs('quota', 'set', self.src_meta, '--path', '/case/' + name,
#                      '--capacity', '1M', '--inodes', '20')
#         self.bootstrap()
#         self.jfs('quota', 'set', self.src_meta, '--path', '/case/existing', '--capacity', '2M', '--inodes', '40')
#         self.jfs('quota', 'set', self.src_meta, '--path', '/case/new', '--capacity', '1M', '--inodes', '20')
#         self.jfs('quota', 'delete', self.src_meta, '--path', '/case/removed')
#         # A fresh writer reads the changed quotas without a timing-dependent sleep.
#         self.unmount(self.src_mount)
#         self.mount(self.src_meta, self.src_mount)
#         (self.src / 'existing' / 'before').unlink()
#         for name in ('existing', 'new', 'removed'):
#             for index in range(3):
#                 write(self.src / name / str(index), payload(0, index, 4096))
#         self.finish({'SETQUOTA', 'DELQUOTA'})
#         quotas = []
#         for meta in (self.src_meta, self.dst_meta):
#             output = self.jfs('quota', 'list', meta).stdout
#             rows = {}
#             for line in output.splitlines():
#                 cells = [cell.strip() for cell in line.split('|')[1:-1]]
#                 if cells and cells[0].startswith('/case/'):
#                     rows[cells[0]] = cells[1:]
#             self.assertEqual(set(rows), {'/case/existing', '/case/new'})
#             for name in ('existing', 'new'):
#                 self.jfs('quota', 'check', meta, '--path', '/case/' + name, '--strict')
#             quotas.append(rows)
#         self.assertEqual(*quotas)
