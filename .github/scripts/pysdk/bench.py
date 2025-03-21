import os
import random
import sys
import time
import argparse
import threading
import hashlib

sys.path.append('.')
from sdk.python.juicefs.juicefs import juicefs

def print_stats(stats, interval):
    while not stats['stop']:
        time.sleep(interval)
        elapsed_time = time.time() - stats['start_time']
        iops = stats['ops'] / elapsed_time
        print(f"IOPS: {iops:.2f}")

def seq_write(filename, client: juicefs.Client, protocol, block_size, buffering, run_time, file_size):
    stats = {'bytes': 0, 'ops': 0, 'start_time': time.time(), 'stop': False}
    stats_thread = threading.Thread(target=print_stats, args=(stats, 2))
    stats_thread.start()

    def perform_seq_writes(f):
        while time.time() - stats['start_time'] < run_time and stats['bytes'] < file_size:
            data = os.urandom(block_size)  
            f.write(data)
            stats['bytes'] += block_size
            stats['ops'] += 1

    try:
        if protocol == 'pysdk':
            with client.open(filename, 'wb', buffering=buffering) as f:
                perform_seq_writes(f)
        else:
            with open(f'/tmp/jfs/{filename}', 'wb') as f:
                perform_seq_writes(f)
    finally:
        stats['stop'] = True
        stats_thread.join()

def random_write(filename, client: juicefs.Client, protocol, buffering, block_size, run_time, file_size, seed):
    random.seed(seed)
    stats = {'bytes': 0, 'ops': 0, 'start_time': time.time(), 'stop': False}
    stats_thread = threading.Thread(target=print_stats, args=(stats, 2))
    stats_thread.start()

    write_records = []

    def perform_random_writes(f):
        while time.time() - stats['start_time'] < run_time and stats['bytes'] < file_size:
            offset = random.randint(0, file_size - block_size)
            data = os.urandom(block_size)  
            f.seek(offset)
            f.write(data)
            stats['bytes'] += block_size
            stats['ops'] += 1

            f.seek(offset)
            read_data = f.read(block_size)
            if hashlib.md5(read_data).hexdigest() != hashlib.md5(data).hexdigest():
                print(f"data inconsistency: offset {offset}")
                return False
    try:
        if protocol == 'pysdk':
            with client.open(filename, 'w+b', buffering=buffering) as f:
                perform_random_writes(f)
        else:
            with open(f'/tmp/jfs/{filename}', 'w+b') as f:
                perform_random_writes(f)
    finally:
        stats['stop'] = True
        stats_thread.join()

def seq_read(filename, client: juicefs.Client, protocol, block_size, buffering):
    stats = {'bytes': 0, 'ops': 0, 'start_time': time.time(), 'stop': False}
    stats_thread = threading.Thread(target=print_stats, args=(stats, 2))
    stats_thread.start()

    def perform_seq_reads(f):
        while True:
            buffer = f.read(block_size)
            if not buffer:
                break
            stats['bytes'] += len(buffer)
            stats['ops'] += 1

    try:
        if protocol == 'pysdk':
            with client.open(filename, 'rb', buffering=buffering) as f:
                perform_seq_reads(f)
        else:
            with open(f'/tmp/jfs/{filename}', 'rb') as f:
                perform_seq_reads(f)
    finally:
        stats['stop'] = True
        stats_thread.join()

def random_read(filename, client: juicefs.Client, protocol, buffering, block_size, seed, count):
    random.seed(seed)
    stats = {'bytes': 0, 'ops': 0, 'start_time': time.time(), 'stop': False}
    stats_thread = threading.Thread(target=print_stats, args=(stats, 2))
    stats_thread.start()

    def perform_random_reads(f):
        f.seek(0, 2)
        file_size = f.tell()
        for _ in range(count):
            length = random.randint(1, block_size)
            offset = random.randint(0, file_size - length)
            f.seek(offset)
            buffer = f.read(length)
            stats['bytes'] += len(buffer)
            stats['ops'] += 1

    try:
        if protocol == 'pysdk':
            with client.open(filename, 'rb', buffering=buffering) as f:
                perform_random_reads(f)
        else:
            with open(f'/tmp/jfs/{filename}', 'rb') as f:
                perform_random_reads(f)
    finally:
        stats['stop'] = True
        stats_thread.join()

def clean_page_cache():
    with open('/proc/sys/vm/drop_caches', 'w') as f:
        f.write('3')
        f.flush()

if __name__ == "__main__":
    parser = argparse.ArgumentParser('benchmark on pysdk')
    parser.add_argument('operation', type=str, help='operation: [random_read|seq_read|random_write|seq_write]')
    parser.add_argument('filename', type=str, help='file name')
    parser.add_argument('--seed', type=int, default=0, help='seed of random read/write')
    parser.add_argument('--count', type=int, default=1000, help='count of random read')
    parser.add_argument('--buffer-size', type=int, default=300, help='buffer size')
    parser.add_argument('--block-size', type=int, default=128*1024, help='block size')
    parser.add_argument('--buffering', type=int, default=2*1024*1024, help='buffering')
    parser.add_argument('--run-time', type=int, default=10, help='run time in seconds')
    parser.add_argument('--file-size', type=int, default=1024*1024*1024, help='file size in bytes')
    parser.add_argument('-p', '--protocol', type=str, default='pysdk', help='protocol: [fuse|pysdk]')
    args = parser.parse_args()

    if args.protocol == 'pysdk':
        meta_url=os.environ.get('META_URL', 'redis://localhost')
        client = juicefs.Client("test-volume", meta=meta_url, access_log="/tmp/access.log")
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
        hot_read=time.time()-start
        print(f"{cold_read:.2f} {hot_read:.2f} ")
    elif args.operation == 'random_read':
        random_read(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering, seed=args.seed, count=args.count)
        hot_read=time.time()-start
        print(f"{cold_read:.2f} {hot_read:.2f} ")
    elif args.operation == 'seq_write':
        seq_write(client=client, filename=args.filename, protocol=args.protocol, block_size=args.block_size, buffering=args.buffering, run_time=args.run_time, file_size=args.file_size)
    elif args.operation == 'random_write':
        random_write(client=client, filename=args.filename, protocol=args.protocol, buffering=args.buffering, block_size=args.block_size, run_time=args.run_time, file_size=args.file_size, seed=args.seed)
    else:
        raise ValueError(f"Unsupported operation: {args.operation}")