# Performance Test Cases

This directory contains performance test cases for the JuiceFS performance regression testing framework.

## Adding a New Test Case

To add a new performance test case:

1. Create a new `.sh` file in this directory (e.g., `my_test.sh`)
2. Make it executable: `chmod +x my_test.sh`
3. Define the required functions and variables (see template below)
4. The test will automatically run in the CI workflow

## Test Case Template

```bash
#!/bin/bash

# Test configuration
THRESHOLD=20                  # Performance regression threshold (%)
COMPARISON_MODE="lower_is_better"  # or "higher_is_better"

# Prepare test environment
prepare() {
    # Setup steps (e.g., start services, create directories)
    # Parameter $1 contains "BASELINE" or "CURRENT" to distinguish versions
    echo "Preparing test..."
}

# Execute the performance test
run_test() {
    # Run your benchmark here
    # Output goes to stdout and will be saved to a result file
    echo "Running benchmark..."
}

# Parse the performance value from test output
parse_result() {
    # Extract numeric performance value from test output
    # Input comes from stdin (the output of run_test)
    # Must output a single numeric value
    grep -oE '[0-9]+\.[0-9]+' | head -1
}

# Clean up after test
cleanup() {
    # Cleanup steps (e.g., stop services, remove files)
    echo "Cleaning up..."
}
```

## Test Case Variables

- `THRESHOLD`: Maximum allowed performance regression percentage
- `COMPARISON_MODE`:
  - `lower_is_better`: For metrics where lower values are better (e.g., time, latency)
  - `higher_is_better`: For metrics where higher values are better (e.g., throughput, IOPS)

## Test Case Functions

All functions are required:

1. **prepare()**: Sets up the test environment
   - Receives one parameter: "BASELINE" or "CURRENT"
   - Use this to switch between baseline and test binaries

2. **run_test()**: Executes the actual performance test
   - All output goes to stdout
   - Should be idempotent (can run multiple times)

3. **parse_result()**: Extracts the performance metric
   - Receives test output via stdin
   - Must output a single numeric value (e.g., "2.345")

4. **cleanup()**: Cleans up after the test
   - Should restore the system to a clean state
   - Called even if the test fails

## Examples

See the existing test cases in this directory:

- `simple_test.sh`: Minimal example with no external dependencies
- `mdtest_example.sh`: Real-world example testing JuiceFS metadata operations

## Performance Regression Test Cases

These test cases correspond to the scenarios from the legacy `mdtest_fio.sh` script, refactored to work with the modular `run_and_compare.sh` framework.

### Metadata Tests (mdtest)

| Test Case | Scenario | Description | Metric | Mode |
|-----------|----------|-------------|--------|------|
| `mdtest_mpi_tree.sh` | Scenario 1 | MPI mdtest with tree structure (`-b 3 -z 1 -I 1000`) | Avg max ops/sec | higher_is_better |
| `mdtest_mpi_flat.sh` | Scenario 2 | MPI mdtest with flat files (`-F -w 102400 -I 10000 -z 0`) | Avg max ops/sec | higher_is_better |
| `mdtest_builtin.sh` | Scenario 3 | JuiceFS built-in mdtest (`--depth 3 --dirs 10 --files 10`) | files/s | higher_is_better |
| `mdtest_builtin_largedir.sh` | Scenario 10 | Large directory test (`--files 50000`) | files/s | higher_is_better |
| `mdtest_list_largedir.sh` | Scenario 11 | List large directory (`ls -l` on 50000 files) | seconds | lower_is_better |

### FIO Benchmarks

| Test Case | Scenario | Description | Metric | Mode |
|-----------|----------|-------------|--------|------|
| `fio_seq_write_64k.sh` | Scenario 4 | Sequential write, 64k blocks, 8 threads | IOPS | higher_is_better |
| `fio_rand_write_64k.sh` | Scenario 5 | Random write, 64k blocks, 8 threads | IOPS | higher_is_better |
| `fio_seq_read_4k.sh` | Scenario 6 | Sequential read, 4k blocks, 8 threads | IOPS | higher_is_better |
| `fio_rand_read_4k.sh` | Scenario 7 | Random read, 4k blocks, 8 threads | IOPS | higher_is_better |
| `fio_seq_write_1m.sh` | Scenario 8 | Sequential write, 1m blocks, 8 threads, 8 files | IOPS | higher_is_better |
| `fio_seq_read_1m.sh` | Scenario 9 | Sequential read, 1m blocks, 8 threads, 8 files | IOPS | higher_is_better |

### AI Format Benchmarks

These test cases benchmark AI training file format performance using `ai_format_benchmark.py`.
Each test case runs a specific format benchmark and reports average throughput (MB/s).
Python venv setup is shared via `ai_common.sh` and only runs once across all AI test cases.

| Test Case | Description | Metric | Mode |
|-----------|-------------|--------|------|
| `ai_lmdb.sh` | LMDB dataset write/read (single & multi process) | Avg throughput MB/s | higher_is_better |
| `ai_pytorch_weights.sh` | PyTorch .pt/.pth model weights write/read | Avg throughput MB/s | higher_is_better |
| `ai_tensorflow_h5.sh` | TensorFlow HDF5 model weights write/read | Avg throughput MB/s | higher_is_better |
| `ai_huggingface_bin.sh` | HuggingFace .bin model weights write/read | Avg throughput MB/s | higher_is_better |
| `ai_tensorflow_checkpoint.sh` | TensorFlow checkpoint write/read | Avg throughput MB/s | higher_is_better |
| `ai_hdf5_dataset.sh` | HDF5 dataset write/read | Avg throughput MB/s | higher_is_better |
| `ai_parquet.sh` | Parquet dataset write/read | Avg throughput MB/s | higher_is_better |

### Result Parsing

Each test case has a `parse_result()` function that extracts a single numeric value from the test output:

- **MPI mdtest**: Averages the max ops/sec values of non-filtered operations (excludes File read, File removal, Tree removal, Tree creation)
- **Built-in mdtest**: Extracts `files/s` from output like `processed N files (X files/s)`
- **List directory**: Extracts real time in seconds from `time` command output
- **FIO**: Extracts IOPS value, handling k/M/G suffixes (e.g., `IOPS=12.3k` → `12300`)

## Workflow Behavior

### Automatic Triggers (push/PR/schedule)
All `.sh` files in this directory are automatically executed in alphabetical order.

### Manual Triggers (workflow_dispatch)
You can specify which test cases to run using the `test_cases` input parameter.

## Best Practices

1. **Keep tests focused**: Each test should measure one specific aspect of performance
2. **Make tests reproducible**: Ensure cleanup is thorough so tests don't affect each other
3. **Use reasonable thresholds**: Set THRESHOLD based on expected variance (typically 10-20%)
4. **Add comments**: Explain what the test measures and why
5. **Test locally first**: Use `run_and_compare.sh` to test locally before committing

## Running Tests Locally

```bash
# Run a specific test case
.github/scripts/perf/run_and_compare.sh simple_test.sh

# Run multiple test cases
.github/scripts/perf/run_and_compare.sh "simple_test.sh,mdtest_example.sh"
```

## Troubleshooting

**Test fails with "divide by zero":**
- Check that `parse_result()` returns a valid numeric value
- Ensure the baseline performance value is not 0

**Test shows bc syntax errors:**
- Verify that functions only output to stderr (except for the final value)
- Use `>&2` to redirect messages: `echo "Status message" >&2`

**Test results are inconsistent:**
- Check that `cleanup()` properly resets the environment
- Ensure external factors (CPU load, network) are minimized
- Consider increasing the threshold to account for natural variance
