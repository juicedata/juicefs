import concurrent.futures
import ctypes
import errno
import fcntl
import os
import stat

from common import ReplayTestCase, expect_errno, get_acl, payload, read, write


class BasicTests(ReplayTestCase):
    def test_files_directories_and_attributes(self):
        write(self.src / 'existing', b'baseline')
        (self.src / 'old-dir').mkdir()
        self.bootstrap()
        directory = self.src / 'new-dir'
        directory.mkdir()
        path = directory / 'file,()|%\n中文'
        write(path, b'hello')
        with path.open('ab', buffering=0) as stream:
            stream.write(b' world')
            os.fsync(stream.fileno())
        read(path, b'hello world')
        with path.open('r+b', buffering=0) as stream:
            stream.write(b'HELLO')
            stream.truncate(20)
            os.fsync(stream.fileno())
        write(self.src / 'existing', b'replaced')
        (self.src / 'existing').rename(directory / 'renamed')
        write(self.src / 'deleted', b'delete me')
        (self.src / 'deleted').unlink()
        (self.src / 'old-dir').rmdir()
        os.chown(path, 12001, 13001)
        os.chmod(path, 0o640)
        os.utime(path, ns=(1700000000123456789, 1700000000987654321))
        os.setxattr(path, 'user.binary', b'v,()|%\x00\xff')
        self.finish({'CREATE', 'WRITE', 'TRUNCATE', 'MOVE', 'UNLINK', 'RMDIR', 'SETATTR', 'SETXATTR'})
        target = self.dst / 'new-dir' / path.name
        read(target, b'HELLO world' + b'\0' * 9)
        read(self.dst / 'new-dir' / 'renamed', b'replaced')
        self.assertEqual((target.stat().st_uid, target.stat().st_gid, stat.S_IMODE(target.stat().st_mode)),
                         (12001, 13001, 0o640))
        self.assertFalse((self.dst / 'deleted').exists())

    def test_file_operations(self):
        self.bootstrap()
        base = self.src / 'posix'
        base.mkdir()
        a, b = base / 'a,()|%\n中文', base / 'b'
        write(a, b'A' * 16384)
        write(b, b'B' * 8192)
        libc = ctypes.CDLL(None, use_errno=True)
        libc.renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        libc.fallocate.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_longlong, ctypes.c_longlong]

        def rename(src, dst, flags):
            if libc.renameat2(-100, os.fsencode(src), -100, os.fsencode(dst), flags):
                raise OSError(ctypes.get_errno(), 'renameat2')
        def allocate(fd, mode, offset, length):
            if libc.fallocate(fd, mode, offset, length):
                raise OSError(ctypes.get_errno(), 'fallocate')

        rename(a, base / 'no-replace', 1)
        a = base / 'no-replace'
        rename(a, b, 2)
        read(a, b'B' * 8192)
        read(b, b'A' * 16384)
        os.chmod(b, 0o6755)
        os.chown(b, 12001, 13001)
        assert b.stat().st_mode & 0o6000 == 0
        (base / 'dir-a').mkdir()
        (base / 'dir-b').mkdir()
        write(base / 'dir-a' / 'child', b'child')
        rename(base / 'dir-a', base / 'dir-b', 2)
        read(base / 'dir-b' / 'child', b'child')
        os.setxattr(a, 'user.edge', b'v,()|%\x00\xff', os.XATTR_CREATE)
        os.setxattr(a, 'user.edge', b'replaced', os.XATTR_REPLACE)
        assert os.getxattr(a, 'user.edge') == b'replaced'
        os.removexattr(a, 'user.edge')
        with open(b, 'r+b', buffering=0) as src, open(base / 'range', 'w+b', buffering=0) as dst:
            assert os.copy_file_range(src.fileno(), dst.fileno(), 32768, 4096, 8192) == 12288
            assert os.copy_file_range(src.fileno(), dst.fileno(), 1, 16384, 0) == 0
            assert os.pread(dst.fileno(), 20480, 0) == b'\x00' * 8192 + b'A' * 12288
            allocate(dst.fileno(), 1, 65536, 4096)  # KEEP_SIZE
            assert os.fstat(dst.fileno()).st_size == 20480
            allocate(dst.fileno(), 3, 9000, 2048)  # PUNCH_HOLE | KEEP_SIZE
            assert os.pread(dst.fileno(), 2048, 9000) == b'\x00' * 2048
            allocate(dst.fileno(), 16, 24000, 4096)  # ZERO_RANGE, extends length
            assert os.fstat(dst.fileno()).st_size == 28096
            allocate(dst.fileno(), 17, 30000, 4096)
            assert os.fstat(dst.fileno()).st_size == 28096
            dst.truncate(0)
            dst.truncate(4097)
            assert os.pread(dst.fileno(), 4097, 0) == b'\x00' * 4097
            for i in range(12):
                assert os.pwrite(dst.fileno(), bytes([i]) * 4096, 1) == 4096
                os.fsync(dst.fileno())
            fcntl.flock(dst.fileno(), fcntl.LOCK_EX)
            fcntl.lockf(dst.fileno(), fcntl.LOCK_EX, 16)
            fcntl.lockf(dst.fileno(), fcntl.LOCK_UN, 16)
            fcntl.flock(dst.fileno(), fcntl.LOCK_UN)
        self.jfs('compact', base / 'range')
        read(base / 'range', b'\x00' + bytes([11]) * 4096)
        os.sync()
        self.finish({'MOVE', 'SETATTR', 'TRUNCATE', 'FALLOCATE', 'COPYFILERANGE', 'WRITE',
                     'COMPACTCHUNK', 'SETXATTR', 'REMOVEXATTR'})
        read(self.dst / 'posix' / 'range', b'\0' + bytes([11]) * 4096)

    def test_clone_and_batch_clone(self):
        base = self.src
        tree = base / 'clone-source'
        tree.mkdir()
        (tree / 'empty-dir').mkdir()
        (tree / 'nested').mkdir()
        # Cross the 1000-entry changelog batch boundary, including an uneven final batch.
        for i in range(1005):
            (tree / f'empty-{i:04d}').touch()
        write(tree / 'data', payload(0, 0))
        write(tree / 'nested' / 'data', payload(1, 0))
        os.chown(tree / 'data', 12001, 13001)
        os.chmod(tree / 'data', 0o640)
        os.setxattr(tree / 'data', 'user.clone', b'clone,()|%\x00\xff')
        self.command('setfacl', '-m', 'u:12002:r--', tree / 'data')
        os.link(tree / 'data', tree / 'hardlink')
        os.symlink('data', tree / 'symlink')
        os.symlink('missing,()|%\n中文', tree / 'dangling')
        write(tree / 'name,()|%\n中文', b'escaped batch clone name')
        with open(tree / 'sparse', 'wb', buffering=0) as f:
            f.seek(64 * 1024 * 1024 - 2)
            f.write(b'cross-chunk')
            os.fsync(f.fileno())
        denied = base / 'clone-denied'
        denied.mkdir()
        (denied / 'unreadable').mkdir(mode=0)
        (base / 'clone-failed').mkdir(mode=0o777)
        os.chmod(base / 'clone-failed', 0o777)
        os.sync()
        self.bootstrap()
        for preserve, name in [(False, 'clone-plain'), (True, 'clone-preserved')]:
            target = base / name
            self.jfs('clone', '--threads', '2', *(['-p'] if preserve else []), tree, target)
            names, cloned = {p.name for p in tree.iterdir()}, {p.name for p in target.iterdir()}
            assert names == cloned, {'missing': sorted(names - cloned), 'extra': sorted(cloned - names)}
            for name in ['data', 'hardlink', 'nested/data', 'sparse']:
                assert (target / name).read_bytes() == (tree / name).read_bytes(), name
                assert (target / name).stat().st_ino != (tree / name).stat().st_ino
            assert os.readlink(target / 'dangling') == os.readlink(tree / 'dangling')
            assert os.getxattr(target / 'data', 'user.clone') == os.getxattr(tree / 'data', 'user.clone')
            assert get_acl(target / 'data') == get_acl(tree / 'data')
            assert (target / 'data').stat().st_uid == (12001 if preserve else 0)
            assert (target / 'data').stat().st_gid == (13001 if preserve else 0)
            # Clones get independent inodes even when the source entries are hardlinks.
            assert (target / 'data').stat().st_ino != (target / 'hardlink').stat().st_ino
        with open(tree / 'data', 'r+b', buffering=0) as held:
            self.jfs('clone', '-p', tree / 'data', base / 'clone-file')
            original = (base / 'clone-file').read_bytes()
            held.write(b'changed source')
            os.fsync(held.fileno())
            assert (base / 'clone-file').read_bytes() == original
        write(base / 'clone-file', b'changed clone')
        assert (tree / 'data').read_bytes() != b'changed clone'
        for src, dst in [(tree, base / 'clone-preserved'), (tree, tree / 'child'),
                         (base / 'missing', base / 'clone-missing')]:
            result = self.jfs('clone', src, dst, check=False)
            assert result.returncode != 0, (src, dst)
        # The top directory is cloned before the unreadable child fails, exercising CLEANUP.
        target = base / 'clone-failed' / 'partial'
        result = self.jfs('clone', base / 'clone-denied', target, check=False,
                          user=12003, group=13003, extra_groups=[])
        assert result.returncode != 0 and 'permission denied' in result.stderr.lower(), result
        assert not target.exists()
        os.mkfifo(base / 'fifo', 0o640)
        self.jfs('clone', base / 'fifo', base / 'cloned-fifo')
        assert stat.S_ISFIFO((base / 'cloned-fifo').stat().st_mode)
        os.sync()
        self.finish({'CLONE', 'CLONEBATCH', 'ATTACH', 'CLEANUP', 'WRITE', 'TRUNCATE'})
        for name in ('clone-plain', 'clone-preserved'):
            self.assertGreater(len(list((self.dst / name).iterdir())), 1000)
        read(self.dst / 'clone-file', b'changed clone')

    def test_links_and_batch_unlink(self):
        write(self.src / 'original', b'linked data')
        self.bootstrap()
        original = self.src / 'original'
        os.link(original, self.src / 'survivor')
        os.symlink('survivor', self.src / 'symlink')
        batch = self.src / 'batch'
        batch.mkdir()
        for index in range(1005):
            (batch / str(index)).touch()
        os.link(original, batch / 'hardlink')
        os.symlink('../survivor', batch / 'symlink')
        write(batch / 'held', b'open file')
        with open(batch / 'held', 'r+b', buffering=0) as stream:
            self.jfs('rmr', batch)
            stream.write(b'after unlink')
            os.fsync(stream.fileno())
            self.assertEqual(os.pread(stream.fileno(), 12, 0), b'after unlink')
        original.unlink()
        self.assertEqual((self.src / 'survivor').stat().st_nlink, 1)
        self.finish({'LINK', 'UNLINK', 'UNLINKBATCH', 'DELSUSTAINED'})
        read(self.dst / 'symlink', b'linked data')
        self.assertEqual((self.dst / 'survivor').stat().st_nlink, 1)
        self.assertFalse((self.dst / 'batch').exists())

    def test_open_files_after_unlink_and_rename(self):
        write(self.src / 'baseline-open', b'baseline')
        self.bootstrap()
        with open(self.src / 'baseline-open', 'r+b', buffering=0) as held:
            inode = os.fstat(held.fileno()).st_ino
            (self.src / 'baseline-open').unlink()
            write(self.src / 'baseline-open', b'new inode')
            self.assertNotEqual((self.src / 'baseline-open').stat().st_ino, inode)
            held.write(b'after unlink')
            held.truncate(8193)
            os.fsync(held.fileno())
            write(self.src / 'recovered', os.pread(held.fileno(), 8193, 0))
        write(self.src / 'left', b'left')
        write(self.src / 'right', b'right')
        with open(self.src / 'left', 'r+b', buffering=0) as left, open(self.src / 'right', 'r+b', buffering=0) as right:
            os.replace(self.src / 'left', self.src / 'right')
            self.assertEqual(os.pread(right.fileno(), 5, 0), b'right')
            right.write(b'overwritten')
            left.write(b'renamed')
            os.fsync(right.fileno())
            os.fsync(left.fileno())
        self.finish({'UNLINK', 'MOVE', 'WRITE', 'TRUNCATE', 'DELSUSTAINED'})
        read(self.dst / 'baseline-open', b'new inode')
        read(self.dst / 'right', b'renamed')
        read(self.dst / 'recovered', b'after unlink' + b'\0' * (8193 - 12))

    def test_continuous_writes(self):
        for worker in range(4):
            write(self.src / str(worker), b'baseline')
        self.bootstrap()
        self.start_replay()

        def worker(index):
            path = self.src / str(index)
            for seq in range(30):
                data = payload(index, seq, 8192)
                write(path, data)
                read(path, data)
                os.setxattr(path, 'user.sequence', str(seq).encode())
                temporary = self.src / f'tmp-{index}'
                write(temporary, data[:32])
                temporary.rename(self.src / f'last-{index}')

        with concurrent.futures.ThreadPoolExecutor(4) as pool:
            list(pool.map(worker, range(4)))
        self.finish({'CREATE', 'WRITE', 'MOVE', 'SETXATTR'})
        for worker in range(4):
            read(self.dst / str(worker), payload(worker, 29, 8192))

    def test_failed_operations(self):
        self.bootstrap()
        base = self.src
        a, b = base / 'a', base / 'b'
        write(a, b'A' * 8192)
        write(b, b'B' * 8192)
        (base / 'directory').mkdir()
        write(base / 'directory' / 'child', b'child')
        expect_errno(errno.ENOTEMPTY, os.rmdir, base / 'directory')
        expect_errno(errno.EINVAL, os.rename, base / 'directory', base / 'directory' / 'loop')
        expect_errno(errno.EEXIST, os.link, a, b)
        expect_errno(errno.ENOENT, os.unlink, base / 'absent')
        expect_errno(errno.EISDIR, os.unlink, base / 'directory')
        os.setxattr(a, 'user.edge', b'original', os.XATTR_CREATE)
        expect_errno(errno.EEXIST, os.setxattr, a, 'user.edge', b'bad', os.XATTR_CREATE)
        expect_errno(errno.ENODATA, os.setxattr, a, 'user.absent', b'bad', os.XATTR_REPLACE)
        assert os.getxattr(a, 'user.edge') == b'original'
        os.removexattr(a, 'user.edge')
        expect_errno(errno.ENODATA, os.removexattr, a, 'user.edge')
        libc = ctypes.CDLL(None, use_errno=True)
        libc.renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        assert libc.renameat2(-100, os.fsencode(a), -100, os.fsencode(b), 1) == -1
        assert ctypes.get_errno() == errno.EEXIST
        libc.fallocate.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_longlong, ctypes.c_longlong]
        with open(a, 'r+b', buffering=0) as f:
            expect_errno(errno.EINVAL, os.copy_file_range, f.fileno(), f.fileno(), 4096, 0, 1)
            expect_errno(errno.EINVAL, os.ftruncate, f.fileno(), -1)
            assert libc.fallocate(f.fileno(), 0, 0, 0) == -1
            assert ctypes.get_errno() == errno.EINVAL
            assert libc.fallocate(f.fileno(), 8, 0, 4096) == -1
            assert ctypes.get_errno() == errno.EOPNOTSUPP
        read(a, b'A' * 8192)
        read(b, b'B' * 8192)
        os.sync()
        self.finish()
        read(self.dst / 'a', b'A' * 8192)
        read(self.dst / 'b', b'B' * 8192)

    def test_trash(self):
        self.jfs('config', self.src_meta, '--trash-days', '1')
        self.include_trash = True
        self.bootstrap()
        base = self.src
        for name in ['closed', 'held', 'overwrite', 'replacement']:
            write(base / name, name.encode())
        (base / 'closed').unlink()
        with open(base / 'held', 'r+b', buffering=0) as held:
            (base / 'held').unlink()
            held.write(b'written after moving to trash')
            os.fsync(held.fileno())
        os.replace(base / 'replacement', base / 'overwrite')
        batch = base / 'batch'
        batch.mkdir()
        write(batch / 'file', b'batch trash')
        os.link(batch / 'file', base / 'hardlink')
        os.symlink('file', batch / 'symlink')
        self.jfs('rmr', batch)
        (base / 'empty').mkdir()
        (base / 'empty').rmdir()
        assert list((self.src_mount / '.trash').iterdir()), 'nothing was retained in trash'
        os.sync()
        self.finish({'UNLINK', 'UNLINKBATCH', 'MOVE', 'RMDIR'})
        self.assertTrue(list((self.dst_mount / '.trash').iterdir()))

