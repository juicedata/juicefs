#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os
import re


def parse_check_sum(test_file_path):
    check_sum_list = []
    with open(test_file_path) as f:
        lines = f.readlines()
        for line in lines:
            # //checksum 5b1ca0cfedd786d9df136a0e042df23a
            group = re.match('//checksum\s+(.{32})$', line.strip())
            if group:
                check_sum_list.append(group.group(1))
    return check_sum_list

def save_black_list(file_name, check_sum_list):
    with open(file_name, 'w') as f:
        f.write('\n'.join(check_sum_list))

if __name__ == '__main__':
    test_file_path = os.environ['TEST_FILE_NAME']
    if not test_file_path:
        print('test file name is empty')
        exit(1)
    black_list_file = os.environ['BLACK_LIST_FILE']
    save_black_list(black_list_file,  parse_check_sum(test_file_path))