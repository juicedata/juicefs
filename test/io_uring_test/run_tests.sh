#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEST_DIR="${1:-/tmp/io_uring_test}"

if [ ! -d "$TEST_DIR" ]; then
    echo "Error: Test directory '$TEST_DIR' does not exist"
    echo "Usage: $0 <test_directory>"
    echo "  test_directory should be on the filesystem you want to test (e.g., JuiceFS mount point)"
    exit 1
fi

echo "============================================"
echo "  io_uring Test Suite for JuiceFS"
echo "============================================"
echo ""
echo "Test directory: $TEST_DIR"
echo "Kernel version: $(uname -r)"
echo ""

if [ -f /proc/sys/kernel/io_uring_disabled ]; then
    io_uring_disabled=$(cat /proc/sys/kernel/io_uring_disabled)
    echo "io_uring_disabled: $io_uring_disabled"
    if [ "$io_uring_disabled" != "0" ]; then
        echo "WARNING: io_uring is disabled! Tests will likely fail."
        echo "Enable with: echo 0 | sudo tee /proc/sys/kernel/io_uring_disabled"
    fi
else
    echo "WARNING: /proc/sys/kernel/io_uring_disabled not found"
fi

echo ""

WORK_DIR="$TEST_DIR/io_uring_test_$$"
mkdir -p "$WORK_DIR"

cd "$SCRIPT_DIR"

if [ ! -f test_basic_io ] || [ ! -f test_fixed_buffers ] || \
   [ ! -f test_registered_files ] || [ ! -f test_splice ] || \
   [ ! -f test_file_ops ] || [ ! -f test_dir_ops ] || [ ! -f test_advanced ]; then
    echo "Building test programs..."
    make clean
    make
    echo ""
fi

TOTAL_PASS=0
TOTAL_FAIL=0
TOTAL_SKIP=0

run_test() {
    local test_name=$1
    local test_binary=$2
    local test_dir=$3

    echo "--------------------------------------------"
    echo "Running: $test_name"
    echo "--------------------------------------------"

    if [ ! -x "$test_binary" ]; then
        echo "  [ERROR] $test_binary not found or not executable"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
        return
    fi

    "$test_binary" "$test_dir" 2>&1 || true
    echo ""
}

echo "============================================"
echo "  Starting Tests"
echo "============================================"
echo ""

run_test "Basic I/O" ./test_basic_io "$WORK_DIR"
run_test "Fixed Buffers" ./test_fixed_buffers "$WORK_DIR"
run_test "Registered Files" ./test_registered_files "$WORK_DIR"
run_test "Splice" ./test_splice "$WORK_DIR"
run_test "File Operations" ./test_file_ops "$WORK_DIR"
run_test "Directory Operations" ./test_dir_ops "$WORK_DIR"
run_test "Advanced Features" ./test_advanced "$WORK_DIR"

echo "============================================"
echo "  Cleanup"
echo "============================================"
rm -rf "$WORK_DIR"

echo ""
echo "All tests completed. Check results above for details."
