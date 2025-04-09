import os
import sys
import time
import shutil
import random
import string
import threading
import unittest
from pathlib import Path

class WindowsFSTest(unittest.TestCase):
    def setUp(self):
        self.test_dir = "Z:\\test_fs"
        self.ensure_clean_dir(self.test_dir)
        
    def tearDown(self):
        if os.path.exists(self.test_dir):
            shutil.rmtree(self.test_dir, ignore_errors=True)
    
    def ensure_clean_dir(self, path):
        if os.path.exists(path):
            shutil.rmtree(path, ignore_errors=True)
        os.makedirs(path)
    
    def random_string(self, length=10):
        return ''.join(random.choices(string.ascii_letters + string.digits, k=length))
    
    def test_basic_operations(self):
        test_file = os.path.join(self.test_dir, "test.txt")
        content = "Hello, Windows!"
        with open(test_file, 'w') as f:
            f.write(content)
        
        with open(test_file, 'r') as f:
            self.assertEqual(f.read(), content)
        
        new_file = os.path.join(self.test_dir, "new.txt")
        os.rename(test_file, new_file)
        self.assertTrue(os.path.exists(new_file))
        
        os.remove(new_file)
        self.assertFalse(os.path.exists(new_file))
    
    def test_rename_case_change(self):
        test_file = os.path.join(self.test_dir, "a")
        content = "Hello, Windows!"
        with open(test_file, 'w') as f:
            f.write(content)
        new_file_lower = os.path.join(self.test_dir, "A")
        os.rename(test_file, new_file_lower)
        self.assertTrue(os.path.exists(new_file_lower))
        new_file_upper = os.path.join(self.test_dir, "a")
        os.rename(new_file_lower, new_file_upper)
        self.assertTrue(os.path.exists(new_file_upper))
        os.remove(new_file_upper)

    def test_directory_operations(self):
        nested_dir = os.path.join(self.test_dir, "dir1", "dir2", "dir3")
        os.makedirs(nested_dir)
        
        self.assertTrue(os.path.exists(nested_dir))
        
        test_file = os.path.join(nested_dir, "test.txt")
        Path(test_file).touch()
        
        files = list(Path(self.test_dir).rglob("*"))
        self.assertTrue(len(files) > 0)
        
        new_dir = os.path.join(self.test_dir, "new_dir")
        shutil.move(os.path.join(self.test_dir, "dir1"), new_dir)
        self.assertTrue(os.path.exists(new_dir))
    
    def test_concurrent_operations(self):
        file_count = 10
        thread_count = 5
        
        def write_files(start_idx):
            for i in range(start_idx, start_idx + file_count):
                file_path = os.path.join(self.test_dir, f"concurrent_{i}.txt")
                with open(file_path, 'w') as f:
                    f.write(self.random_string(100))
        
        threads = []
        for i in range(thread_count):
            t = threading.Thread(target=write_files, args=(i * file_count,))
            threads.append(t)
            t.start()
        
        for t in threads:
            t.join()
        
        files = os.listdir(self.test_dir)
        self.assertEqual(len(files), file_count * thread_count)
    
    def test_special_characters(self):
        special_chars = [
            "test with spaces",
            "test_with_unicode_中文",
            "test_with_symbols_!@#$%",
            "test.with.multiple.dots"
        ]
        
        for name in special_chars:
            file_path = os.path.join(self.test_dir, name)
            with open(file_path, 'w') as f:
                f.write("test")
            self.assertTrue(os.path.exists(file_path))
            with open(file_path, 'r') as f:
                self.assertEqual(f.read(), "test")
    
    def test_large_files(self):
        large_file = os.path.join(self.test_dir, "large_file.dat")
        size_mb = 10
        chunk_size = 1024 * 1024  # 1MB
        
        with open(large_file, 'wb') as f:
            for _ in range(size_mb):
                f.write(os.urandom(chunk_size))
        
        self.assertEqual(os.path.getsize(large_file), size_mb * chunk_size)
        
        with open(large_file, 'rb') as f:
            chunks = 0
            while f.read(chunk_size):
                chunks += 1
        self.assertEqual(chunks, size_mb)
    
    def test_file_attributes(self):
        test_file = os.path.join(self.test_dir, "attrs.txt")
        with open(test_file, 'w') as f:
            f.write("test")
        
        
        os.system(f'attrib +R "{test_file}"')
        
        with self.assertRaises(PermissionError):
            with open(test_file, 'w') as f:
                f.write("new content")
        
        os.system(f'attrib -R "{test_file}"')
    @unittest.skip("Windows Do not support")
    def test_symlinks(self):
        source_dir_root = os.path.join(self.test_dir, "source")
        link_dir_root = os.path.join(self.test_dir, "links")
        os.makedirs(source_dir_root)
        os.makedirs(link_dir_root)
        
        source_file = os.path.join(source_dir_root, "source_file.txt")
        source_dir = os.path.join(source_dir_root, "source_dir")
        with open(source_file, 'w') as f:
            f.write("test content")
        os.makedirs(source_dir)
        
        link_file = os.path.join(link_dir_root, "link_file.txt")
        os.symlink(source_file, link_file)
        self.assertTrue(os.path.exists(link_file))
        with open(link_file, 'r') as f:
            self.assertEqual(f.read(), "test content")
            
        link_dir = os.path.join(link_dir_root, "link_dir")
        os.symlink(source_dir, link_dir, target_is_directory=True)
        self.assertTrue(os.path.exists(link_dir))
        
        link_test_file = os.path.join(link_dir, "test.txt")
        with open(link_test_file, 'w') as f:
            f.write("test through link")
        
        source_test_file = os.path.join(source_dir, "test.txt")
        self.assertTrue(os.path.exists(source_test_file))
        with open(source_test_file, 'r') as f:
            self.assertEqual(f.read(), "test through link")
        
        os.remove(source_file)
        self.assertFalse(os.path.exists(link_file))

    def test_long_paths(self):
        deep_dir = self.test_dir
        for i in range(10):  
            deep_dir = os.path.join(deep_dir, f"dir_{i}")
        
        os.makedirs(deep_dir, exist_ok=True)
        test_file = os.path.join(deep_dir, "test.txt")
        
        with open(test_file, 'w') as f:
            f.write("test")
        
        self.assertTrue(os.path.exists(test_file))

if __name__ == '__main__':
    unittest.main(verbosity=2)