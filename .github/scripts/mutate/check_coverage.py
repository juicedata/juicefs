#!/usr/bin/env python
# -*- encoding: utf-8 -*-
import os

def is_mutation_in_coverage(original_file, changed_file, coverage_file):
	coverage = parse_coverage(coverage_file)
	# print(coverage)
	original = open(original_file, 'r').readlines()
	changed = open(changed_file, 'r').readlines()
	for i in range( min(len(original), len(changed)) ):
		if original[i] != changed[i]:
			# print(f'line {i+1} is different')
			if (i+1) not in coverage:
				# print(f'line {i+1} is not in coverage')
				return False
			else:
				# print(f'line {i+1} is in coverage')
				return True
	return True


def parse_coverage(file):
	cov = set()
	with open(file, 'r') as f:
		lines = f.readlines()
		for line in lines[1:]:
			name = line.split(':')[0]
			count = int(line.split(' ')[2])
			if count > 0:
				start_line = int(line.split(':')[1].split(' ')[0].split(',')[0].split('.')[0])
				end_line = int(line.split(':')[1].split(' ')[0].split(',')[1].split('.')[0])
				for i in range(start_line, end_line+1):
					cov.add(i)
	return cov
	
if __name__ == '__main__':
	# MUTATE_ORIGINAL=../cmd/meta/xattr.go MUTATE_CHANGED=../cmd/meta/xattr_copy.go COVERAGE_FILE=xattr-cov.out python3 check_coverage.py
	# MUTATE_ORIGINAL=cmd/meta/xattr.go MUTATE_CHANGED=/var/folders/jz/mvf43cj13sl4l17z1yy8m92h0000gn/T/go-mutesting-3937777628/xattr.go.4 COVERAGE_FILE=cmd/meta/xattr-cov.out python3 scripts/check_coverage.py
	original_file = os.environ['MUTATE_ORIGINAL']
	changed_file = os.environ['MUTATE_CHANGED']
	coverage_file = os.environ['COVERAGE_FILE']
	# print(f'MUTATE_ORIGINAL={original_file} MUTATE_CHANGED={changed_file} COVERAGE_FILE={coverage_file} python3 ../../scripts/check_coverage.py')
	r = is_mutation_in_coverage(original_file, changed_file, coverage_file)
	if r:
		exit(0)
	else:
		exit(3)