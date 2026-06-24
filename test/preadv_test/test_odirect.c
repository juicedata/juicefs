#include "common.h"
#include <linux/fs.h>

static const char *test_dir;

static int test_odirect_preadv_aligned(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/odirect_preadv", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC | O_DIRECT, 0644);
    if (fd < 0) {
        if (errno == EINVAL || errno == EOPNOTSUPP) {
            SKIP("odirect_preadv_aligned", "O_DIRECT not supported on this filesystem");
            return 0;
        }
        FAIL("odirect_preadv_aligned", "open with O_DIRECT failed: %s", strerror(errno));
        return 1;
    }

    char *write_buf = NULL;
    if (posix_memalign((void **)&write_buf, BLOCK_SIZE, BLOCK_SIZE) != 0) {
        FAIL("odirect_preadv_aligned", "posix_memalign failed");
        close(fd); unlink(filepath);
        return 1;
    }
    memset(write_buf, 'O', BLOCK_SIZE);

    ssize_t nwritten = pwrite(fd, write_buf, BLOCK_SIZE, 0);
    if (nwritten != BLOCK_SIZE) {
        FAIL("odirect_preadv_aligned",
             "pwrite with O_DIRECT returned %zd, expected %d: %s",
             nwritten, BLOCK_SIZE, strerror(errno));
        free(write_buf); close(fd); unlink(filepath);
        return 1;
    }

    char *read_buf = NULL;
    if (posix_memalign((void **)&read_buf, BLOCK_SIZE, BLOCK_SIZE) != 0) {
        free(write_buf); close(fd); unlink(filepath);
        FAIL("odirect_preadv_aligned", "posix_memalign for read failed");
        return 1;
    }
    memset(read_buf, 0, BLOCK_SIZE);

    struct iovec iov;
    iov.iov_base = read_buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv(fd, &iov, 1, 0);
    if (nread < 0) {
        FAIL("odirect_preadv_aligned", "preadv with O_DIRECT failed: %s", strerror(errno));
        free(write_buf); free(read_buf); close(fd); unlink(filepath);
        return 1;
    }

    if (nread != BLOCK_SIZE) {
        FAIL("odirect_preadv_aligned", "read %zd bytes, expected %d", nread, BLOCK_SIZE);
        free(write_buf); free(read_buf); close(fd); unlink(filepath);
        return 1;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        FAIL("odirect_preadv_aligned", "data integrity check failed");
        free(write_buf); free(read_buf); close(fd); unlink(filepath);
        return 1;
    }

    free(write_buf); free(read_buf); close(fd); unlink(filepath);
    PASS("odirect_preadv_aligned (O_DIRECT + preadv with aligned buffer)");
    return 0;
}

static int test_odirect_pwritev_aligned(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/odirect_pwritev", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC | O_DIRECT, 0644);
    if (fd < 0) {
        if (errno == EINVAL || errno == EOPNOTSUPP) {
            SKIP("odirect_pwritev_aligned", "O_DIRECT not supported");
            return 0;
        }
        FAIL("odirect_pwritev_aligned", "open with O_DIRECT failed: %s", strerror(errno));
        return 1;
    }

    char *buf1 = NULL, *buf2 = NULL;
    if (posix_memalign((void **)&buf1, BLOCK_SIZE, BLOCK_SIZE) != 0 ||
        posix_memalign((void **)&buf2, BLOCK_SIZE, BLOCK_SIZE) != 0) {
        FAIL("odirect_pwritev_aligned", "posix_memalign failed");
        free(buf1); free(buf2); close(fd); unlink(filepath);
        return 1;
    }

    memset(buf1, 'A', BLOCK_SIZE);
    memset(buf2, 'B', BLOCK_SIZE);

    struct iovec iov[2];
    iov[0].iov_base = buf1; iov[0].iov_len = BLOCK_SIZE;
    iov[1].iov_base = buf2; iov[1].iov_len = BLOCK_SIZE;

    ssize_t nwritten = pwritev(fd, iov, 2, 0);
    if (nwritten < 0) {
        FAIL("odirect_pwritev_aligned", "pwritev with O_DIRECT failed: %s", strerror(errno));
        free(buf1); free(buf2); close(fd); unlink(filepath);
        return 1;
    }

    if (nwritten != BLOCK_SIZE * 2) {
        FAIL("odirect_pwritev_aligned", "wrote %zd bytes, expected %d", nwritten, BLOCK_SIZE * 2);
        free(buf1); free(buf2); close(fd); unlink(filepath);
        return 1;
    }

    close(fd);
    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("odirect_pwritev_aligned", "reopen for verify failed");
        free(buf1); free(buf2); unlink(filepath);
        return 1;
    }

    char verify[BLOCK_SIZE * 2];
    ssize_t nread = read(fd, verify, sizeof(verify));
    close(fd); unlink(filepath);

    if (nread != BLOCK_SIZE * 2) {
        FAIL("odirect_pwritev_aligned", "verification read returned %zd", nread);
        free(buf1); free(buf2);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE && valid; i++)
        if (verify[i] != 'A') valid = 0;
    for (int i = BLOCK_SIZE; i < BLOCK_SIZE * 2 && valid; i++)
        if (verify[i] != 'B') valid = 0;

    free(buf1); free(buf2);

    if (!valid) {
        FAIL("odirect_pwritev_aligned", "data integrity check failed");
        return 1;
    }

    PASS("odirect_pwritev_aligned (O_DIRECT + pwritev with 2 aligned buffers)");
    return 0;
}


int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== O_DIRECT + preadv/pwritev Tests ===\n\n");

    test_odirect_preadv_aligned();
    test_odirect_pwritev_aligned();

    print_summary("O_DIRECT + preadv/pwritev Tests");
    return g_fail > 0 ? 1 : 0;
}
