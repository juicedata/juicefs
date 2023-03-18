#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os
import re


def parse_test_cases(test_file_path):
    test_cases = []
    with open(test_file_path) as f:
        lines = f.readlines()
        for line in lines:
            # func TestXattr2(t *testing.T) {
            if re.search('^func\s+Test.+', line.strip()):
                if 'skip mutate' in line:
                    continue
                name = line.strip().split(' ')[1].split('(')[0]
                test_cases.append(name)
    return test_cases


if __name__ == '__main__':
    test_file_path = os.environ['TEST_FILE_NAME']
    if not test_file_path:
        print('test file name is empty')
        exit(1)
    test_cases = parse_test_cases(test_file_path)
    if len(test_cases) == 0:
        print('test case is empty')
        exit(1)
    test_cases_str = '|'.join(test_cases)
    print(f'({test_cases_str})')