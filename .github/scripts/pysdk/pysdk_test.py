import errno
import fractions
import shutil
import unittest
import os
import pwd
from os.path import dirname
import sys
sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs

TESTFN='/test'
TESTFILE='/test/file'
os.makedirs('/tmp/jfsCache0', exist_ok=True)
meta_url=os.environ.get('META_URL', 'redis://localhost')
v = juicefs.Client("test-volume", meta=meta_url, access_log="/tmp/access.log")

def create_file(filename, content=b'content'):
    with v.open(filename, "xb", 0) as fp:
        fp.write(content)

class FileTests(unittest.TestCase):
    def setUp(self):
        if not v.exists(TESTFN):
           v.mkdir(TESTFN)

    def test_read(self):
        with v.open(TESTFILE, "w+b") as fobj:
            fobj.write(b"spam")
            fobj.flush()
            fd = fobj.fileno()
            fobj.seek(0,0)
            s = fobj.read(4)
            self.assertEqual(type(s), bytes)
            self.assertEqual(s, b"spam")

    def test_write(self):
        fd = v.open(TESTFILE, 'wb')
        self.assertRaises(TypeError, os.write, fd, "beans")
        fd.write(b"bacon\n")
        fd.close()
        with v.open(TESTFILE, "rb") as fobj:
            self.assertEqual(fobj.read().splitlines(), [b"bacon"])

class UtimeTests(unittest.TestCase):
    def setUp(self):
        self.dirname = TESTFN
        self.fname = os.path.join(self.dirname, "f1")
        if not v.exists(TESTFN):
            v.mkdir(self.dirname)
        if not v.exists(self.fname):
            create_file(self.fname)

    def _test_utime(self, set_time, filename=None):
        if not filename:
            filename = self.fname
        atime = 1.0   # 1.0 seconds
        mtime = 4.0   # 4.0 seconds
        set_time(filename, (atime, mtime))
        st = v.stat(filename)
        self.assertEqual(st.st_atime, atime)
        self.assertEqual(st.st_mtime, mtime)

    def test_utime(self):
        def set_time(filename, times):
            v.utime(filename, times)
        self._test_utime(set_time)

    def test_utime_by_times(self):
        self.test_utime()


class MakedirTests(unittest.TestCase):
    def setUp(self):
        if v.exists(TESTFN):
           v.rmr(TESTFN)
        v.mkdir(TESTFN)

    def test_makedir(self):
        base = TESTFN
        path = os.path.join(base, 'dir1', 'dir2', 'dir3')
        v.makedirs(path)             # Should work
        path = os.path.join(base, 'dir1', 'dir2', 'dir3', 'dir4')
        v.makedirs(path)
        self.assertRaises(OSError, v.makedirs, os.curdir)
        path = os.path.join(base, 'dir1', 'dir2', 'dir3', 'dir4', 'dir5', os.curdir)
        path = os.path.join(base, 'dir1', os.curdir, 'dir2', 'dir3', 'dir4',
                            'dir5', 'dir6')
        v.makedirs(path)
        v.rmr(TESTFN)

    def tearDown(self):
        path = os.path.join(TESTFN, 'dir1', 'dir2', 'dir3',
                            'dir4', 'dir5', 'dir6')
        # If the tests failed, the bottom-most directory ('../dir6')
        # may not have been created, so we look for the outermost directory
        # that exists.
        if v.exists(path):
           v.rmr(path)

