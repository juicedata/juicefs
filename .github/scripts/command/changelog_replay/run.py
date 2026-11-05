#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import tempfile
import unittest

from common import ReplayTestCase


def flatten(suite):
    for item in suite:
        if isinstance(item, unittest.TestSuite):
            yield from flatten(item)
        else:
            yield item


def main():
    parser = argparse.ArgumentParser(description='Replay the same filesystem cases across metadata engines and backup formats.')
    parser.add_argument('--src-meta', required=True, help='empty source metadata namespace')
    parser.add_argument('--dst-meta', required=True, help='empty destination metadata namespace')
    parser.add_argument('--dump-format', choices=['json', 'binary', 'all'], default='all')
    parser.add_argument('--case', action='append', default=[], help='category or substring of a test name; repeat to select several')
    parser.add_argument('--juicefs', default='./juicefs')
    parser.add_argument('--log-dir')
    parser.add_argument('--timeout', type=int, default=120)
    parser.add_argument('--list', action='store_true')
    options = parser.parse_args()
    if options.src_meta == options.dst_meta:
        parser.error('source and destination must be different namespaces')
    options.juicefs = str(Path(options.juicefs).resolve())
    tests = list(flatten(unittest.defaultTestLoader.discover(str(Path(__file__).parent), pattern='run_*.py')))
    tests = [test for test in tests if not options.case or
             any(name in test.id().removeprefix('run_') for name in options.case)]
    if not tests:
        parser.error('no test cases matched')
    if options.list:
        print('\n'.join(test.id() for test in tests))
        return
    options.log_dir = str(Path(options.log_dir or tempfile.mkdtemp(prefix='changelog-replay.')).resolve())
    Path(options.log_dir).mkdir(parents=True, exist_ok=True)
    Path(options.log_dir).chmod(0o755)
    if os.geteuid() != 0:
        parser.error('these FUSE, chown and ACL cases require root')
    formats = ['json', 'binary'] if options.dump_format == 'all' else [options.dump_format]
    reports = []
    for backup_format in formats:
        options.dump_format = backup_format
        ReplayTestCase.options = options
        suite = unittest.TestSuite(type(test)(test._testMethodName) for test in tests)
        result = unittest.TextTestRunner(verbosity=2).run(suite)
        reports.append({'format': backup_format, 'tests': result.testsRun,
                        'failures': [{'test': test.id(), 'traceback': error} for test, error in result.failures + result.errors],
                        'skipped': [{'test': test.id(), 'reason': reason} for test, reason in result.skipped],
                        'passed': result.wasSuccessful()})
    (Path(options.log_dir) / 'results.json').write_text(json.dumps(reports, indent=2))
    raise SystemExit(0 if all(report['passed'] for report in reports) else 1)


if __name__ == '__main__':
    main()
