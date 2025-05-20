import errno
import fractions
import unittest
import os
import pwd
from os.path import dirname
import sys
import time
sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs
from bench import seq_write, random_write, seq_read, random_read

TESTFN='/test'
TESTFILE='/test/file'
os.makedirs('/tmp/jfsCache0', exist_ok=True)
meta_url=os.environ.get('META_URL', 'redis://localhost')


class FileTests(unittest.TestCase):
    def setUp(self):
        self.v = juicefs.Client("test-volume", meta=meta_url, access_log="/tmp/access.log")
        if not self.v.exists(TESTFN):
            self.v.mkdir(TESTFN)

    def tearDown(self):
        self.v.rmr(TESTFN)

    def create_file(self, filename, content=b'content'):
        with self.v.open(filename, "xb", 0) as fp:
            fp.write(content)

    def test_read(self):
        with self.v.open(TESTFILE, "w+b") as fobj:
            fobj.write(b"spam")
            fobj.flush()
            fd = fobj.fileno()
            fobj.seek(0,0)
            s = fobj.read(4)
            self.assertEqual(type(s), bytes)
            self.assertEqual(s, b"spam")

    def test_write(self):
        fd = self.v.open(TESTFILE, 'wb')
        self.assertRaises(TypeError, os.write, fd, "beans")
        fd.write(b"bacon\n")
        fd.close()
        with self.v.open(TESTFILE, "rb") as fobj:
            self.assertEqual(fobj.read().splitlines(), [b"bacon"])


class UtimeTests(FileTests):
    def setUp(self):
        super().setUp()
        self.fname = os.path.join(TESTFN, "f1")
        if not self.v.exists(self.fname):
            self.create_file(self.fname)

    def _test_utime(self, set_time, filename=None):
        if not filename:
            filename = self.fname
        atime = 1.0   # 1.0 seconds
        mtime = 4.0   # 4.0 seconds
        set_time(filename, (atime, mtime))
        st = self.v.stat(filename)
        self.assertEqual(st.st_atime, atime)
        self.assertEqual(st.st_mtime, mtime)

    def test_utime(self):
        def set_time(filename, times):
            self.v.utime(filename, times)
        self._test_utime(set_time)

    def test_utime_by_times(self):
        self.test_utime()


class MakedirTests(FileTests):
    def test_makedir(self):
        base = TESTFN
        path = os.path.join(base, 'dir1', 'dir2', 'dir3')
        self.v.makedirs(path)             # Should work
        path = os.path.join(base, 'dir1', 'dir2', 'dir3', 'dir4')
        self.v.makedirs(path)
        self.assertRaises(OSError, self.v.makedirs, os.curdir)
        path = os.path.join(base, 'dir1', 'dir2', 'dir3', 'dir4', 'dir5', os.curdir)
        path = os.path.join(base, 'dir1', os.curdir, 'dir2', 'dir3', 'dir4',
                            'dir5', 'dir6')
        self.v.makedirs(path)


class ChownFileTests(FileTests):
    def test_chown_uid_gid_arguments_must_be_index(self):
        stat = self.v.stat(TESTFN)
        uid = stat.st_uid
        gid = stat.st_gid
        for value in (-1.0, -1j, fractions.Fraction(-2, 2)):
            self.assertRaises(TypeError, self.v.chown, TESTFN, value, gid)
            self.assertRaises(TypeError, self.v.chown, TESTFN, uid, value)
        self.assertIsNone(self.v.chown(TESTFN, uid, gid))

    def test_chown_with_root(self):
        try:
            all_users = [u.pw_uid for u in pwd.getpwall()]
        except (AttributeError):
            all_users = []
        uid_1, uid_2 = all_users[:2]
        gid = self.v.stat(TESTFN).st_gid
        self.v.chown(TESTFN, uid_1, gid)
        uid = self.v.stat(TESTFN).st_uid
        self.assertEqual(uid, uid_1)
        self.v.chown(TESTFN, uid_2, gid)
        uid = self.v.stat(TESTFN).st_uid
        self.assertEqual(uid, uid_2)


