#!/bin/bash

# This exec script implements
# - the replacement of the original file with the mutation,
# - the execution of all tests originating from the package of the mutated file,
# - and the reporting if the mutation was killed.

if [ -z ${MUTATE_CHANGED+x} ]; then echo "MUTATE_CHANGED is not set"; exit 1; fi
if [ -z ${MUTATE_ORIGINAL+x} ]; then echo "MUTATE_ORIGINAL is not set"; exit 1; fi
if [ -z ${MUTATE_PACKAGE+x} ]; then echo "MUTATE_PACKAGE is not set"; exit 1; fi
if [ -z ${COVERAGE_FILE+x} ]; then echo "COVERAGE_FILE is not set"; exit 1; fi
if [ -z ${TEST_FILE_NAME+x} ]; then echo "TEST_FILE_NAME is not set"; exit 1; fi
if [ -z ${PACKAGE_PATH+x} ]; then echo "PACKAGE_PATH is not set"; exit 1; fi

function clean_up {
	if [ -f $MUTATE_ORIGINAL.tmp ];
	then
		mv $MUTATE_ORIGINAL.tmp $MUTATE_ORIGINAL
	fi
}

function sig_handler {
	clean_up

	exit $GOMUTESTING_RESULT
}
trap sig_handler SIGHUP SIGINT SIGTERM

export MUTATE_TIMEOUT=${MUTATE_TIMEOUT:-10}

if [ -n "$TEST_RECURSIVE" ]; then
	TEST_RECURSIVE="/..."
fi

export GOMUTESTING_DIFF=$(diff -u $MUTATE_ORIGINAL $MUTATE_CHANGED)
if [ -z "$GOMUTESTING_DIFF" ]; then
	echo "mutate file is the same as original file", $MUTATE_CHANGED
	exit 100
fi

python3 .github/scripts/mutate/check_coverage.py

if [ $? -ne 0 ]; then
	echo "mutate is out of code coverage", $MUTATE_CHANGED
	exit 101
fi

python3 .github/scripts/mutate/check_skip_by_comment.py
if [ $? -ne 0 ]; then
	echo "mutate is skipped by comment", $MUTATE_CHANGED
	exit 102
fi

test_cases=$(python3 .github/scripts/mutate/parse_test_cases.py)
if [ $? -ne 0 ]; then
	echo "no test cases in test file ", $TEST_FILE_NAME
	exit 103
fi

mv $MUTATE_ORIGINAL $MUTATE_ORIGINAL.tmp
cp $MUTATE_CHANGED $MUTATE_ORIGINAL
echo "------------------------------------------------------------------------"
echo "Start unit test with: $MUTATE_CHANGED"
go test ./$PACKAGE_PATH/...  -run "$test_cases" -v -cover -count=1 -timeout=5m 
# GOMUTESTING_TEST=$(go test -timeout $(printf '%ds' $MUTATE_TIMEOUT) $MUTATE_PACKAGE$TEST_RECURSIVE 2>&1)
export GOMUTESTING_RESULT=$?


if [ "$MUTATE_DEBUG" = true ] ; then
	echo "$GOMUTESTING_TEST"
fi

clean_up

case $GOMUTESTING_RESULT in
0) # tests passed -> FAIL
	echo "$GOMUTESTING_DIFF"
	echo "tests passed -> FAIL"
	exit 1
	;;
1) # tests failed -> PASS
	echo "$GOMUTESTING_DIFF"
	echo "tests failed -> PASS"
	exit 0
	;;
2) # did not compile -> SKIP
	if [ "$MUTATE_VERBOSE" = true ] ; then
		echo "Mutation did not compile"
	fi

	if [ "$MUTATE_DEBUG" = true ] ; then
		echo "$GOMUTESTING_DIFF"
	fi
	echo "did not compile -> SKIP"
	exit 2
	;;
3) # mutation is out of coverage -> SKIP
	echo "mutation is out of coverage -> SKIP"
	echo "$GOMUTESTING_DIFF"

	exit $GOMUTESTING_RESULT
	;;
4) # check coverage failed -> SKIP
	echo "check coverage failed -> SKIP"
	echo "$GOMUTESTING_DIFF"

	exit $GOMUTESTING_RESULT
	;;

*) # Unkown exit code -> SKIP
	echo "Unknown exit code"
	echo "$GOMUTESTING_DIFF"

	exit $GOMUTESTING_RESULT
	;;
esac