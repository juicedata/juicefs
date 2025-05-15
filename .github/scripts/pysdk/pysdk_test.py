import errno
import fractions
import shutil
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

    def tearDown(self):
        v.rmr(TESTFN)

    def test_summary(self):
        res = v.summary(TESTFILE, depth=258, entries=2)
        self.assertTrue(normalize(res)==normalize({"Path": "file", "Type": 2, "Files":1, "Dirs":0, "Size":4096}))
        res = v.summary(TESTFN)
        self.assertTrue(normalize(res)==normalize({"Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480}))
        res = v.summary(TESTFN, depth=257, entries=1)
        self.assertTrue(normalize(res)==normalize({"Path": "test", "Type": 2, "Files":2, "Dirs":3, "Size":20480, "Children":[
            {"Path": "dir1", "Type": 2, "Files":1, "Dirs":1, "Size":8192},{'Path': '...', 'Type': 1, 'Size': 8192, 'Files': 1, 'Dirs': 1}]}))
        res = v.summary(TESTFN, depth=258, entries=1)
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
        res = v.summary(TESTFN, depth=259, entries=4)
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

class QuotaTests(unittest.TestCase):
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

    def tearDown(self):
        v.rmr(TESTFN)

    def test_quota(self):
        # set quota
        v.set_quota(path=TESTFN, capacity=1024*1024*1024, inodes=1000, create=True)
        res = v.get_quota(path=TESTFN)
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}}))

        res = v.list_quota()
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}}))

        v.set_quota(path=TESTFN+"/dir1",  capacity=1024*1024*1024, inodes=10000, create=True, strict=True)
        res = v.list_quota()
        self.assertTrue(normalize(res)==normalize({"/test": {"MaxSpace": 1024*1024*1024, "MaxInodes": 1000, "UsedSpace": 0, "UsedInodes": 3}, "/test/dir1": {"MaxSpace": 1024*1024*1024, "MaxInodes": 10000, "UsedSpace": 4096, "UsedInodes": 1}}))

        # check quota
        v.check_quota(path=TESTFN, strict=True, repair=True)

        # unset quota
        v.del_quota(path=TESTFN)
        res = v.get_quota(path=TESTFN)
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

        v.setxattr(fn, s("user.test"), b"a", **kwargs)
        xattr = set(init_xattr)
        xattr.add("user.test")
        self.assertEqual(set(v.listxattr(fn)), xattr)
        self.assertEqual(v.getxattr(fn, b"user.test", **kwargs), b"a")
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


class BenchTests(unittest.TestCase):
    def setUp(self):
        if not v.exists(TESTFN):
            v.mkdir(TESTFN)
        self.test_file = TESTFILE + '_bench'
        self.block_size = 128 * 1024  # 128KB
        self.buffer_size = 300
        self.buffering = 2 * 1024 * 1024
        self.run_time = 30
        self.file_size = 100 * 1024 * 1024
        self.seed = 20
        self.count = 200

    def tearDown(self):
        if v.exists(self.test_file):
            v.unlink(self.test_file)

    def test_seq_write(self):
        print('test_seq_write')
        seq_write(
            filename=self.test_file,
            client=v,
            protocol='pysdk',
            block_size=self.block_size,
            buffering=self.buffering,
            run_time=self.run_time,
            file_size=self.file_size
        )
        self.assertTrue(v.exists(self.test_file))
        stat = v.stat(self.test_file)
        self.assertGreater(stat.st_size, 0)

    def test_random_write(self):
        print('test_random_write')
        random_write(
            filename=self.test_file,
            client=v,
            protocol='pysdk',
            buffering=self.buffering,
            block_size=self.block_size,
            run_time=self.run_time,
            file_size=self.file_size,
            seed=self.seed
        )
        self.assertTrue(v.exists(self.test_file))
        stat = v.stat(self.test_file)
        self.assertGreater(stat.st_size, 0)

    def test_seq_read(self):
        print('test_seq_read')
        with v.open(self.test_file, 'wb') as f:
            f.write(os.urandom(self.file_size))

        seq_read(
            filename=self.test_file,
            client=v,
            protocol='pysdk',
            block_size=self.block_size,
            buffering=self.buffering
        )

    def test_random_read(self):
        print('test_random_read')
        with v.open(self.test_file, 'wb') as f:
            f.write(os.urandom(self.file_size))

        random_read(
            filename=self.test_file,
            client=v,
            protocol='pysdk',
            buffering=self.buffering,
            block_size=self.block_size,
            seed=self.seed,
            count=self.count
        )