class ChownFileTests(unittest.TestCase):

    @classmethod
    def setUpClass(cls):
        if not v.exists(TESTFN):
           v.mkdir(TESTFN)

    def test_chown_uid_gid_arguments_must_be_index(self):
        stat = v.stat(TESTFN)
        uid = stat.st_uid
        gid = stat.st_gid
        for value in (-1.0, -1j, fractions.Fraction(-2, 2)):
            self.assertRaises(TypeError, v.chown, TESTFN, value, gid)
            self.assertRaises(TypeError, v.chown, TESTFN, uid, value)
        self.assertIsNone(v.chown(TESTFN, uid, gid))
        self.assertIsNone(v.chown(TESTFN, -1, -1))

    def test_chown_with_root(self):
        try:
            all_users = [u.pw_uid for u in pwd.getpwall()]
        except (AttributeError):
            all_users = []
        uid_1, uid_2 = all_users[:2]
        gid = v.stat(TESTFN).st_gid
        v.chown(TESTFN, uid_1, gid)
        uid = v.stat(TESTFN).st_uid
        self.assertEqual(uid, uid_1)
        v.chown(TESTFN, uid_2, gid)
        uid = v.stat(TESTFN).st_uid
        self.assertEqual(uid, uid_2)

class LinkTests(unittest.TestCase):
    def setUp(self):
        self.file1 = TESTFN + "1"
        self.file2 = os.path.join(TESTFN + "2")

    def tearDown(self):
        for file in (self.file1, self.file2):
            if v.exists(file):
                v.unlink(file)

    def are_files_same(self, file1, file2):
        stat1 = v.lstat(file1)
        stat2 = v.lstat(file2)
        return stat1.st_ino  == stat2.st_ino and stat1.st_dev == stat2.st_dev

    def _test_link(self, file1, file2):
        create_file(file1)

        try:
            v.link(file1, file2)
        except PermissionError as e:
            self.skipTest('os.link(): %s' % e)
        self.assertTrue(self.are_files_same(file1, file2))

    def test_link(self):
        self._test_link(self.file1, self.file2)

@unittest.skip("Skipping SummaryTests")
class SummaryTests(unittest.TestCase):
    # /test/dir1/file
    #      /dir2
    #      /file
    def setUp(self):
        if not v.exists(TESTFN):
            v.mkdir(TESTFN)
        create_file(TESTFILE)
        v.mkdir(TESTFN + '/dir1')
        create_file(TESTFN + '/dir1/file')
        v.mkdir(TESTFN + '/dir2')

    def test_summary(self):
        res = v.summary(TESTFILE, depth=258, entries=2)
        self.assertTrue(res=={"Length":7, "Files":1, "Dirs":0, "Size":4096})
        res = v.summary(TESTFN)
        self.assertTrue(res=={"Length":14, "Files":2, "Dirs":3, "Size":20480, "Entries": None})
        res = v.summary(TESTFN, depth=257, entries=1)
        self.assertTrue(res=={"Length":14, "Files":2, "Dirs":3, "Size":20480, "Entries":
            {"dir1":{"Length":7, "Files":1, "Dirs":1, "Size":8192, "Entries": None}}})
        res = v.summary(TESTFN, depth=258, entries=1)
        self.assertTrue(res=={"Length":14, "Files":2, "Dirs":3, "Size":20480, "Entries":
            {"dir1":{"Length":7, "Files":1, "Dirs":1, "Size":8192, "Entries": {
                "file": {"Length": 7, "Size": 4096, "Files": 1, "Dirs": 0}
            }}}})
        res = v.summary(TESTFN, depth=259, entries=4)
        self.assertTrue(res=={"Length":14, "Files":2, "Dirs":3, "Size":20480, "Entries":
            {
                "dir1":{"Length":7, "Files":1, "Dirs":1, "Size":8192, "Entries": {
                    "file": {"Length": 7, "Size": 4096, "Files": 1, "Dirs": 0}
                    }},
                "dir2": {"Length": 0, "Size": 4096, "Files": 0, "Dirs": 1, "Entries": {}},
                "file": {"Length": 7, "Size": 4096, "Files": 1, "Dirs": 0}
            }})
        
class NonLocalSymlinkTests(unittest.TestCase):
    def setUp(self):
        r"""
        Create this structure:
        base
         \___ some_dir
        """
        v.makedirs('base/some_dir')

    def tearDown(self):
        v.rmr('base')

    def test_directory_link_nonlocal(self):
        src = os.path.join('base', 'some_link')
        v.symlink('some_dir', src)
        assert v.readlink(src) == '../some_dir'

