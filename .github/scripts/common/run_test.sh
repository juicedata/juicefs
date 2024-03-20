#!/bin/bash -e
run_one_test()
{
    test=$1
    test=${test%%(*}
    echo -e "\033[0;34mStart Test: $test\033[0m"
    START_TIME=$(date +%s)    
    set +e 
    ( set -e; "${test}" )
    EXIT_STATUS=$?
    set -e
    echo $test exit with $EXIT_STATUS
    END_TIME=$(date +%s)
    ELAPSED_TIME=$((END_TIME - START_TIME))
    if [[ $EXIT_STATUS -eq 0 ]]; then
        echo -e "\033[0;34mFinish Test: $test in $ELAPSED_TIME seconds\033[0m"
    else
        echo -e "\033[0;31mTest Failed: $test($0) in $ELAPSED_TIME seconds\033[0m"
        exit 1
    fi
}

run_test(){
    START_TIME_ALL=$(date +%s) 
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
        if [[ -z "$tests" ]]; then
            echo -e "\033[0;31mNo test function found in $0\033[0m"
        else
            for test in ${tests}; do
                run_one_test $test
            done
        fi
    fi
    END_TIME_ALL=$(date +%s)
    ELAPSED_TIME_ALL=$((END_TIME_ALL - START_TIME_ALL))
    echo -e "\033[0;34mAll tests passed in $ELAPSED_TIME_ALL seconds\033[0m"
}