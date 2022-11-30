#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import glob
import os

def do_mutate_test(mutation_dir, index, total):
    list_of_files = filter( os.path.isfile, glob.glob(mutation_dir + '/*') )
    list_of_files = sorted( list_of_files, key = os.path.getmtime)
    stats = {'passed':0, 'failed':0, 'compile_error':0, 'out_of_coverage':0, 'skip_by_comment':0}
    count = int(len(list_of_files)/total) + 1
    start = index*count
    end = start + count
    print(f'count:{count}, start:{start}, end:{end}')
    if end > len(list_of_files):
        end = len(list_of_files)
    for changed_file in list_of_files[start:end]:
        # timestamp_str = time.strftime(  '%m/%d/%Y :: %H:%M:%S',
        #                             time.gmtime(os.path.getmtime(changed_file))) 
        # print(timestamp_str, ' -->', changed_file) 
        os.environ['MUTATE_PACKAGE'] = ''
        os.environ['MUTATE_CHANGED'] = changed_file
        print('-----------------------------------------------------------------')
        ret = os.system('.github/scripts/mutest.sh') >> 8
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
    return stats

if __name__ == '__main__':
    mutation_dir = os.path.join(os.environ['MUTATION_DIR'], os.environ['PACKAGE_PATH'])
    print(f'mutation dir is {mutation_dir}')
    original_file = os.environ['MUTATE_ORIGINAL']
    print(f'original file is {original_file}')
    if not os.environ['JOB_INDEX']:
        index = 0
    else:
        index = int(os.environ['JOB_INDEX'])-1
    total = int(os.environ['JOB_TOTAL'])
    stats = do_mutate_test(mutation_dir, index, total)
    print(stats)