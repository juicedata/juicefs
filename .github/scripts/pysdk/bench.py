import random
import sys
import time
import argparse

sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs

# ./start_meta.sh
# ./cmd/mount/mount mount --no-update --conf-dir=/root/jfs/deploy/docker --cache-dir /dev/shm --cache-size 16G test-volume /tmp/jfs
# dd if=/dev/zero of=/tmp/jfs/test bs=1G count=10
# 顺序读
# echo 3 >/proc/sys/vm/drop_caches && rm /dev/shm/test-volume/raw -rf && python3 .github/scripts/pysdk/bench.py seq_read test --buffer-size 300 --block-size $((128*1024)) --buffering $((2*1024*1024)) -p pysdk
# echo 3 >/proc/sys/vm/drop_caches && rm /dev/shm/test-volume/raw -rf && python3 .github/scripts/pysdk/bench.py seq_read test --buffer-size 300 --block-size $((128*1024)) --buffering $((2*1024*1024)) -p fuse
# 随机读
# echo 3 >/proc/sys/vm/drop_caches && rm /dev/shm/test-volume/raw -rf && python3 .github/scripts/pysdk/bench.py random_read test --buffer-size 300 --block-size $((128*1024)) --buffering $((2*1024*1024))  --count=100 -p pysdk
# echo 3 >/proc/sys/vm/drop_caches && rm /dev/shm/test-volume/raw -rf && python3 .github/scripts/pysdk/bench.py random_read test --buffer-size 300 --block-size $((128*1024)) --buffering $((2*1024*1024))  --count=100 -p fuse

def seq_read(filename, client:juicefs.Client, protocol, block_size, buffering):
    def perform_seq_reads(f):
        while True:
            buffer = f.read(block_size)
            if not buffer:
                break

    if protocol == 'pysdk':
        with client.open(filename, 'rb', buffering=buffering) as f:
            perform_seq_reads(f)
    else:
        with open(f'/tmp/jfs/{filename}', 'rb') as f:
            perform_seq_reads(f)

def random_read(filename, client:juicefs.Client, protocol, buffering, block_size, seed, count):
    random.seed(seed)
    
    def perform_random_reads(f):
        f.seek(0, 2)
        file_size = f.tell()
        for _ in range(count):
            length = random.randint(1, block_size)
            offset = random.randint(0, file_size - length) 
            f.seek(offset)
            f.read(length)

    if protocol == 'pysdk':
        with client.open(filename, 'rb', buffering=buffering) as f:
            perform_random_reads(f)
    else:
        with open(f'/tmp/jfs/{filename}', 'rb') as f:
            perform_random_reads(f)

def clean_page_cache():
    with open('/proc/sys/vm/drop_caches', 'w') as f:
        f.write('3')
        f.flush()

if __name__ == "__main__":
    parser = argparse.ArgumentParser('benchmark on pysdk')
    parser.add_argument('operation', type=str, help='operation: [random_read|seq_read]')
    parser.add_argument('filename', type=str, help='file name')
    parser.add_argument('--seed', type=int, default=0, help='seed of random read')
    parser.add_argument('--count', type=int, default=1000, help='count of random read')
    parser.add_argument('--buffer-size', type=int, default=300, help='buffer size')
    parser.add_argument('--block-size', type=int, default=128*1024, help='block size')
    parser.add_argument('--buffering', type=int, default=2*1024*1024, help='buffering')
    parser.add_argument('-p', '--protocol', type=str, default='pysdk', help='protocol: [fuse|pysdk]')
    args = parser.parse_args()
    if args.protocol == 'pysdk':
        client = juicefs.Client('test-volume', conf_dir='deploy/docker', cache_dir='/dev/shm', cache_size='16G', buffer_size=300)
    else:
        client = None
    start=time.time()
    if args.operation == 'seq_read':
        seq_read(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering)
    elif args.operation == 'random_read':
        random_read(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering, seed=args.seed, count=args.count)
    cold_read=time.time()-start
    clean_page_cache()
    start=time.time()
    if args.operation == 'seq_read':
        seq_read(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering)
    elif args.operation == 'random_read':
        random_read(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering, seed=args.seed, count=args.count)
    hot_read=time.time()-start
    print(f"{cold_read:.2f} {hot_read:.2f} ")
