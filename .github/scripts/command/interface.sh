#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

test_list_large_dir()
{
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    local files_count=100000
    if [[ "$META_URL" == redis://* ]]; then
        files_count=1300000
    fi
    ./juicefs mdtest $META_URL /test --depth 0 --dirs 1 --files $files_count --threads 1
    du /jfs/test & du_pid=$!
    sleep 2
    kill -INT $du_pid || true
    wait $du_pid || true
    if ! [ -d "/jfs/test" ]; then
        echo >&2 "<FATAL>: directory /jfs/test is not accessible after ls interruption"
        exit 1
    fi
}

test_deep_nested_dirs() {
    prepare_test
    ./juicefs format $META_URL myjfs
    ./juicefs mount -d $META_URL /jfs
    dir="/juicefs1/test"
    for i in $(seq 1 100); do
        dir="$dir/dir$i"
        mkdir -p "$dir"
        echo "content$i" > "$dir/file$i"
    done
    max_jobs=10
    for i in $(seq 1 50); do
        nested_dir="/juicefs1/test"
        for j in $(seq 1 $i); do
            nested_dir="$nested_dir/dir$j"
        done
        ls "$nested_dir" > /dev/null 2>&1 &
        if (( $(jobs -p | wc -l) >= max_jobs )); then
            wait -n
        fi
    done
    wait
    file_count=$(find /juicefs1/test -type f | wc -l)
    if [[ $file_count -ne 100 ]]; then
        echo "File number error： $file_count"
        return 1
    fi
    for i in $(seq 1 100); do
        nested_dir="/juicefs1/test"
        for j in $(seq 1 $i); do
            nested_dir="$nested_dir/dir$j"
        done
        expected_content="content$i"
        actual_content=$(cat "$nested_dir/file$i" 2>/dev/null)
        if [[ "$actual_content" != "$expected_content" ]]; then
            echo "expect: '$expected_content'，actual: '$actual_content'"
            return 1
        fi
    done
    return 0
}


source .github/scripts/common/run_test.sh && run_test $@

