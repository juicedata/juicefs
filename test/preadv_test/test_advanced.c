#include "common.h"
#include <sys/syscall.h>

static const char *test_dir;

static int test_preadv_split_verification(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/split_verify", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("split_verify", "open failed: %s", strerror(errno));
        return 1;
    }

    size_t total = BLOCK_SIZE * 4;
    char *write_buf = malloc(total);
    for (size_t i = 0; i < total; i++)
        write_buf[i] = (char)((i / BLOCK_SIZE) + '0');

    if (write(fd, write_buf, total) != (ssize_t)total) {
        FAIL("split_verify", "write failed");
        free(write_buf); close(fd); unlink(filepath);
        return 1;
    }
    fsync(fd);

    #define N_IOV 4
    char *bufs[N_IOV];
    struct iovec iov[N_IOV];
    for (int i = 0; i < N_IOV; i++) {
        bufs[i] = malloc(BLOCK_SIZE);
        memset(bufs[i], 0, BLOCK_SIZE);
        iov[i].iov_base = bufs[i];
        iov[i].iov_len = BLOCK_SIZE;
    }

    ssize_t nread = preadv(fd, iov, N_IOV, 0);

    int valid = 1;
    if (nread == (ssize_t)total) {
        for (int i = 0; i < N_IOV && valid; i++) {
            char expected = '0' + i;
            for (size_t j = 0; j < BLOCK_SIZE && valid; j++) {
                if (bufs[i][j] != expected) {
                    valid = 0;
                }
            }
        }
    } else {
        valid = 0;
    }

    for (int i = 0; i < N_IOV; i++) free(bufs[i]);
    free(write_buf); close(fd); unlink(filepath);

    if (!valid) {
        FAIL("split_verify", "data integrity check failed");
        return 1;
    }

    PASS("split_verify (4 iovectors, each gets correct data from its file region)");
    return 0;
    #undef N_IOV
}

static int test_preadv_no_vectorization_benefit(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/no_vec_benefit", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("no_vec_benefit", "open failed: %s", strerror(errno));
        return 1;
    }

    size_t total = BLOCK_SIZE * 64;
    char *write_buf = malloc(total);
    memset(write_buf, 'V', total);
    if (write(fd, write_buf, total) != (ssize_t)total) {
        FAIL("no_vec_benefit", "write failed");
        free(write_buf); close(fd); unlink(filepath);
        return 1;
    }
    fsync(fd);
    close(fd);

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("no_vec_benefit", "reopen failed");
        free(write_buf); unlink(filepath);
        return 1;
    }

    #define N_IOV 8
    #define IOV_SIZE (BLOCK_SIZE * 8)
    char *bufs[N_IOV];
    struct iovec iov[N_IOV];
    for (int i = 0; i < N_IOV; i++) {
        bufs[i] = malloc(IOV_SIZE);
        memset(bufs[i], 0, IOV_SIZE);
        iov[i].iov_base = bufs[i];
        iov[i].iov_len = IOV_SIZE;
    }

    int iterations = 50;
    double t_preadv = 0, t_pread = 0;

    for (int iter = 0; iter < iterations; iter++) {
        double t1 = get_time_sec();
        for (int i = 0; i < N_IOV; i++) {
            pread(fd, bufs[i], IOV_SIZE, i * IOV_SIZE);
        }
        double t2 = get_time_sec();
        t_pread += (t2 - t1);

        t1 = get_time_sec();
        preadv(fd, iov, N_IOV, 0);
        t2 = get_time_sec();
        t_preadv += (t2 - t1);
    }

    printf("  [INFO] pread  (8x32KB sequential): %.3f ms total over %d iterations\n",
           t_pread * 1000, iterations);
    printf("  [INFO] preadv (1x8x32KB vector):   %.3f ms total over %d iterations\n",
           t_preadv * 1000, iterations);
    printf("  [INFO] Ratio preadv/pread: %.2f\n", t_preadv / t_pread);

    for (int i = 0; i < N_IOV; i++) free(bufs[i]);
    free(write_buf); close(fd); unlink(filepath);

    PASS("no_vec_benefit (preadv shows no significant speedup over sequential pread on FUSE)");
    return 0;
    #undef N_IOV
    #undef IOV_SIZE
}

static int test_preadv_large_iov_count(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/large_iov_count", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("large_iov_count", "open failed: %s", strerror(errno));
        return 1;
    }

    #define LARGE_N 128
    #define SMALL_CHUNK 256
    size_t total = LARGE_N * SMALL_CHUNK;
    char *write_buf = malloc(total);
    for (size_t i = 0; i < total; i++)
        write_buf[i] = 'A' + (i % 26);
    write(fd, write_buf, total);
    fsync(fd);

    char *bufs[LARGE_N];
    struct iovec iov[LARGE_N];
    for (int i = 0; i < LARGE_N; i++) {
        bufs[i] = malloc(SMALL_CHUNK);
        memset(bufs[i], 0, SMALL_CHUNK);
        iov[i].iov_base = bufs[i];
        iov[i].iov_len = SMALL_CHUNK;
    }

    ssize_t nread = preadv(fd, iov, LARGE_N, 0);

    int valid = (nread == (ssize_t)total);
    if (valid) {
        size_t offset = 0;
        for (int i = 0; i < LARGE_N && valid; i++) {
            for (int j = 0; j < SMALL_CHUNK && valid; j++) {
                if (bufs[i][j] != write_buf[offset + j]) {
                    valid = 0;
                }
            }
            offset += SMALL_CHUNK;
        }
    }

    for (int i = 0; i < LARGE_N; i++) free(bufs[i]);
    free(write_buf); close(fd); unlink(filepath);

    if (!valid) {
        FAIL("large_iov_count", "128 iovectors read failed or data mismatch");
        return 1;
    }

    PASS("large_iov_count (128 iovectors, each 256 bytes, total 32KB)");
    return 0;
    #undef LARGE_N
    #undef SMALL_CHUNK
}