#     def test_directory_stats(self):
#         self.bootstrap()
#         write(self.src / 'file', b'directory statistics')
#         self.unmount(self.src_mount)
#         self.jfs('fsck', self.src_meta, '--path', '/case', '--repair', '--sync-dir-stat')
#         self.finish({'DIRSTAT'})

    def test_empty_xattrs(self):
        if self.source_empty_xattrs and not self.destination_empty_xattrs:
            self.skipTest('destination does not support the source empty-xattr capability')
        self.bootstrap()
        path = self.src / 'xattrs'
        write(path, b'xattrs')
        if self.source_empty_xattrs:
            os.setxattr(path, 'user.empty', b'', os.XATTR_CREATE)
            os.setxattr(path, 'user.empty', b'nonempty', os.XATTR_REPLACE)
            os.setxattr(path, 'user.empty', b'', os.XATTR_REPLACE)
            expected = b''
        else:
            expect_errno(errno.EINVAL, os.setxattr, path, 'user.empty', b'', os.XATTR_CREATE)
            os.setxattr(path, 'user.empty', b'nonempty', os.XATTR_CREATE)
            expect_errno(errno.EINVAL, os.setxattr, path, 'user.empty', b'', os.XATTR_REPLACE)
            expected = b'nonempty'
        self.finish({'SETXATTR'})
        self.assertEqual(os.getxattr(self.dst / 'xattrs', 'user.empty'), expected)
