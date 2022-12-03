#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os
import re
import sys


def parse_test_jobs(test_file_path):
    with open(test_file_path) as f:
        lines = f.readlines()
        for line in lines:
            g = re.search('^//mutate_test_job_number:\s*(.+)', line.strip())
            if g:
                return int(g.group(1))
                
    return 0

if __name__ == '__main__':
    test_file_path = os.environ['TEST_FILE_NAME']
    if not test_file_path:
        print('test file name is empty', file=sys.stderr)
        exit(1)
    print(parse_test_jobs(test_file_path))
    