class LinkTests(FileTests):
    def setUp(self):
        super().setUp()
        self.file1 = os.path.join(TESTFN, "1")
        self.file2 = os.path.join(TESTFN, "2")

    def are_files_same(self, file1, file2):
        stat1 = self.v.lstat(file1)
        stat2 = self.v.lstat(file2)
        return stat1.st_ino  == stat2.st_ino and stat1.st_dev == stat2.st_dev

    def _test_link(self, file1, file2):
        self.create_file(file1)

        try:
            self.v.link(file1, file2)
        except PermissionError as e:
            self.skipTest('os.link(): %s' % e)
        self.assertTrue(self.are_files_same(file1, file2))

    def test_link(self):
        self._test_link(self.file1, self.file2)


class SummaryTests(FileTests):
    # /test/dir1/file
    #      /dir2
    #      /file
    def setUp(self):
        super().setUp()
        self.create_file(TESTFILE)
        self.v.mkdir(TESTFN + '/dir1')
        self.create_file(TESTFN + '/dir1/file')
        self.v.mkdir(TESTFN + '/dir2')

    def test_summary(self):
        res = self.v.summary(TESTFILE, depth=258, entries=2)
        self.assertTrue(normalize(res)==normalize({"Path": "file", "Type": 2, "Files":1, "Dirs":0, "Size":4096}))
        res = self.v.summary(TESTFN)
        self.assertTrue(normalize(res)==normalize({"Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480}))
        res = self.v.summary(TESTFN, depth=257, entries=1)
        self.assertTrue(normalize(res)==normalize({"Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480, "Children":[
            {"Path": "dir1", "Type": 2, "Files":1, "Dirs":1, "Size":8192},{'Path': '...', 'Type': 1, 'Size': 8192, 'Files': 1, 'Dirs': 1}]}))
        res = self.v.summary(TESTFN, depth=258, entries=1)
        self.assertTrue(normalize(res)==normalize(
            {
                "Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480, "Children":
                [
                    {"Path": "dir1", "Type": 2, "Files":1, "Dirs":1, "Size":8192, "Children": [
                        {"Path": "dir1/file", "Type": 1, "Size": 4096, "Files": 1, "Dirs": 0}
                    ]
                     },{'Path': '...', 'Type': 1, 'Size': 8192, 'Files': 1, 'Dirs': 1}
                ]}
        ))
        res = self.v.summary(TESTFN, depth=259, entries=4)
        self.assertTrue(normalize(res)==normalize(
            {
                "Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480, "Children":
                [
                    {
                        "Path": "dir1", "Type": 2, "Files":1, "Dirs":1, "Size":8192, "Children":
                        [{"Path": "dir1/file", "Type": 1, "Size": 4096, "Files": 1, "Dirs": 0}]
                    },{
                    'Path': 'file', 'Type': 1, 'Size': 4096, 'Files': 1, 'Dirs': 0
                },{
                    'Path': 'dir2', 'Type': 2, 'Size': 4096, 'Files': 0, 'Dirs': 1
                }
                ]}
        ))


class QuotaTests(FileTests):
    def test_quota(self):
        # /test/dir1/file
        #      /dir2
        #      /file
        self.create_file(TESTFILE)
        self.v.mkdir(TESTFN + '/dir1')
        self.create_file(TESTFN + '/dir1/file')
        self.v.mkdir(TESTFN + '/dir2')

        # set quota
        self.v.set_quota(path=TESTFN, capacity=1024*1024*1024, inodes=1000, create=True)
        res = self.v.get_quota(path=TESTFN)
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}}))

        res = self.v.list_quota()
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}}))

        self.v.set_quota(path=TESTFN+"/dir1",  capacity=1024*1024*1024, inodes=10000, create=True, strict=True)
        res = self.v.list_quota()
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}, "/test/dir1": {"MaxSpace": 1024*1024*1024, "MaxInodes": 10000, "UsedSpace": 4096, "UsedInodes": 1}}))

        # check quota
        self.v.check_quota(path=TESTFN, strict=True, repair=True)

        # unset quota
        self.v.del_quota(path=TESTFN)
        res = self.v.get_quota(path=TESTFN)
        self.assertTrue(res=={})