class ClientParamsTests(unittest.TestCase):
    def setUp(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)
        v.mkdir(TESTFN)
        self.testfile = TESTFN + '/testfile'
    
    def tearDown(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)

    def test_readonly_param(self):
        readonly_client = juicefs.Client(
            "test-volume-ro",
            meta=meta_url,
            read_only=True
        )
        with self.assertRaises(OSError):
            readonly_client.open(self.testfile, 'w')

    def test_cache_params(self):
        cache_client = juicefs.Client(
            "test-volume-cache",
            meta=meta_url,
            cache_dir="/tmp/jfs_test_cache",
            cache_size="100M",
            cache_partial_only=False
        )
        
        size_mb = 48
        test_data = os.urandom(size_mb * 1024 * 1024) 
        with cache_client.open(self.testfile, 'wb') as f:
            f.write(test_data)
        
        with cache_client.open(self.testfile, 'rb') as f:
            read_data = f.read()
        self.assertEqual(read_data, test_data)

        cache_dir = "/tmp/jfs_test_cache"
        cache_size = 0
        for root, dirs, files in os.walk(cache_dir):
            for file in files:
                cache_size += os.path.getsize(os.path.join(root, file))
        self.assertGreaterEqual(cache_size, size_mb * 1024 * 1024)

    def test_io_limits(self):
        limited_client = juicefs.Client(
            "test-volume-limited",
            meta=meta_url,
            upload_limit="1M",
            download_limit="1M"
        )
        
        test_data = b"x" * (10 * 1024 * 1024)  # 10MB
        start_time = time.time()
        with limited_client.open(self.testfile, 'wb') as f:
            f.write(test_data)
        write_time = time.time() - start_time
        
        self.assertGreaterEqual(write_time, 10.0)

class CloneTests(unittest.TestCase):
    def setUp(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)
        v.mkdir(TESTFN)
        self.source = TESTFN + '/source'
        self.target = TESTFN + '/target'
        self.test_data = b"Hello JuiceFS!" * 1024
        
        with v.open(self.source, 'wb') as f:
            f.write(self.test_data)
    
    def tearDown(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)

    def test_basic_clone(self):
        v.clone(self.source, self.target)
        
        self.assertTrue(v.exists(self.target))
        
        with v.open(self.target, 'rb') as f:
            cloned_data = f.read()
        self.assertEqual(cloned_data, self.test_data)
        
        source_stat = v.stat(self.source)
        target_stat = v.stat(self.target)
        self.assertEqual(source_stat.st_size, target_stat.st_size)

    def test_clone_with_preserve(self):
        v.chmod(self.source, 0o644)
        
        v.clone(self.source, self.target, preserve=True)        
        source_stat = v.stat(self.source)
        target_stat = v.stat(self.target)
        self.assertEqual(source_stat.st_mode, target_stat.st_mode)

class WarmupTests(unittest.TestCase):
    @classmethod
    def setUpClass(self):
        self.warmup_client = juicefs.Client(
            "test-warmup",
            meta=meta_url,
            cache_dir="/tmp/jfs_test_warmup",
            cache_size="1000M",
            cache_partial_only=True
        )
        if self.warmup_client.exists(TESTFN):
            self.warmup_client.rmr(TESTFN)
        self.warmup_client.mkdir(TESTFN)
        self.test_files = [
            TESTFN + '/file1',
            TESTFN + '/file2'
        ]
        size_mb = 50
        test_data = os.urandom(size_mb * 1024 * 1024)
        for file in self.test_files:
            with self.warmup_client.open(file, 'wb') as f:
                f.write(test_data)
    
    @classmethod
    def tearDownClass(self):
        if self.warmup_client.exists(TESTFN):
            self.warmup_client.warmup(self.test_files, isEvict=True)
            self.warmup_client.rmr(TESTFN)

    def test_basic_warmup(self): 
        result = self.warmup_client.warmup(self.test_files, numthreads=4)
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
        self.warmup_client.warmup(self.test_files)
        result = self.warmup_client.warmup(self.test_files, isCheck=True)
        self.assertEqual(result['MissBytes'], 0)
        self.assertTrue(any('jfs_test_warmup' in path for path in result['Locations']),
                       msg=f"'jfs_test_warmup' not found in {result['Locations']}")

    def test_warmup_evict(self):
        self.warmup_client.warmup(self.test_files)
        result = self.warmup_client.warmup(self.test_files, isEvict=True)
        time.sleep(2)
        cache_dir = "/tmp/jfs_test_warmup"
        size_mb = 1
        cache_size = 0
        for root, dirs, files in os.walk(cache_dir):
            for file in files:
                cache_size += os.path.getsize(os.path.join(root, file))
        self.assertLessEqual(cache_size, size_mb * 1024 * 1024)
        result = self.warmup_client.warmup(self.test_files, isCheck=True)
        self.assertEqual(result['MissBytes'], result['TotalBytes'])

class InfoTests(unittest.TestCase):
    def setUp(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)
        v.mkdir(TESTFN)
        
        self.test_dir = TESTFN + '/infotest'
        self.test_file = self.test_dir + '/testfile'
        v.makedirs(self.test_dir)
        with v.open(self.test_file, 'w') as f:
            f.write("test content")
    
    def tearDown(self):
        if v.exists(TESTFN):
            v.rmr(TESTFN)

    def test_file_info(self):
        info = v.info(self.test_dir,recursive=True,strict=True)
        self.assertIn('Length', info)
        self.assertEqual(info['Files'], 1)
        self.assertEqual(info['Dirs'], 1)

if __name__ == '__main__':
    unittest.main()
