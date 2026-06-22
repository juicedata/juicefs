#include "common.h"
#include <fcntl.h>

#ifndef POSIX_FADV_DONTNEED
#define POSIX_FADV_DONTNEED 4
#endif

#ifndef RWF_NOWAIT
#define RWF_NOWAIT 0x00000008
#endif

#ifndef RWF_HIPRI
#define RWF_HIPRI 0x00000001
#endif

#ifndef RWF_DSYNC
#define RWF_DSYNC 0x00000002
#endif

#ifndef RWF_SYNC
#define RWF_SYNC 0x00000004
#endif

#ifndef RWF_APPEND
#define RWF_APPEND 0x00000010
#endif

static const char *test_dir;

/*
 * Test RWF_NOWAIT semantics with two sub-cases:
 *
 * Sub-case A (cold cache):
 *   After POSIX_FADV_DONTNEED, data should not be in page cache.
 *   A filesystem that truly implements RWF_NOWAIT must return EAGAIN
 *   instead of blocking to fetch data from storage.
 *   - EAGAIN  → flag is honoured, data not in cache  (PASS)
 *   - EOPNOTSUPP / EINVAL → filesystem does not support RWF_NOWAIT (SKIP)
 *   - nread >= 0 → flag was silently ignored (data read in blocking mode),
 *                  or cache eviction was not effective; reported as INFO
 *
 * Sub-case B (warm cache):
 *   After a normal read the page should be in cache.
 *   A filesystem that truly implements RWF_NOWAIT must return data
 *   immediately without blocking.
 *   - nread == BLOCK_SIZE → flag is honoured, data served from cache (PASS)
 *   - EOPNOTSUPP / EINVAL → filesystem does not support RWF_NOWAIT (SKIP)
 *   - EAGAIN  → data was not cached despite prior read; unusual, noted
 *
 * If sub-case A returns data AND sub-case B also returns data with the
 * same result as a plain preadv2(flags=0), the flag is almost certainly
 * being silently ignored.
 */
