#!/bin/bash

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -z "$1" ]; then
    echo "Error: Test directory is required"
    echo "Usage: $0 <test_directory>"
    exit 1
fi

TEST_DIR="$1"

if [ ! -d "$TEST_DIR" ]; then
    echo "Error: Test directory '$TEST_DIR' does not exist"
    echo "Usage: $0 <test_directory>"
    exit 1
fi

echo "============================================"
echo "  preadv/pwritev Test Suite for JuiceFS"
echo "============================================"
echo ""
echo "Test directory: $TEST_DIR"
echo "Kernel version: $(uname -r)"
echo ""

WORK_DIR="$TEST_DIR/preadv_test_$$"
mkdir -p "$WORK_DIR"

cd "$SCRIPT_DIR"

if [ ! -f test_basic ] || [ ! -f test_flags ] || [ ! -f test_odirect ]; then
    echo "Building test programs..."
    make clean 2>/dev/null || true
    make test_basic test_flags test_odirect
    echo ""
fi

run_test() {
    local name=$1
    local binary=$2
    local dir=$3

    echo "--------------------------------------------"
    echo "Running: $name"
    echo "--------------------------------------------"

    "$binary" "$dir" 2>&1 || true
    echo ""
}

run_test "Basic preadv/pwritev" ./test_basic "$WORK_DIR"
run_test "preadv2/pwritev2 Flags" ./test_flags "$WORK_DIR"
run_test "O_DIRECT + preadv/pwritev" ./test_odirect "$WORK_DIR"

echo "============================================"
echo "  Cleanup"
echo "============================================"
rm -rf "$WORK_DIR"

echo ""
echo "All tests completed."