class ExtendedAttributeTests(unittest.TestCase):
#    def tearDown(self):
#        if v.exists(TESTFN + "_xattr"):
#            v.rmr(TESTFN + '_xattr')

    def _check_xattrs_str(self, s, getxattr, setxattr, removexattr, listxattr, **kwargs):
        fn = TESTFN + '_xattr'
        if v.exists(fn):
            v.unlink(fn)
        create_file(fn)

#        with self.assertRaises(OSError) as cm:
#            v.getxattr(fn, s("user.test"), **kwargs)
#        self.assertEqual(cm.exception.errno, errno.ENODATA)

        init_xattr = v.listxattr(fn)
        self.assertIsInstance(init_xattr, list)

        v.setxattr(fn, s("user.test"), b"", **kwargs)
        xattr = set(init_xattr)
        xattr.add("user.test")
        self.assertEqual(set(v.listxattr(fn)), xattr)
        self.assertEqual(v.getxattr(fn, b"user.test", **kwargs), b"")
        v.setxattr(fn, s("user.test"), b"hello", os.XATTR_REPLACE, **kwargs)
        self.assertEqual(v.getxattr(fn, b"user.test", **kwargs), b"hello")

        with self.assertRaises(OSError) as cm:
            v.setxattr(fn, s("user.test"), b"bye", os.XATTR_CREATE, **kwargs)
        self.assertEqual(cm.exception.errno, errno.EEXIST)

#        with self.assertRaises(OSError) as cm:
#            v.setxattr(fn, s("user.test2"), b"bye", os.XATTR_REPLACE, **kwargs)
#        self.assertEqual(cm.exception.errno, errno.ENODATA)

        v.setxattr(fn, s("user.test2"), b"foo", os.XATTR_CREATE, **kwargs)
        xattr.add("user.test2")
        self.assertEqual(set(v.listxattr(fn)), xattr)
        v.removexattr(fn, s("user.test"), **kwargs)

        with self.assertRaises(OSError) as cm:
            v.getxattr(fn, s("user.test"), **kwargs)
        self.assertEqual(cm.exception.errno, errno.ENODATA)

        xattr.remove("user.test")
        self.assertEqual(set(v.listxattr(fn)), xattr)
        self.assertEqual(v.getxattr(fn, s("user.test2"), **kwargs), b"foo")
        v.setxattr(fn, s("user.test"), b"a"*1024, **kwargs)
        self.assertEqual(v.getxattr(fn, s("user.test"), **kwargs), b"a"*1024)
        v.removexattr(fn, s("user.test"), **kwargs)
        many = sorted("user.test{}".format(i) for i in range(100))
        for thing in many:
            v.setxattr(fn, thing, b"x", **kwargs)
        self.assertEqual(set(v.listxattr(fn)), set(init_xattr) | set(many))


    def _check_xattrs(self, *args, **kwargs):
        self._check_xattrs_str(str, *args, **kwargs)
        v.unlink(TESTFN + '_xattr')

        self._check_xattrs_str(os.fsencode, *args, **kwargs)
        v.unlink(TESTFN + '_xattr')

    def test_simple(self):
        self._check_xattrs(v.getxattr, v.setxattr, v.removexattr,
                           v.listxattr)


    def test_fds(self):
        def getxattr(path, *args):
            with v.open(path, "rb") as fp:
                return v.getxattr(fp.fileno(), *args)
        def setxattr(path, *args):
            with v.open(path, "wb", 0) as fp:
                v.setxattr(fp.fileno(), *args)
        def removexattr(path, *args):
            with v.open(path, "wb", 0) as fp:
                v.removexattr(fp.fileno(), *args)
        def listxattr(path, *args):
            with v.open(path, "rb") as fp:
                return v.listxattr(fp.fileno(), *args)
        self._check_xattrs(getxattr, setxattr, removexattr, listxattr)

if __name__ == "__main__":
    unittest.main()
