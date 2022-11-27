#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os
import re


def parse_mutate_log(log_file):
    mutants = {}
    with open(log_file) as f:
        lines = f.readlines()
        for line in lines:
            # The mutation score is 0.326154 (106 passed, 180 failed, 31 duplicated, 39 skipped, total is 325)
            if line.strip().startswith("The mutation score is"):
                result = re.match(r'(.+)\((\d+) passed, (\d+) failed, (\d+) duplicated, (\d+) skipped, total is (\d+)\)', line)
                passed = result.group(2)
                failed = result.group(3)
                duplicated = result.group(4)
                skipped = result.group(5)
                total = result.group(6)
                score = int(passed) * 1.0 / (int(total) - int(skipped))
                return f'The mutation score is {score} ({passed} passed, {failed} failed, {duplicated} duplicated, {skipped} skipped, total is {total})'
    return ''

if __name__ == '__main__':
    log_file = os.environ['LOG_FILE']
    if not log_file:
        print('log file is empty')
        exit(1)
    s = parse_mutate_log(log_file)
    if s:
        print(s)
    else:
        exit(1)
    