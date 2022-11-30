#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import glob
import os
import re
import subprocess
import time


def do_mutate_test(mutation_dir, original_file):
    list_of_files = filter( os.path.isfile, glob.glob(mutation_dir + '/*') )
    list_of_files = sorted( list_of_files, key = os.path.getmtime)
    for file_path in list_of_files:
        timestamp_str = time.strftime(  '%m/%d/%Y :: %H:%M:%S',
                                    time.gmtime(os.path.getmtime(file_path))) 
        print(timestamp_str, ' -->', file_path) 
        os.environ['MUTATE_CHANGED'] = file_path
        os.environ['GOMUTESTING_DIFF'] = 'diff.......'
        ret = os.system('.github/scripts/mutest.sh')
        print(f'return code {ret}')

if __name__ == '__main__':
    mutation_dir = os.environ['MUTATION_DIR']
    print(f'mutation dir is {mutation_dir}')
    do_mutate_test(mutation_dir)