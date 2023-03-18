#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import glob
import json
import os
import sys
from tkinter import Tcl

def do_mutate_test(mutation_dir, index, total):
    print(f'mutation dir is {mutation_dir}, inde is {index}, total is {total}', file=sys.stderr)
    # os.system(f'ls -l {mutation_dir}')
    list_of_files = Tcl().call('lsort', '-dict', glob.glob(mutation_dir + '/*.go.*') )
    if len(list_of_files) > 0 and 'original' in list_of_files[-1]:
        list_of_files = list_of_files[:-1]
    # print('\n'.join(list_of_files), file=sys.stderr)
    stats = {'passed':0, 'failed':0, 'compile_error':0, 'out_of_coverage':0, 'skip_by_comment':0, 'others':0, 'total':0}
    count = int(len(list_of_files)/total) + 1
    start = index*count
    end = start + count
    print(f'count:{count}, start:{start}, end:{end}', file=sys.stderr)
    if end > len(list_of_files):
        end = len(list_of_files)
    for changed_file in list_of_files[start:end]:
        # timestamp_str = time.strftime(  '%m/%d/%Y :: %H:%M:%S',
        #                             time.gmtime(os.path.getmtime(changed_file))) 
        # print(timestamp_str, ' -->', changed_file) 
        os.environ['MUTATE_CHANGED'] = changed_file
        ret = os.system('.github/scripts/mutate/mutest.sh') >> 8
        if ret == 0:
            stats['passed'] += 1
        elif ret == 1:
            stats['failed'] += 1
        elif ret == 2:
            stats['compile_error'] += 1
        elif ret == 101:
            stats['out_of_coverage'] += 1
        elif ret == 102:
            stats['skip_by_comment'] += 1
        else:
            stats['others'] += 1
        stats['total'] += 1
    if stats['passed'] + stats['failed'] == 0:
        stats['score'] = 1.0
    else:
        stats['score'] = stats['passed'] / (stats['passed'] + stats['failed'])
    return stats

if __name__ == '__main__':
    os.environ['MUTATE_PACKAGE'] = ''
    mutation_dir = os.path.join(os.environ['MUTATION_DIR'], os.environ['PACKAGE_PATH'])
    print(f'mutation dir is {mutation_dir}', file=sys.stderr)
    original_file = os.environ['MUTATE_ORIGINAL']
    print(f'original file is {original_file}', file=sys.stderr)
    if not os.environ['JOB_INDEX']:
        index = 0
    else:
        index = int(os.environ['JOB_INDEX'])-1
    total = int(os.environ['JOB_TOTAL'])
    stats = do_mutate_test(mutation_dir, index, total)
    print(stats)
    stat_result_file = os.environ['STAT_RESULT_FILE']
    print(f'stat result file is {stat_result_file}', file=sys.stderr)
    with open(stat_result_file, "w") as f:
        json.dump(stats, f)