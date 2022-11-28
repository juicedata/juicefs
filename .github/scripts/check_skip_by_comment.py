#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os


def is_mutation_skipped_by_comment(original_file, changed_file):
    original = open(original_file, 'r').readlines()
    changed = open(changed_file, 'r').readlines()
    for i in range( min(len(original), len(changed)) ):
        if original[i] != changed[i]:
            # print(f'line {i+1} is different')
            if 'skip mutate' in original[i]:
                print(f'line {i+1} is skipped by comment')
                return  True
    return False


if __name__ == '__main__':
    original_file = os.environ['MUTATE_ORIGINAL']
    changed_file = os.environ['MUTATE_CHANGED']
    if is_mutation_skipped_by_comment(original_file, changed_file):
        exit(1)
    else:
        exit(0)

    