static int test_preadv2_nowait(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv2_nowait", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv2_nowait", "create test file failed");
        return 1;
    }

    /* ------------------------------------------------------------------ */
    /* Sub-case A: cold cache                                               */
    /* ------------------------------------------------------------------ */
    {
        int fd = open(filepath, O_RDONLY);
        if (fd < 0) {
            FAIL("preadv2_nowait[cold]", "open failed: %s", strerror(errno));
            unlink(filepath);
            return 1;
        }

        /* Ask the kernel to drop cached pages for this file. */
        posix_fadvise(fd, 0, 0, POSIX_FADV_DONTNEED);

        char buf[BLOCK_SIZE];
        struct iovec iov = { .iov_base = buf, .iov_len = BLOCK_SIZE };

        ssize_t nread = preadv2(fd, &iov, 1, 0, RWF_NOWAIT);
        int saved_errno = errno;
        close(fd);

        if (nread < 0) {
            if (saved_errno == EOPNOTSUPP || saved_errno == EINVAL) {
                SKIP("preadv2_nowait[cold]",
                     "RWF_NOWAIT not supported by this filesystem "
                     "(errno=%d: %s)", saved_errno, strerror(saved_errno));
                unlink(filepath);
                return 0;
            }
            if (saved_errno == EAGAIN) {
                /*
                 * This is the correct behaviour: the kernel refused to block
                 * because data was not in cache.
                 */
                PASS("preadv2_nowait[cold]: got EAGAIN on cold cache — "
                     "RWF_NOWAIT is honoured");
            } else {
                FAIL("preadv2_nowait[cold]",
                     "unexpected error: %s (errno=%d)",
                     strerror(saved_errno), saved_errno);
                unlink(filepath);
                return 1;
            }
        } else {
            /*
             * Returned data on a cold cache.  Two explanations:
             *   1. POSIX_FADV_DONTNEED had no effect (common on network FS
             *      where the server-side cache is not affected).
             *   2. RWF_NOWAIT is silently ignored and a blocking read
             *      was performed instead.
             * Either way the flag semantics are not verifiable here; report
             * but do not count as PASS.
             */
            printf("  [INFO] preadv2_nowait[cold]: got %zd bytes on cold cache "
                   "— FADV_DONTNEED may be ineffective on this FS, or "
                   "RWF_NOWAIT is silently ignored\n", nread);
        }
    }

    /* ------------------------------------------------------------------ */
    /* Sub-case B: warm cache                                               */
    /* ------------------------------------------------------------------ */
    {
        int fd = open(filepath, O_RDONLY);
        if (fd < 0) {
            FAIL("preadv2_nowait[warm]", "open failed: %s", strerror(errno));
            unlink(filepath);
            return 1;
        }

        /* Warm the cache with a plain blocking read. */
        char warmup[BLOCK_SIZE];
        ssize_t warmup_ret = pread(fd, warmup, BLOCK_SIZE, 0);
        if (warmup_ret != BLOCK_SIZE) {
            FAIL("preadv2_nowait[warm]",
                 "warmup read returned %zd, expected %d: %s",
                 warmup_ret, BLOCK_SIZE, strerror(errno));
            close(fd); unlink(filepath);
            return 1;
        }

        char buf[BLOCK_SIZE];
        struct iovec iov = { .iov_base = buf, .iov_len = BLOCK_SIZE };

        ssize_t nread_nowait  = preadv2(fd, &iov, 1, 0, RWF_NOWAIT);
        int saved_errno = errno;

        /* Also read the same range without the flag for comparison. */
        char buf2[BLOCK_SIZE];
        struct iovec iov2 = { .iov_base = buf2, .iov_len = BLOCK_SIZE };
        ssize_t nread_plain = preadv2(fd, &iov2, 1, 0, 0);

        close(fd);

        if (nread_nowait < 0) {
            if (saved_errno == EOPNOTSUPP || saved_errno == EINVAL) {
                SKIP("preadv2_nowait[warm]",
                     "RWF_NOWAIT not supported by this filesystem "
                     "(errno=%d: %s)", saved_errno, strerror(saved_errno));
            } else if (saved_errno == EAGAIN) {
                printf("  [INFO] preadv2_nowait[warm]: EAGAIN on warm cache "
                       "— cache may have been evicted between warmup and test\n");
            } else {
                FAIL("preadv2_nowait[warm]",
                     "unexpected error: %s (errno=%d)",
                     strerror(saved_errno), saved_errno);
                unlink(filepath);
                return 1;
            }
        } else if (nread_nowait == BLOCK_SIZE) {
            if (nread_plain == BLOCK_SIZE &&
                memcmp(buf, buf2, BLOCK_SIZE) == 0) {
                /*
                 * Data matches a plain read.  This is consistent with two
                 * scenarios:
                 *   (a) RWF_NOWAIT worked correctly (data was in cache).
                 *   (b) RWF_NOWAIT was ignored and a normal read happened.
                 * We cannot distinguish them without kernel instrumentation,
                 * so report the result honestly.
                 */
                printf("  [INFO] preadv2_nowait[warm]: got %zd bytes on warm "
                       "cache (same as plain read) — cannot confirm flag is "
                       "honoured vs silently ignored on this FS\n", nread_nowait);
            } else {
                PASS("preadv2_nowait[warm]: got data on warm cache");
            }
        } else {
            FAIL("preadv2_nowait[warm]",
                 "short read: %zd bytes, expected %d", nread_nowait, BLOCK_SIZE);
            unlink(filepath);
            return 1;
        }
    }

    unlink(filepath);
    return 0;
}

static int test_preadv2_hipri(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv2_hipri", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv2_hipri", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv2_hipri", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv2(fd, &iov, 1, 0, RWF_HIPRI);

    close(fd); unlink(filepath);

    if (nread < 0) {
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("preadv2_hipri", "RWF_HIPRI not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        FAIL("preadv2_hipri", "preadv2 with RWF_HIPRI failed: %s (errno=%d)", strerror(errno), errno);
        return 1;
    }

    if (nread != BLOCK_SIZE) {
        FAIL("preadv2_hipri", "read %zd bytes, expected %d", nread, BLOCK_SIZE);
        return 1;
    }

    PASS("preadv2_hipri (RWF_HIPRI accepted, data read successfully)");
    return 0;
}

static int test_pwritev2_sync(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev2_sync", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev2_sync", "open failed: %s", strerror(errno));
        return 1;
    }

    char buf[BLOCK_SIZE];
    memset(buf, 'S', BLOCK_SIZE);

    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nwritten = pwritev2(fd, &iov, 1, 0, RWF_SYNC);

    if (nwritten < 0) {
        close(fd); unlink(filepath);
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("pwritev2_sync", "RWF_SYNC not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        FAIL("pwritev2_sync", "pwritev2 with RWF_SYNC failed: %s (errno=%d)", strerror(errno), errno);
        return 1;
    }

    if (nwritten != BLOCK_SIZE) {
        FAIL("pwritev2_sync", "wrote %zd bytes, expected %d", nwritten, BLOCK_SIZE);
        close(fd); unlink(filepath);
        return 1;
    }

    char read_buf[BLOCK_SIZE];
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);
    close(fd); unlink(filepath);

    if (nread != BLOCK_SIZE || memcmp(buf, read_buf, BLOCK_SIZE) != 0) {
        FAIL("pwritev2_sync", "data integrity check failed");
        return 1;
    }

    PASS("pwritev2_sync (RWF_SYNC: write+sync completed, data verified)");
    return 0;
}

