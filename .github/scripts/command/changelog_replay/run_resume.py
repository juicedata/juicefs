import re
import time

from common import ReplayTestCase, snapshot, write


class ResumeTests(ReplayTestCase):
    def test_resume_and_dry_run(self):
        write(self.src / 'baseline', b'baseline')
        self.bootstrap()
        write(self.src / 'first', b'first')
        self.unmount(self.src_mount)
        self.mount(self.dst_meta, self.dst_mount, readonly=True)
        before = snapshot(self.dst_mount)
        self.assertIn('Validated changelog:', self.replay(dry_run=True))
        self.assertEqual(snapshot(self.dst_mount), before)
        first = self.replay()
        self.assertNotIn('Read replay progress log', first)
        self.baseline.rename(self.folder / 'baseline.used')
        self.mount(self.src_meta, self.src_mount)
        (self.src / 'first').rename(self.src / 'moved')
        write(self.src / 'resumed', b'after restart')
        self.unmount(self.src_mount)
        before = snapshot(self.dst_mount)
        progress = next(self.folder.glob('changelog-replay-*.log'))
        saved_progress = progress.read_bytes()
        dry = self.replay(dry_run=True, flags=['--backup', self.folder / 'baseline.used'])
        self.assertIn('Validated changelog:', dry)
        self.assertEqual(snapshot(self.dst_mount), before)
        self.assertEqual(progress.read_bytes(), saved_progress)
        resumed = self.replay()
        versions = lambda log: set(re.findall(r'(?:Replayed|Skipped) changelog: version=(\d+)', log))
        self.assertTrue(versions(first))
        self.assertTrue(versions(resumed))
        self.assertFalse(versions(first) & versions(resumed))
        self.assertIn('Read replay progress log', resumed)
        self.assertNotIn('Ignoring --', resumed)
        warning = self.replay(flags=['--from=0', '--backup', self.baseline])
        self.assertIn('Ignoring --from', warning)
        self.assertIn('Ignoring --backup', warning)
        self.finish({'CREATE', 'MOVE'})
        self.assertFalse((self.dst / 'first').exists())
        self.assertEqual((self.dst / 'moved').read_bytes(), b'first')
        self.assertEqual((self.dst / 'resumed').read_bytes(), b'after restart')

    def test_graceful_stop(self):
        self.bootstrap()
        first_log = self.start_replay()
        for index in range(30):
            write(self.src / str(index), str(index).encode())
        deadline = time.monotonic() + self.options.timeout
        while not re.search(r'Replayed changelog: version=\d+ operation=CREATE', first_log.read_text()):
            self.assertIsNone(self.follower.poll(), first_log.read_text())
            self.assertLess(time.monotonic(), deadline, f'replay made no progress: {first_log}')
            time.sleep(.1)
        self.stop_replay()
        write(self.src / 'after-stop', b'after stop')
        self.finish({'CREATE', 'WRITE'})
        self.assertIn('Changelog replay stopped:', first_log.read_text())
        self.assertEqual((self.dst / 'after-stop').read_bytes(), b'after stop')

    def test_invalid_arguments(self):
        self.bootstrap()
        result = self.jfs('changelog', 'replay', self.src_meta, self.src_meta, '--dry-run', '--follow=false', check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('must be different', result.stderr)
        invalid = self.folder / 'invalid-backup'
        invalid.write_text('invalid backup')
        result = self.jfs('changelog', 'replay', self.src_meta, self.dst_meta, '--backup', invalid,
                          '--dry-run', '--follow=false', check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('read changelog baseline', result.stderr)
        self.finish()