def normalize(d):
    if isinstance(d, dict):
        if "Children" in d:
            d["Children"].sort(key=lambda x: x["Path"])
        return {k: normalize(v) for k, v in d.items()}
    elif isinstance(d, list):
        return sorted((normalize(x) for x in d), key=lambda x: x.get("Path", ""))
    else:
        return d


class NonLocalSymlinkTests(FileTests):
    def test_directory_link_nonlocal(self):
        src = os.path.join(TESTFN, 'some_link')
        self.v.symlink('/some_dir', src)
        assert self.v.readlink(src) == '../some_dir'


class ExtendedAttributeTests(FileTests):
    def _check_xattrs_str(self, s, getxattr, setxattr, removexattr, listxattr, **kwargs):
        fn = TESTFN + '_xattr'
        if self.v.exists(fn):
            self.v.unlink(fn)
        self.create_file(fn)

        #        with self.assertRaises(OSError) as cm:
        #            self.v.getxattr(fn, s("user.test"), **kwargs)
        #        self.assertEqual(cm.exception.errno, errno.ENODATA)

        init_xattr = self.v.listxattr(fn)
        self.assertIsInstance(init_xattr, list)

        self.v.setxattr(fn, s("user.test"), b"a", **kwargs)
        xattr = set(init_xattr)
        xattr.add("user.test")
        self.assertEqual(set(self.v.listxattr(fn)), xattr)
        self.assertEqual(self.v.getxattr(fn, b"user.test", **kwargs), b"a")
        self.v.setxattr(fn, s("user.test"), b"hello", os.XATTR_REPLACE, **kwargs)
        self.assertEqual(self.v.getxattr(fn, b"user.test", **kwargs), b"hello")

        with self.assertRaises(OSError) as cm:
            self.v.setxattr(fn, s("user.test"), b"bye", os.XATTR_CREATE, **kwargs)
        self.assertEqual(cm.exception.errno, errno.EEXIST)

        #        with self.assertRaises(OSError) as cm:
        #            self.v.setxattr(fn, s("user.test2"), b"bye", os.XATTR_REPLACE, **kwargs)
        #        self.assertEqual(cm.exception.errno, errno.ENODATA)

        self.v.setxattr(fn, s("user.test2"), b"foo", os.XATTR_CREATE, **kwargs)
        xattr.add("user.test2")
        self.assertEqual(set(self.v.listxattr(fn)), xattr)
        self.v.removexattr(fn, s("user.test"), **kwargs)

        with self.assertRaises(OSError) as cm:
            self.v.getxattr(fn, s("user.test"), **kwargs)
        self.assertEqual(cm.exception.errno, errno.ENODATA)

        xattr.remove("user.test")
        self.assertEqual(set(self.v.listxattr(fn)), xattr)
        self.assertEqual(self.v.getxattr(fn, s("user.test2"), **kwargs), b"foo")
        self.v.setxattr(fn, s("user.test"), b"a"*1024, **kwargs)
        self.assertEqual(self.v.getxattr(fn, s("user.test"), **kwargs), b"a"*1024)
        self.v.removexattr(fn, s("user.test"), **kwargs)
        many = sorted("user.test{}".format(i) for i in range(100))
        for thing in many:
            self.v.setxattr(fn, thing, b"x", **kwargs)
        self.assertEqual(set(self.v.listxattr(fn)), set(init_xattr) | set(many))

    def _check_xattrs(self, *args, **kwargs):
        self._check_xattrs_str(str, *args, **kwargs)
        self.v.unlink(TESTFN + '_xattr')

        self._check_xattrs_str(os.fsencode, *args, **kwargs)
        self.v.unlink(TESTFN + '_xattr')

    def test_simple(self):
        self._check_xattrs(self.v.getxattr, self.v.setxattr, self.v.removexattr,
                           self.v.listxattr)

    def test_fds(self):
        def getxattr(path, *args):
            with self.v.open(path, "rb") as fp:
                return self.v.getxattr(fp.fileno(), *args)
        def setxattr(path, *args):
            with self.v.open(path, "wb", 0) as fp:
                self.v.setxattr(fp.fileno(), *args)
        def removexattr(path, *args):
            with self.v.open(path, "wb", 0) as fp:
                self.v.removexattr(fp.fileno(), *args)
        def listxattr(path, *args):
            with self.v.open(path, "rb") as fp:
                return self.v.listxattr(fp.fileno(), *args)
        self._check_xattrs(getxattr, setxattr, removexattr, listxattr)


