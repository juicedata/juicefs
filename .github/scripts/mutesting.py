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
    for changed_file in list_of_files:
        timestamp_str = time.strftime(  '%m/%d/%Y :: %H:%M:%S',
                                    time.gmtime(os.path.getmtime(changed_file))) 
        print(timestamp_str, ' -->', changed_file) 
        os.environ['MUTATE_CHANGED'] = changed_file
        os.environ['GOMUTESTING_DIFF'] = run_cmd(['diff', '-u', original_file, changed_file])
        ret = os.system('.github/scripts/mutest.sh')
        print(f'return code {ret}')


def run_cmd(cmd):
    try:
        output = subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except subprocess.CalledProcessError as e:
        print(f'<FATAL>: subprocess run error, return code: {e.returncode} , error message: {e.output.decode()}')
        raise Exception('subprocess run error')
    print(f'run_cmd return code: {output.returncode}, output: {output.stdout.decode()}')
    return output.stdout.decode()

if __name__ == '__main__':
    mutation_dir = os.environ['MUTATION_DIR']
    print(f'mutation dir is {mutation_dir}')
    original_file = os.environ['MUTATE_ORIGINAL']
    print(f'original file is {original_file}')
    do_mutate_test(mutation_dir, original_file)