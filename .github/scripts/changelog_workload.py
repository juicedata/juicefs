#!/usr/bin/env python3
"""POSIX workload and read-only snapshot helper for changelog_e2e.py (Linux)."""

import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import threading
import time
import traceback

WORKERS = 4
SEEDS = 300
counts = {'reads': 0, 'writes': 0, 'cycles': 0}
lock = threading.Lock()


def count(name):
    with lock:
        counts[name] += 1


def payload(worker, seq, size=16384):
    tag = f'worker={worker};sequence={seq};真实文件数据\n'.encode()
    return (tag * (size // len(tag) + 1))[:size]


def write(path, data):
    with open(path, 'wb', buffering=0) as f:
        assert f.write(data) == len(data)
        os.fsync(f.fileno())
    count('writes')


def read(path, expected):
    with open(path, 'rb', buffering=0) as f:
        actual = f.read()
    if actual != expected:
        raise AssertionError(f'source read mismatch: {path}: {len(actual)} vs {len(expected)}')
    count('reads')


def prime(root):
    (root / 'case').mkdir()
    write(root / 'case' / 'continuous-io', b'initial')
    def worker(w):
        for dirname in ['seed', 'live', 'moved']:
            (root / 'case' / f'{dirname}-{w}').mkdir()
        for seq in range(SEEDS):
            p = root / 'case' / f'seed-{w}' / f'file-{seq:04d}'
            data = payload(w, seq, 16384 + (seq % 5) * 16384)
            write(p, data)
            read(p, data)
            os.setxattr(p, 'user.seed', str(seq).encode())
        seed = root / 'case' / f'seed-{w}'
        os.symlink('file-0000', seed / 'symlink')
        os.link(seed / 'file-0001', seed / 'hardlink')
        subprocess.run(['setfacl', '-m', 'u:1001:rw-', str(seed / 'file-0002')], check=True)
    with concurrent.futures.ThreadPoolExecutor(WORKERS) as pool:
        list(pool.map(worker, range(WORKERS)))
    os.sync()


def live(root, folder):
    done = threading.Event()
    errors = []
    def report():
        with open(folder / 'progress.jsonl', 'w') as f:
            while True:
                with lock:
                    sample = dict(counts, time=time.time())
                f.write(json.dumps(sample) + '\n')
                f.flush()
                if done.wait(.01):
                    with lock:
                        f.write(json.dumps(dict(counts, time=time.time())) + '\n')
                    return
    reporter = threading.Thread(target=report)
    reporter.start()
    # Keep reads and writes flowing even while the other workers run metadata-only operations.
    def continuous_io():
        try:
            with open(root / 'case' / 'continuous-io', 'r+b', buffering=0) as f:
                seq = 0
                while not (folder / 'stop').exists() and not errors:
                    data = str(seq).encode().ljust(32, b' ')
                    if os.pwrite(f.fileno(), data, 0) != len(data):
                        raise AssertionError('short continuous write')
                    os.fsync(f.fileno())
                    count('writes')
                    if os.pread(f.fileno(), len(data), 0) != data:
                        raise AssertionError('continuous read mismatch')
                    count('reads')
                    seq += 1
                    time.sleep(.02)
        except Exception:
            errors.append(traceback.format_exc())
    traffic = threading.Thread(target=continuous_io)
    traffic.start()
    def worker(w):
        seq = 0
        time.sleep(w * .07)
        base = root / 'case' / f'live-{w}'
        moved = root / 'case' / f'moved-{w}'
        seed = root / 'case' / f'seed-{w}'
        try:
            while not (folder / 'stop').exists() and not errors:
                data = payload(w, seq, 8192 + (seq % 3) * 4096)
                p = base / f'新文件-{seq:05d}'
                write(p, data)
                read(p, data)
                os.setxattr(p, 'user.live', b'hello,()|%\x00\xff' + str(seq).encode())
                os.setxattr(p, 'user.remove_me', b'temporary')
                os.removexattr(p, 'user.remove_me')
                os.chmod(p, 0o640 if seq % 2 else 0o644)
                os.chown(p, 1000 + w, 2000 + w)
                os.utime(p, ns=(1700000000123456789 + seq, 1700000000987654321 + seq))
                if seq % 10 == 0:
                    subprocess.run(['setfacl', '-m', 'u:1001:rw-', str(p)], check=True)
                q = moved / p.name
                os.rename(p, q)
                hard = base / f'hard-{seq:05d}'
                os.link(q, hard)
                sym = base / f'sym-{seq:05d}'
                os.symlink('../' + moved.name + '/' + q.name, sym)
                read(sym, data)
                assert os.stat(hard).st_ino == os.stat(q).st_ino

                # Modify existing baseline files, including non-aligned writes and holes.
                old = seed / f'file-{seq % SEEDS:04d}'
                write(old, data)
                with open(old, 'r+b', buffering=0) as f:
                    assert os.pwrite(f.fileno(), b'patch', 4093) == 5
                    f.truncate(20000)
                    os.fsync(f.fileno())
                expected = data[:4093] + b'patch' + data[4098:]
                expected += b'\x00' * (20000 - len(expected))
                read(old, expected)

                # Real kernel copy_file_range and fallocate requests.
                copy = base / f'copy-{seq:05d}'
                with open(q, 'rb', buffering=0) as a, open(copy, 'wb', buffering=0) as b:
                    assert os.copy_file_range(a.fileno(), b.fileno(), len(data)) == len(data)
                    os.posix_fallocate(b.fileno(), len(data), 4096)
                    os.fsync(b.fileno())
                count('writes')
                read(copy, data + b'\x00' * 4096)

                # Replace an open inode, then write/read it after unlink.
                held = base / 'held'
                write(held, b'old-data')
                with open(held, 'r+b', buffering=0) as f:
                    replacement = base / 'replacement'
                    write(replacement, b'new-data')
                    os.replace(replacement, held)
                    f.seek(0)
                    assert f.read() == b'old-data'
                    f.seek(0)
                    f.write(b'changed!')
                    os.fsync(f.fileno())
                read(held, b'new-data')
                os.unlink(held)
                directory = base / 'temporary-dir'
                directory.mkdir()
                directory.rmdir()
                if seq % 3 == 0:
                    for victim in [q, hard, sym, copy]:
                        victim.unlink()
                count('cycles')
                seq += 1
                time.sleep(1)
        except Exception:
            errors.append(traceback.format_exc())
    with concurrent.futures.ThreadPoolExecutor(WORKERS) as pool:
        list(pool.map(worker, range(WORKERS)))
    traffic.join()
    done.set()
    reporter.join()
    os.sync()
    if errors:
        raise RuntimeError('\n'.join(errors))
    print(json.dumps(counts), flush=True)


def snapshot(root, output):
    entries, errors = {}, []
    paths = [root]
    def walk(p):
        paths.append(p)
        if p.is_dir() and not p.is_symlink():
            for child in sorted(p.iterdir()):
                walk(child)
    walk(root / 'case')
    for p in paths:
        name = '/' if p == root else '/' + str(p.relative_to(root))
        try:
            s = os.lstat(p)
            rec = {field: getattr(s, 'st_' + field) for field in ['ino', 'mode', 'nlink', 'uid', 'gid', 'size', 'rdev', 'atime_ns', 'mtime_ns', 'ctime_ns']}
            rec['xattrs'] = {k: os.getxattr(p, k, follow_symlinks=False).hex() for k in sorted(os.listxattr(p, follow_symlinks=False))}
            if stat.S_ISREG(s.st_mode):
                sha = hashlib.sha256()
                with open(p, 'rb', buffering=0) as f:
                    for block in iter(lambda: f.read(1024 * 1024), b''):
                        sha.update(block)
                rec['sha256'] = sha.hexdigest()
            elif stat.S_ISLNK(s.st_mode):
                rec['target'] = os.readlink(p)
            entries[name] = rec
        except Exception:
            errors.append({'path': name, 'error': traceback.format_exc()})
    s = os.statvfs(root)
    stats = {'usedSpace': (s.f_blocks - s.f_bfree) * s.f_frsize, 'usedInodes': s.f_files - s.f_ffree}
    output.write_text(json.dumps({'entries': entries, 'statfs': stats, 'errors': errors}, indent=2))
    print(json.dumps({'entries': len(entries), 'statfs': stats, 'errors': len(errors)}), flush=True)
    if errors:
        sys.exit(1)


if __name__ == '__main__':
    mode = sys.argv[1]
    root = Path(sys.argv[2])
    if mode == 'prime':
        prime(root)
    elif mode == 'live':
        live(root, Path(sys.argv[3]))
    elif mode == 'snapshot':
        snapshot(root, Path(sys.argv[3]))
    elif mode == 'seal':
        os.setxattr(root, 'user.replication_end', sys.argv[3].encode())
        os.sync()
    elif mode == 'probe':
        try:
            print(os.getxattr(root, 'user.replication_end').decode())
        except OSError:
            sys.exit(1)
    else:
        sys.exit('unknown workload mode: ' + mode)
