#!/bin/bash -e

# Performance Test Runner and Comparator
# This script runs performance tests for both baseline and test branches,
# then compares the results to detect regressions.
#
# Usage:
#   ./run_and_compare.sh <test_cases> <baseline_branch> <test_branch>
#
# Example:
#   ./run_and_compare.sh "mdtest_example.sh" release-5.2 main
#
# Environment variables:
#   TEST_CASES       - Comma-separated list of test case files (default: mdtest_example.sh)
#   BASELINE_BRANCH  - Baseline branch name (default: release-5.2)
#   TEST_BRANCH      - Test branch name (default: main)
#   RESULTS_DIR      - Directory for results (default: perf_results)
#   BIN_DIR          - Directory containing binaries (default: bin)

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
TEST_CASES="${1:-${TEST_CASES:-mdtest_example.sh}}"
BASELINE_BRANCH="${2:-${BASELINE_BRANCH:-release-5.2}}"
TEST_BRANCH="${3:-${TEST_BRANCH:-main}}"
RESULTS_DIR="${RESULTS_DIR:-perf_results}"
BIN_DIR="${BIN_DIR:-bin}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_CASES_DIR="${SCRIPT_DIR}/test_cases"

usage() {
    cat << EOF
Usage: $0 [TEST_CASES] [BASELINE_BRANCH] [TEST_BRANCH]

Run performance tests and compare baseline vs test branch results.

ARGUMENTS:
    TEST_CASES       Comma-separated list of test case files (default: mdtest_example.sh)
    BASELINE_BRANCH  Baseline branch name (default: release-5.2)
    TEST_BRANCH      Test branch name (default: main)

ENVIRONMENT VARIABLES:
    RESULTS_DIR      Directory for results (default: perf_results)
    BIN_DIR          Directory containing binaries (default: bin)

EXAMPLES:
    $0 mdtest_example.sh release-5.2 main
    $0 "test1.sh,test2.sh" release-5.2 feature-branch
    
    # Using environment variables
    TEST_CASES="mdtest_example.sh" BASELINE_BRANCH=release-5.2 TEST_BRANCH=main $0

EOF
    exit 0
}

# Run a performance test for a specific version
# Arguments:
#   $1 - version label (e.g., "BASELINE", "TEST")
#   $2 - branch name
#   $3 - binary subdirectory (e.g., "baseline", "test")
# Returns:
#   Performance value via stdout
run_version_test() {
  local test_name="$1"
  local version_label="$2"
  
  echo "" >&2
  echo -e "${YELLOW}>>> Running $version_label for $test_name...${NC}" >&2
  
  local result_file="$RESULTS_DIR/${test_name}_${version_label}.result"
  local value_file="$RESULTS_DIR/${test_name}_${version_label}.value"
  
  # Run preparation
  if declare -f prepare >/dev/null 2>&1; then
    prepare $version_label >&2 || { echo -e "${RED}$version_label prep failed${NC}" >&2; exit 1; }
  fi
  
  # Run test with timing
  local start_time=$(date +%s)
  run_test 2>&1| tee "$result_file" >&2 || { echo -e "${RED}$version_label test failed${NC}" >&2; exit 1; }
  local end_time=$(date +%s)
  local duration=$((end_time - start_time))
  echo "$duration" > "$RESULTS_DIR/${test_name}_${version_label}.duration"
  
  # Parse result
  local perf_value
  perf_value=$(parse_result < "$result_file")
  echo "$perf_value" > "$value_file"
  echo "$version_label performance: $perf_value (duration: ${duration}s)" >&2
  
  # Cleanup
  if declare -f cleanup >/dev/null 2>&1; then
    cleanup >&2 || true
  fi
  
  # Return the performance value (only numeric value to stdout)
  echo "$perf_value"
}

# Show help if requested
if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    usage
fi

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Performance Test Runner${NC}"
echo -e "${GREEN}========================================${NC}"
echo "Test cases:      $TEST_CASES"
echo "Results dir:     $RESULTS_DIR"
echo -e "${GREEN}========================================${NC}"
echo ""

# Parse test cases (comma-separated)
IFS=',' read -ra CASES <<< "$TEST_CASES"

overall_status=0

