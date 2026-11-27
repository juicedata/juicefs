import errno
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import time
import unittest


def write(path, data):
    with open(path, 'wb', buffering=0) as stream:
        assert stream.write(data) == len(data)
        os.fsync(stream.fileno())


def read(path, expected):
    assert path.read_bytes() == expected, str(path)


def payload(worker, seq, size=16384):
    tag = f'worker={worker};sequence={seq};真实文件数据\n'.encode()
    return (tag * (size // len(tag) + 1))[:size]


def expect_errno(expected, operation, *args):
    try:
        operation(*args)
    except OSError as error:
        assert error.errno == expected, (operation, expected, error)
    else:
        raise AssertionError(f'{operation} should fail with errno {expected}')


def get_acl(path):
    return subprocess.check_output(['getfacl', '-n', '-c', '-p', str(path)], text=True).strip()


def parse_json(output):
    # Xorm writes its database ping log to stdout, including at error log level.
    return json.loads('\n'.join(line for line in output.splitlines() if not line.startswith('[xorm] ')))


def snapshot(root, include_trash=False):
    entries = {}
    paths = [root]

    def walk(path):
        paths.append(path)
        if path.is_dir() and not path.is_symlink():
            for child in sorted(path.iterdir()):
                walk(child)

    walk(root / 'case')
    if include_trash:
        walk(root / '.trash')
    for path in paths:
        s = path.lstat()
        record = {key: getattr(s, 'st_' + key) for key in
                  ('ino', 'mode', 'nlink', 'uid', 'gid', 'size', 'rdev', 'atime_ns', 'mtime_ns', 'ctime_ns')}
        if path == root / '.trash':
            for key in ('atime_ns', 'mtime_ns', 'ctime_ns'):
                del record[key]  # Virtual trash root timestamps are local to each mount.
        try:
            names = os.listxattr(path, follow_symlinks=False)
        except OSError as error:
            if error.errno != errno.ENODATA or not (path == root / '.trash' or path.parent == root / '.trash'):
                raise
            names = []
        record['xattrs'] = {name: os.getxattr(path, name, follow_symlinks=False).hex() for name in sorted(names)}
        if stat.S_ISREG(s.st_mode):
            digest = hashlib.sha256()
            with path.open('rb') as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b''):
                    digest.update(block)
            record['sha256'] = digest.hexdigest()
        elif stat.S_ISLNK(s.st_mode):
            record['target'] = os.readlink(path)
        entries[str(path.relative_to(root))] = record
    fs = os.statvfs(root)
    return {'entries': entries, 'statfs': {
        'usedSpace': (fs.f_blocks - fs.f_bfree) * fs.f_frsize,
        'usedInodes': fs.f_files - fs.f_ffree,
    }}


class ReplayTestCase(unittest.TestCase):
    options = None

    def setUp(self):
        self.folder = Path(self.options.log_dir) / self.options.dump_format / self.id()
        self.folder.mkdir(parents=True)
        self.folder.chmod(0o755)
        self.src_mount, self.dst_mount = self.folder / 'source', self.folder / 'destination'
        self.src, self.dst = self.src_mount / 'case', self.dst_mount / 'case'
        self.src_meta, self.dst_meta = self.options.src_meta, self.options.dst_meta
        self.source_empty_xattrs = not self.src_meta.startswith('tikv://')
        self.destination_empty_xattrs = not self.dst_meta.startswith('tikv://')
        self.cli = self.options.juicefs
        self.baseline = self.folder / 'baseline'
        self.mounts, self.owned = {}, []
        self.follower = None
        self.replayed = False
        self.include_trash = False
        self.serial = 0
        self.addCleanup(self.cleanup)
        for meta in (self.src_meta, self.dst_meta):
            status = self.jfs('status', meta, check=False)
            self.assertNotEqual(status.returncode, 0, f'refusing to overwrite an existing volume: {meta}')
        self.jfs('format', '--storage', 'file', '--bucket', self.folder / 'objects',
                 '--block-size', '64', '--trash-days', '0', '--enable-acl', self.src_meta, 'replay-test')
        self.uuid = parse_json(self.jfs('status', self.src_meta).stdout)['Setting']['UUID']
        self.owned.append(self.src_meta)
        self.jfs('config', self.src_meta, '--changelog', '--changelog-max-age', '1h')
        self.mount(self.src_meta, self.src_mount)
        self.src.mkdir()

    def command(self, *args, check=True, **kwargs):
        self.serial += 1
        log = self.folder / f'{self.serial:03d}-{Path(str(args[0])).name}.log'
        result = subprocess.run(list(map(str, args)), capture_output=True, text=True,
                                timeout=self.options.timeout, **kwargs)
        log.write_text(f'$ {list(map(str, args))!r}\nexit={result.returncode}\n{result.stdout}{result.stderr}')
        if check:
            self.assertEqual(result.returncode, 0, f'{log}\n{result.stdout}\n{result.stderr}')
        return result

    def jfs(self, *args, **kwargs):
        return self.command(self.cli, *args, **kwargs)

    def mount(self, meta, path, readonly=False):
        path.mkdir(exist_ok=True)
        self.serial += 1
        log = self.folder / f'{self.serial:03d}-mount-{path.name}.log'
        args = [self.cli, 'mount', '-f', '--no-bgjob', '--no-agent', '--max-deletes', '0',
                '--no-usage-report', '--heartbeat', '1s', '--cache-dir', 'memory', '--cache-size', '0',
                '--metrics', '127.0.0.1:0', '--attr-cache', '0', '--entry-cache', '0', '--dir-entry-cache', '0',
                '--atime-mode', 'strictatime']
        if readonly:
            args.append('--read-only')
        with log.open('w') as output:
            process = subprocess.Popen(args + [meta, str(path)], stdout=output, stderr=subprocess.STDOUT,
                                       env=dict(os.environ, JFS_SUPERVISOR='test'))
        self.mounts[path] = process
        deadline = time.monotonic() + self.options.timeout
        while not os.path.ismount(path):
            self.assertIsNone(process.poll(), log.read_text())
            self.assertLess(time.monotonic(), deadline, f'mount timed out: {log}')
            time.sleep(.1)

    def unmount(self, path):
        process = self.mounts.get(path)
        if process is None:
            return
        self.command('umount', path)
        self.assertEqual(process.wait(timeout=self.options.timeout), 0)
        del self.mounts[path]

    def bootstrap(self):
        # Closing the writer session flushes stats and quotas before the baseline.
        self.unmount(self.src_mount)
        flags = ['--binary', '--threads', '1'] if self.options.dump_format == 'binary' else []
        self.jfs('dump', *flags, self.src_meta, self.baseline)
        self.owned.append(self.dst_meta)
        self.jfs('load', *flags, self.dst_meta, self.baseline)
        self.mount(self.src_meta, self.src_mount)

    def replay(self, *, dry_run=False, flags=None):
        args = ['changelog', 'replay', self.src_meta, self.dst_meta, '--follow=false', '--log-dir', self.folder]
        if flags is not None:
            args += list(map(str, flags))
        elif not self.replayed:
            args += ['--backup', str(self.baseline)]
        if dry_run:
            args.append('--dry-run')
        result = self.jfs(*args)
        if not dry_run:
            self.replayed = True
        return result.stdout + result.stderr

    def start_replay(self):
        self.serial += 1
        log = self.folder / f'{self.serial:03d}-follow.log'
        args = [self.cli, 'changelog', 'replay', self.src_meta, self.dst_meta, '--log-dir', str(self.folder)]
        if not self.replayed:
            args += ['--backup', str(self.baseline)]
        with log.open('w') as output:
            self.follower = subprocess.Popen(args, stdout=output, stderr=subprocess.STDOUT)
        deadline = time.monotonic() + self.options.timeout
        while 'Start changelog replay:' not in log.read_text():
            self.assertIsNone(self.follower.poll(), log.read_text())
            self.assertLess(time.monotonic(), deadline, f'replay startup timed out: {log}')
            time.sleep(.1)
        self.replayed = True
        return log

    def stop_replay(self):
        if self.follower is not None:
            self.assertIsNone(self.follower.poll(), 'replay exited before the test stopped it')
            self.follower.send_signal(signal.SIGINT)
            self.assertEqual(self.follower.wait(timeout=self.options.timeout), 0)
            self.follower = None

    def finish(self, expected_ops=()):
        self.unmount(self.src_mount)
        self.stop_replay()
        self.replay()
        changelog = self.jfs('changelog', self.src_meta, '--from', '1', '--follow=false').stdout
        (self.folder / 'changelog.txt').write_text(changelog)
        applied = set()
        for log in self.folder.glob('*.log'):
            applied.update(map(int, re.findall(r'Replayed changelog: version=(\d+)', log.read_text())))
        operations = {match[2] for match in re.finditer(r'(?m)^(\d+): [^|]+\|(\w+)\(', changelog)
                      if int(match[1]) in applied}
        self.assertFalse(set(expected_ops) - operations, f'missing replayed operations: {set(expected_ops) - operations}')
        self.unmount(self.dst_mount)
        self.mount(self.src_meta, self.src_mount, readonly=True)
        self.mount(self.dst_meta, self.dst_mount, readonly=True)
        source, target = snapshot(self.src_mount, self.include_trash), snapshot(self.dst_mount, self.include_trash)
        (self.folder / 'source.json').write_text(json.dumps(source, indent=2))
        (self.folder / 'destination.json').write_text(json.dumps(target, indent=2))
        differences = []
        for path in sorted(source['entries'].keys() | target['entries'].keys()):
            left, right = source['entries'].get(path), target['entries'].get(path)
            if left != right:
                differences.append({'path': path, 'source': left, 'destination': right})
        if source['statfs'] != target['statfs']:
            differences.append({'statfs': {'source': source['statfs'], 'destination': target['statfs']}})
        (self.folder / 'diff.json').write_text(json.dumps(differences, indent=2))
        if differences:
            self.fail(f'{len(differences)} source/destination differences: {self.folder / "diff.json"}')

    def cleanup(self):
        errors = []
        if self.follower is not None:
            self.follower.send_signal(signal.SIGINT)
            try:
                self.follower.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.follower.kill()
                self.follower.wait()
                errors.append('replay did not stop gracefully')
        for path, process in list(self.mounts.items()):
            try:
                self.unmount(path)
            except Exception as error:
                errors.append(str(error))
                subprocess.run(['umount', '-l', str(path)], capture_output=True, timeout=15)
                if process.poll() is None:
                    process.kill()
                    process.wait()
        for meta in reversed(self.owned):
            result = self.jfs('destroy', meta, self.uuid, '--yes', '--force', check=False)
            if result.returncode:
                errors.append(result.stderr)
        self.assertFalse(errors, '\n'.join(errors))
