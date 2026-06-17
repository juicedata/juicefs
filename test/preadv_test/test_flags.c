#include "common.h"

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

static int test_preadv2_nowait(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv2_nowait", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv2_nowait", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv2_nowait", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv2(fd, &iov, 1, 0, RWF_NOWAIT);

    close(fd); unlink(filepath);

    if (nread < 0) {
        if (errno == EOPNOTSUPP || errno == EINVAL) {
            SKIP("preadv2_nowait", "RWF_NOWAIT not supported (errno=%d: %s)", errno, strerror(errno));
            return 0;
        }
        if (errno == EAGAIN) {
            PASS("preadv2_nowait (RWF_NOWAIT returns EAGAIN as expected for network FS)");
            return 0;
        }
        FAIL("preadv2_nowait", "preadv2 with RWF_NOWAIT failed: %s (errno=%d)", strerror(errno), errno);
        return 1;
    }

    PASS("preadv2_nowait (RWF_NOWAIT returned data, no error)");
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
