## JuiceFS preadv/pwritev Test Result Summary

**Test Environment**: JuiceFS 1.4-beta2, kernel 6.8.0-55-generic

### Overview: 12 PASS / 0 FAIL / 1 SKIP

| Test Suite | Result | Details |
|------------|--------|---------|
| **Basic preadv/pwritev** | ✅ 8/8 | All passed |
| **preadv2/pwritev2 Flags** | ✅ 1/2 | 1 item SKIPPED |
| **O_DIRECT + preadv/pwritev** | ✅ 3/3 | All passed |

---

### Detailed Support Notes

1. **First**

    The kernel VFS preprocesses the `preadv/pwritev` interface by handling multiple `iovec` entries and forwarding them through normal read/write paths to JuiceFS. Applications can still benefit from fewer syscall invocations and fewer user-kernel context switches.

2. **Basic Capability**

    The basic semantics of `preadv/pwritev` are covered and passed in `test_basic.c`, mainly validating:

    - Read/write length and data consistency across multiple `iovec` entries
    - Correct read/write behavior with a specified `offset`
    - Boundary behavior (partial reads, EOF returning `0`)
    - Special `iovec` cases (for example, zero-length entries)

3. **Advanced Flag Support**

    - `RWF_APPEND` is fully supported
    - `RWF_NOWAIT` is explicitly not supported and returns `EOPNOTSUPP`.
    - `RWF_HIPRI` can be called, but has no practical effect (high-priority polling depends on the `iopoll` interface, which is currently unsupported by `FUSE`, so it still follows the normal I/O path).
    - `RWF_SYNC/RWF_DSYNC` are partially supported (the kernel handles these flags by issuing an `fsync` request after `write`; JuiceFS does not distinguish between the two and handles both in a unified sync mode).

4. **`O_DIRECT` Test Notes**

    Current cases cover both buffer I/O and direct I/O. Direct I/O currently includes:

    - `O_DIRECT + preadv` with aligned buffers (verifies normal reads and data consistency)
    - `O_DIRECT + pwritev` with aligned buffers (verifies normal writes and data consistency)

### Test Code Location

```text
test/preadv_test/
├── common.h          # common header
├── test_basic.c      # basic function tests (8 cases currently)
├── test_flags.c      # preadv2/pwritev2 flags tests (2 cases currently)
├── test_odirect.c    # O_DIRECT tests (3 cases currently)
├── Makefile
└── preadv_test.sh
```