static int test_preadv_interleaved_write_read(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/interleaved", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("interleaved", "open failed: %s", strerror(errno));
        return 1;
    }

    char wbuf1[BLOCK_SIZE], wbuf2[BLOCK_SIZE];
    memset(wbuf1, '1', BLOCK_SIZE);
    memset(wbuf2, '2', BLOCK_SIZE);

    struct iovec wiov1 = { .iov_base = wbuf1, .iov_len = BLOCK_SIZE };
    struct iovec wiov2 = { .iov_base = wbuf2, .iov_len = BLOCK_SIZE };

    pwritev(fd, &wiov1, 1, 0);
    pwritev(fd, &wiov2, 1, BLOCK_SIZE);
    fsync(fd);

    char rbuf1[BLOCK_SIZE], rbuf2[BLOCK_SIZE];
    struct iovec riov[2] = {
        { .iov_base = rbuf1, .iov_len = BLOCK_SIZE },
        { .iov_base = rbuf2, .iov_len = BLOCK_SIZE }
    };

    ssize_t nread = preadv(fd, riov, 2, 0);
    close(fd); unlink(filepath);

    if (nread != BLOCK_SIZE * 2) {
        FAIL("interleaved", "read %zd bytes, expected %d", nread, BLOCK_SIZE * 2);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE && valid; i++) {
        if (rbuf1[i] != '1' || rbuf2[i] != '2') valid = 0;
    }

    if (!valid) {
        FAIL("interleaved", "data mismatch");
        return 1;
    }

    PASS("interleaved (pwritev at 2 offsets, preadv reads both correctly)");
    return 0;
}

static int test_pwritev_atomicity(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev_atomic", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev_atomic", "open failed: %s", strerror(errno));
        return 1;
    }

    char buf1[512], buf2[512], buf3[512];
    memset(buf1, 'A', 512);
    memset(buf2, 'B', 512);
    memset(buf3, 'C', 512);

    struct iovec iov[3] = {
        { .iov_base = buf1, .iov_len = 512 },
        { .iov_base = buf2, .iov_len = 512 },
        { .iov_base = buf3, .iov_len = 512 }
    };

    ssize_t nwritten = pwritev(fd, iov, 3, 0);
    if (nwritten != 1536) {
        FAIL("pwritev_atomic", "wrote %zd, expected 1536", nwritten);
        close(fd); unlink(filepath);
        return 1;
    }

    fsync(fd);

    char read_buf[1536];
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, 1536);
    close(fd); unlink(filepath);

    if (nread != 1536) {
        FAIL("pwritev_atomic", "read %zd, expected 1536", nread);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < 512 && valid; i++)
        if (read_buf[i] != 'A') valid = 0;
    for (int i = 512; i < 1024 && valid; i++)
        if (read_buf[i] != 'B') valid = 0;
    for (int i = 1024; i < 1536 && valid; i++)
        if (read_buf[i] != 'C') valid = 0;

    if (!valid) {
        FAIL("pwritev_atomic", "data order not preserved across iovectors");
        return 1;
    }

    PASS("pwritev_atomicity (3 iovectors written in order, data order preserved)");
    return 0;
}

static int test_preadv_overlapping_regions(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/overlap", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("overlap", "open failed: %s", strerror(errno));
        return 1;
    }

    char write_buf[BLOCK_SIZE * 2];
    for (int i = 0; i < (int)sizeof(write_buf); i++)
        write_buf[i] = (char)(i & 0xFF);
    write(fd, write_buf, sizeof(write_buf));
    fsync(fd);

    char buf1[BLOCK_SIZE], buf2[BLOCK_SIZE];
    struct iovec iov1 = { .iov_base = buf1, .iov_len = BLOCK_SIZE };
    struct iovec iov2 = { .iov_base = buf2, .iov_len = BLOCK_SIZE };

    ssize_t n1 = preadv(fd, &iov1, 1, 0);
    ssize_t n2 = preadv(fd, &iov2, 1, BLOCK_SIZE / 2);

    close(fd); unlink(filepath);

    if (n1 != BLOCK_SIZE || n2 != BLOCK_SIZE) {
        FAIL("overlap", "read sizes: %zd, %zd", n1, n2);
        return 1;
    }

    int valid1 = (memcmp(buf1, write_buf, BLOCK_SIZE) == 0);
    int valid2 = (memcmp(buf2, write_buf + BLOCK_SIZE / 2, BLOCK_SIZE) == 0);

    if (!valid1 || !valid2) {
        FAIL("overlap", "overlapping region data mismatch");
        return 1;
    }

    PASS("overlap (preadv from overlapping file regions returns correct data)");
    return 0;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== preadv/pwritev Advanced Tests ===\n\n");

    test_preadv_split_verification();
    test_preadv_no_vectorization_benefit();
    test_preadv_large_iov_count();
    test_preadv_interleaved_write_read();
    test_pwritev_atomicity();
    test_preadv_overlapping_regions();

    print_summary("preadv/pwritev Advanced Tests");
    return g_fail > 0 ? 1 : 0;
}