static int test_pwritev2_dsync(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev2_dsync", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev2_dsync", "open failed: %s", strerror(errno));
        return 1;
    }

    char buf[BLOCK_SIZE];
    memset(buf, 'D', BLOCK_SIZE);

    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nwritten = pwritev2(fd, &iov, 1, 0, RWF_DSYNC);

    if (nwritten < 0) {
        close(fd); unlink(filepath);
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("pwritev2_dsync", "RWF_DSYNC not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        FAIL("pwritev2_dsync", "pwritev2 with RWF_DSYNC failed: %s (errno=%d)", strerror(errno), errno);
        return 1;
    }

    if (nwritten != BLOCK_SIZE) {
        FAIL("pwritev2_dsync", "wrote %zd bytes, expected %d", nwritten, BLOCK_SIZE);
        close(fd); unlink(filepath);
        return 1;
    }

    char read_buf[BLOCK_SIZE];
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);
    close(fd); unlink(filepath);

    if (nread != BLOCK_SIZE || memcmp(buf, read_buf, BLOCK_SIZE) != 0) {
        FAIL("pwritev2_dsync", "data integrity check failed");
        return 1;
    }

    PASS("pwritev2_dsync (RWF_DSYNC: write+dsync completed, data verified)");
    return 0;
}

static int test_preadv2_no_flags(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv2_noflags", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv2_noflags", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv2_noflags", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf1[1024], buf2[1024];
    struct iovec iov[2];
    iov[0].iov_base = buf1; iov[0].iov_len = 1024;
    iov[1].iov_base = buf2; iov[1].iov_len = 1024;

    ssize_t nread = preadv2(fd, iov, 2, 0, 0);

    close(fd); unlink(filepath);

    if (nread != 2048) {
        FAIL("preadv2_noflags", "read %zd bytes, expected 2048", nread);
        return 1;
    }

    PASS("preadv2_no_flags (preadv2 with flags=0 works like preadv)");
    return 0;
}

static int test_pwritev2_append(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev2_append", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev2_append", "open failed: %s", strerror(errno));
        return 1;
    }

    char initial[] = "INITIAL";
    size_t initial_len = strlen(initial);
    if (write(fd, initial, initial_len) != (ssize_t)initial_len) {
        FAIL("pwritev2_append", "initial write failed");
        close(fd); unlink(filepath);
        return 1;
    }
    fsync(fd);

    char append_data[] = "APPENDED";
    size_t append_len = strlen(append_data);
    struct iovec iov;
    iov.iov_base = append_data;
    iov.iov_len = append_len;

    ssize_t nwritten = pwritev2(fd, &iov, 1, -1, RWF_APPEND);

    if (nwritten < 0) {
        close(fd); unlink(filepath);
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("pwritev2_append", "RWF_APPEND not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        FAIL("pwritev2_append", "pwritev2 with RWF_APPEND failed: %s", strerror(errno));
        return 1;
    }

    fsync(fd);
    lseek(fd, 0, SEEK_SET);

    char read_buf[64] = {0};
    ssize_t nread = read(fd, read_buf, sizeof(read_buf));
    close(fd); unlink(filepath);

    size_t expected_len = initial_len + append_len;
    if (nread != (ssize_t)expected_len) {
        FAIL("pwritev2_append", "file size=%zd, expected %zd", nread, expected_len);
        return 1;
    }

    if (memcmp(read_buf, "INITIALAPPENDED", expected_len) != 0) {
        FAIL("pwritev2_append", "data not appended correctly");
        return 1;
    }

    PASS("pwritev2_append (RWF_APPEND: data appended to end of file)");
    return 0;
}

static int test_preadv2_sync_read(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv2_sync_read", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv2_sync_read", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv2_sync_read", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv2(fd, &iov, 1, 0, RWF_SYNC);

    close(fd); unlink(filepath);

    if (nread < 0) {
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("preadv2_sync_read", "RWF_SYNC on read not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        FAIL("preadv2_sync_read", "preadv2 with RWF_SYNC on read failed: %s", strerror(errno));
        return 1;
    }

    PASS("preadv2_sync_read (RWF_SYNC on read: accepted)");
    return 0;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== preadv2/pwritev2 Flags Tests ===\n\n");

    test_preadv2_no_flags();
    test_preadv2_nowait();
    test_preadv2_hipri();
    test_pwritev2_sync();
    test_pwritev2_dsync();
    test_preadv2_sync_read();
    test_pwritev2_append();

    print_summary("preadv2/pwritev2 Flags Tests");
    return g_fail > 0 ? 1 : 0;
}
