import random
import os

def random_write(path1, path2, count=1000):
    if not os.path.exists(path1):
        os.system(f'touch {path1}')
    if not os.path.exists(path2):
        os.system(f'touch {path2}')
    with open(path1, 'r+b') as f1, open(path2, 'r+b') as f2:
        print(f1.seek(0, 2))
        for i in range(1, count):
            # Get the size of the file
            # size = os.path.getsize(path1)
            size = f1.seek(0, 2)
            # Generate a random position within the file that is not at the end
            pos = random.randint(0, size)
            f1.seek(pos, 0)
            f2.seek(pos, 0)
            # Generate random data
            length = random.randint(1, 1024*1024*5)
            data = os.urandom(length)
            # data = b"abcdefg"
            length = len(data)
            # Write data to the files
            f1.write(data)
            f2.write(data)
            f1.flush()
            f2.flush()
            assert f1.seek(0, 2) == pos+max(length, size-pos)
            assert f1.seek(0, 2) == f2.seek(0, 2)
            print("Wrote %d bytes at position %d" % (length, pos))

def random_read(path1, path2):
    with open(path1, 'rb') as f1, open(path2, 'rb') as f2:
        size = f1.seek(0, 2)
        pos = random.randint(0, size)
        f1.seek(pos)
        f2.seek(pos)
        len = random.randint(1, 1024*1024)
        assert f1.read(len) == f2.read(len)
        print("Read %d bytes at position %d" % (len, pos))

def read_all(path1, path2):
    with open(path1, 'rb') as f1, open(path2, 'rb') as f2:
        assert f1.read() == f2.read()
        print("Read all bytes")
    
if __name__ == '__main__':
    path1 = os.environ.get('PATH1', '/tmp/test1')
    path2 = os.environ.get('PATH2', '/tmp/test2')
    print(f'path1: {path1}, path2: {path2}')
    if os.path.exists(path1):
        os.remove(path1)
    if os.path.exists(path2):
        os.remove(path2)
    for i in range(10):
        random_write(path1, path2, count=100)

    for i in range(1000):
        random_read(path1, path2)

    read_all(path1, path2)