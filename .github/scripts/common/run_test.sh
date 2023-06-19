#!/bin/bash
set -e
run_one_test()
{
    test=$1
    test=${test%%(*}
    echo Start Test: $test
    START_TIME=$(date +%s)
    set -o pipefail
    "${test}" | tee /dev/null || EXIT_STATUS=$?
    set +o pipefail
    echo $EXIT_STATUS
    END_TIME=$(date +%s)
    ELAPSED_TIME=$((END_TIME - START_TIME))
    if [[ $EXIT_STATUS -eq 0 ]]; then
        echo Finish Test: $test in $ELAPSED_TIME seconds
    else
        echo -e "\033[0;31mTest Failed: $test of $0 in $ELAPSED_TIME seconds\033[0m"
        exit 1
    fi
}

run_test(){
    if [[ ! -z "$@" ]]; then
        # run test functions passed by arguments
        for test in "$@"; do
            if declare -F "$test" > /dev/null; then
                run_one_test $test
            else
                echo -e "\033[0;31mTest $test was not found in $0\033[0m"
                exit 1
            fi
        done
    else
        # Find and run all test functions
        tests=$(grep -oP '^\s*test_\w+\s*\(\s*\)' "$0")
        for test in ${tests}; do
            run_one_test $test
        done
    fi
}