class BenchTests(FileTests):
    test_file = TESTFILE + '_bench'
    block_size = 128 * 1024  # 128KB
    buffer_size = 300
    buffering = 2 * 1024 * 1024
    run_time = 30
    file_size = 100 * 1024 * 1024
    seed = 20
    count = 200

    def test_seq_write(self):
        print('test_seq_write')
        seq_write(
            filename=self.test_file,
            client=self.v,
            protocol='pysdk',
            block_size=self.block_size,
            buffering=self.buffering,
            run_time=self.run_time,
            file_size=self.file_size
        )
        self.assertTrue(self.v.exists(self.test_file))
        stat = self.v.stat(self.test_file)
        self.assertGreater(stat.st_size, 0)

    def test_random_write(self):
        print('test_random_write')
        random_write(
            filename=self.test_file,
            client=self.v,
            protocol='pysdk',
            buffering=self.buffering,
            block_size=self.block_size,
            run_time=self.run_time,
            file_size=self.file_size,
            seed=self.seed
        )
        self.assertTrue(self.v.exists(self.test_file))
        stat = self.v.stat(self.test_file)
        self.assertGreater(stat.st_size, 0)

    def test_seq_read(self):
        print('test_seq_read')
        with self.v.open(self.test_file, 'wb') as f:
            f.write(os.urandom(self.file_size))

        seq_read(
            filename=self.test_file,
            client=self.v,
            protocol='pysdk',
            block_size=self.block_size,
            buffering=self.buffering
        )

    def test_random_read(self):
        print('test_random_read')
        with self.v.open(self.test_file, 'wb') as f:
            f.write(os.urandom(self.file_size))

        random_read(
            filename=self.test_file,
            client=self.v,
            protocol='pysdk',
            buffering=self.buffering,
            block_size=self.block_size,
            seed=self.seed,
            count=self.count
        )


class ClientParamsTests(FileTests):
    testfile = TESTFN + '/testfile'

    def test_readonly_param(self):
        v = juicefs.Client(
            "test-volume-ro",
            meta=meta_url,
            read_only=True
        )
        with self.assertRaises(OSError):
            v.open(self.testfile, 'w')

    def test_cache_params(self):
        v = juicefs.Client(
            "test-volume-cache",
            meta=meta_url,
            cache_dir="/tmp/jfs_test_cache",
            cache_size="100M",
            cache_partial_only=False
        )

        size_mb = 48
        test_data = os.urandom(size_mb * 1024 * 1024)
        with v.open(self.testfile, 'wb') as f:
            f.write(test_data)

        with v.open(self.testfile, 'rb') as f:
            read_data = f.read()
        self.assertEqual(read_data, test_data)

        cache_dir = "/tmp/jfs_test_cache"
        cache_size = 0
        for root, dirs, files in os.walk(cache_dir):
            for file in files:
                cache_size += os.path.getsize(os.path.join(root, file))
        self.assertGreaterEqual(cache_size, size_mb * 1024 * 1024/2)

    def test_io_limits(self):
        v = juicefs.Client(
            "test-volume-limited",
            meta=meta_url,
            upload_limit="1M",
            download_limit="1M"
        )

        test_data = b"x" * (10 * 1024 * 1024)  # 10MB
        start_time = time.time()
        with v.open(self.testfile, 'wb') as f:
            f.write(test_data)
        write_time = time.time() - start_time

        self.assertGreaterEqual(write_time, 10.0)


