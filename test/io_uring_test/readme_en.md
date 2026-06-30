## JuiceFS io_uring Test Results Summary

**Test Environment**: Linux kernel 6.8-generic, JuiceFS 1.4-beta2

### Overview: 12 test cases in total

| Test Suite | Case Count | Description |
|---------|--------|------|
| **Basic I/O** | 4 | Basic read/write, readv/writev, batched submission, consistency checks |
| **Fixed Buffers** | 3 | Fixed buffer registration, read/write, cross-index validation |
| **Registered Files** | 3 | Fixed file table registration with fixed-buffer read/write |
| **Splice** | 2 | file->pipe, pipe->file, tee |

---

### Detailed Support Notes

1. **Overview**

    This suite validates the availability and semantic correctness of Linux io_uring requests in JuiceFS scenarios, covering major paths from regular I/O to advanced opcodes.

2. **Basic Capabilities**

    `test_basic_io.c` covers and verifies:

    - Basic correctness of `IORING_OP_READ/WRITE`
    - Vector I/O semantics of `IORING_OP_READV/WRITEV`
    - Completion mapping (`user_data`) after batched SQE submissions
    - Write-then-read consistency checks

    > Basic io requests still reach JuiceFS as regular read/write requests, and can benefit from io_uring performance optimizations.

3. **Fixed Resource Capabilities (buffer/file registration)**

    - `fixed_buffers` means pre-registering a set of buffers with io_uring to improve address resolution efficiency for high-frequency I/O
    - `file registration` means pre-registering a set of fds with io_uring to reuse cached file structures and reduce overhead such as `fget` locking

    > Both optimizations are handled at the io_uring layer. No special FUSE-layer adaptation is required, and JuiceFS can benefit directly.

4. **Advanced Feature Notes**

    | Feature | Details |
    |---|---|
    | `IORING_OP_SPLICE` | Transfers a file descriptor directly from one address space to another, without going through a user-space buffer |
    | `IORING_OP_NOP` | No operation; used to pad the io_uring queue, triggers no I/O |
    | `IORING_OP_TIMEOUT` | Sets a timeout, used to wait for I/O operations to complete |
    | `IORING_OP_TIMEOUT_REMOVE` | Removes a timeout, used to cancel the timeout set for waiting on I/O operations |
    | `IORING_OP_LINK` | Links a file descriptor to another file descriptor, without going through a user-space buffer |
    | `IORING_OP_PROVIDE_BUFFERS` | Provides fixed buffers, improving address resolution efficiency for high-frequency io |
    | `IORING_OP_SYNC_FILE_RANGE` | Syncs the pagecache of a given file range to disk |

    > All of the above features are implemented by the Linux kernel. No special FUSE-layer adaptation is required, and JuiceFS can benefit directly.

    **`IORING_SETUP_IOPOLL`**

    It switches the ring's I/O completion mode from interrupt-driven to polling (poll) driven. It relies on the underlying iopoll interface and is mostly used for direct read/write on block devices; the vast majority of filesystems are not involved, and JuiceFS does not support this feature.

### How To Run

### Check io_uring availability

```bash
cat /proc/sys/kernel/io_uring_disabled
# 0 means enabled
# if non-zero, run: echo 0 | sudo tee /proc/sys/kernel/io_uring_disabled
```

### Install liburing

```bash
# Ubuntu/Debian
sudo apt install liburing-dev

# CentOS/RHEL
sudo yum install liburing-devel

# Or build from source
git clone https://github.com/axboe/liburing.git
cd liburing && make && sudo make install
```

### Build test binaries

```bash
cd test/io_uring_test
make
```

### Run the full test suite

```bash
./run_tests.sh /path/to/juicefs/mountpoint
```

Notes:

- It is recommended to pass the target filesystem mountpoint (for example, a JuiceFS mount path)
- If no argument is provided, the default working directory is `/tmp/io_uring_test`
- To verify whether io_uring is disabled, check `/proc/sys/kernel/io_uring_disabled`

### Test Code Locations

```text
test/io_uring_test/
├── common.h                 # Shared header and test helpers
├── test_basic_io.c          # Basic I/O (4 cases)
├── test_fixed_buffers.c     # Fixed buffers (3 cases)
├── test_registered_files.c  # Registered files (3 cases)
├── test_splice.c            # Splice/Tee (2 cases)
├── run_tests.sh
└── Makefile
```