# Run each test case: baseline first, then test, then compare
for test_case in "${CASES[@]}"; do
  test_case=$(echo "$test_case" | xargs) # trim whitespace
  
  echo "=========================================="
  echo "Processing test case: $test_case"
  echo "=========================================="
  
  # Source the test case to get configuration
  if [[ ! -f "$TEST_CASES_DIR/$test_case" ]]; then
    echo -e "${RED}ERROR: Test case file not found: $TEST_CASES_DIR/$test_case${NC}"
    overall_status=1
    mkdir -p "$RESULTS_DIR"
    echo "${test_case},-,-,-,ERROR,-,-,-" >> "$RESULTS_DIR/summary.csv"
    continue
  fi
  
  source "$TEST_CASES_DIR/$test_case"
  
  mkdir -p "$RESULTS_DIR"
  test_name=$(basename "$test_case" .sh)
  # Run baseline version test
  baseline_value=$(run_version_test $test_name "BASELINE")
  
  # Run current version test
  current_value=$(run_version_test $test_name "CURRENT")
  echo "base_line: $baseline_value, current: $current_value" >&2

  # ===== Compare results =====
  echo ""
  echo -e "${YELLOW}>>> Comparing results for $test_case...${NC}"
  echo "====================================="
  
  # Check for zero baseline to avoid division by zero
  if (( $(echo "$baseline_value == 0" | bc -l) )); then
    echo -e "${RED}❌ ERROR: Baseline value is zero, cannot calculate percentage${NC}"
    overall_status=1
    baseline_duration=$(cat "$RESULTS_DIR/${test_name}_BASELINE.duration" 2>/dev/null || echo "N/A")
    current_duration=$(cat "$RESULTS_DIR/${test_name}_CURRENT.duration" 2>/dev/null || echo "N/A")
    echo "${test_name},${baseline_value},${current_value},-,ERROR,${COMPARISON_MODE},${baseline_duration},${current_duration}" >> "$RESULTS_DIR/summary.csv"
    continue
  fi
  
  # Calculate percentage difference
  diff_pct=$(echo "scale=2; ($current_value - $baseline_value) * 100 / $baseline_value" | bc -l)
  echo "Difference: ${diff_pct}%"
  
  # Check for regression based on comparison mode
  threshold=${THRESHOLD:-20}
  
  test_status="PASS"
  if [[ "$COMPARISON_MODE" == "lower_is_better" ]]; then
    # Lower is better (e.g., execution time)
    if (( $(echo "$diff_pct > $threshold" | bc -l) )); then
      echo -e "${RED}❌ REGRESSION: Test branch is ${diff_pct}% slower than baseline (threshold: ${threshold}%)${NC}"
      overall_status=1
      test_status="FAIL"
    else
      echo -e "${GREEN}✅ PASS: No regression detected${NC}"
    fi
  elif [[ "$COMPARISON_MODE" == "higher_is_better" ]]; then
    # Higher is better (e.g., throughput)
    if (( $(echo "$diff_pct < -$threshold" | bc -l) )); then
      echo -e "${RED}❌ REGRESSION: Test branch is ${diff_pct}% slower than baseline (threshold: ${threshold}%)${NC}"
      overall_status=1
      test_status="FAIL"
    else
      echo -e "${GREEN}✅ PASS: No regression detected${NC}"
    fi
  fi
  
  # Write summary data
  baseline_duration=$(cat "$RESULTS_DIR/${test_name}_BASELINE.duration" 2>/dev/null || echo "N/A")
  current_duration=$(cat "$RESULTS_DIR/${test_name}_CURRENT.duration" 2>/dev/null || echo "N/A")
  echo "${test_name},${baseline_value},${current_value},${diff_pct},${test_status},${COMPARISON_MODE},${baseline_duration},${current_duration}" >> "$RESULTS_DIR/summary.csv"
  
  echo "=========================================="
  echo ""
done

echo ""
echo -e "${GREEN}========================================${NC}"
if [[ $overall_status -eq 0 ]]; then
  echo -e "${GREEN}✅ All tests passed!${NC}"
else
  echo -e "${RED}❌ Some tests failed!${NC}"
fi
echo -e "${GREEN}========================================${NC}"

exit $overall_status