class CloneTests(FileTests):
    def setUp(self):
        super().setUp()
        self.source = TESTFN + '/source'
        self.target = TESTFN + '/target'
        self.test_data = b"Hello JuiceFS!" * 1024

        with self.v.open(self.source, 'wb') as f:
            f.write(self.test_data)

    def test_basic_clone(self):
        self.v.clone(self.source, self.target)

        self.assertTrue(self.v.exists(self.target))

        with self.v.open(self.target, 'rb') as f:
            cloned_data = f.read()
        self.assertEqual(cloned_data, self.test_data)

        source_stat = self.v.stat(self.source)
        target_stat = self.v.stat(self.target)
        self.assertEqual(source_stat.st_size, target_stat.st_size)

    def test_clone_with_preserve(self):
        self.v.chmod(self.source, 0o644)

        self.v.clone(self.source, self.target, preserve=True)
        source_stat = self.v.stat(self.source)
        target_stat = self.v.stat(self.target)
        self.assertEqual(source_stat.st_mode, target_stat.st_mode)


class WarmupTests(unittest.TestCase):
    @classmethod
    def setUpClass(self):
        self.v = juicefs.Client(
            "test-warmup",
            meta=meta_url,
            cache_dir="/tmp/jfs_test_warmup",
            cache_size="1000M",
            cache_partial_only=True
        )
        if self.v.exists(TESTFN):
            self.v.rmr(TESTFN)
        self.v.mkdir(TESTFN)
        self.test_files = [
            TESTFN + '/file1',
            TESTFN + '/file2'
        ]
        size_mb = 50
        test_data = os.urandom(size_mb * 1024 * 1024)
        for file in self.test_files:
            with self.v.open(file, 'wb') as f:
                f.write(test_data)

    @classmethod
    def tearDownClass(self):
        if self.v.exists(TESTFN):
            self.v.warmup(self.test_files, isEvict=True)
            self.v.rmr(TESTFN)

    def test_basic_warmup(self):
        result = self.v.warmup(self.test_files, numthreads=4)
        self.assertIn('FileCount', result)
        self.assertEqual(result['FileCount'], 2)
        self.assertIn('SliceCount', result)
        self.assertIn('TotalBytes', result)
        self.assertIn('MissBytes', result)
        #        self.assertIn('Locations', result)
        cache_dir = "/tmp/jfs_test_warmup"
        size_mb = 100
        cache_size = 0
        time.sleep(2)
        for root, dirs, files in os.walk(cache_dir):
            for file in files:
                cache_size += os.path.getsize(os.path.join(root, file))
        self.assertGreaterEqual(cache_size, size_mb * 1024 * 1024)

    def test_warmup_check(self):
        self.v.warmup(self.test_files)
        result = self.v.warmup(self.test_files, isCheck=True)
        self.assertEqual(result['MissBytes'], 0)
        self.assertTrue(any('jfs_test_warmup' in path for path in result['Locations']),
                        msg=f"'jfs_test_warmup' not found in {result['Locations']}")

    def test_warmup_evict(self):
        self.v.warmup(self.test_files)
        result = self.v.warmup(self.test_files, isEvict=True)
        time.sleep(2)
        cache_dir = "/tmp/jfs_test_warmup"
        size_mb = 1
        cache_size = 0
        for root, dirs, files in os.walk(cache_dir):
            for file in files:
                cache_size += os.path.getsize(os.path.join(root, file))
        self.assertLessEqual(cache_size, size_mb * 1024 * 1024)
        result = self.v.warmup(self.test_files, isCheck=True)
        self.assertEqual(result['MissBytes'], result['TotalBytes'])


class InfoTests(FileTests):
    def test_file_info(self):
        self.test_dir = TESTFN + '/infotest'
        self.test_file = self.test_dir + '/testfile'
        self.v.makedirs(self.test_dir)
        with self.v.open(self.test_file, 'w') as f:
            f.write("test content")

        info = self.v.info(self.test_dir,recursive=True,strict=True)
        self.assertIn('Length', info)
        self.assertEqual(info['Files'], 1)
        self.assertEqual(info['Dirs'], 1)


if __name__ == '__main__':
    unittest.main()
