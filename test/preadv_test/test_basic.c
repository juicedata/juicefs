#include "common.h"

static const char *test_dir;

static int test_preadv_basic(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_basic", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv_basic", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv_basic", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf1[1024], buf2[1024], buf3[2048];
    struct iovec iov[3];
    iov[0].iov_base = buf1; iov[0].iov_len = 1024;
    iov[1].iov_base = buf2; iov[1].iov_len = 1024;
    iov[2].iov_base = buf3; iov[2].iov_len = 2048;

    ssize_t nread = preadv(fd, iov, 3, 0);
    if (nread < 0) {
        FAIL("preadv_basic", "preadv failed: %s", strerror(errno));
        close(fd); unlink(filepath);
        return 1;
    }

    if (nread != 4096) {
        FAIL("preadv_basic", "preadv returned %zd, expected 4096", nread);
        close(fd); unlink(filepath);
        return 1;
    }

    int valid = 1;
    size_t file_offset = 0;
    for (int b = 0; b < 3 && valid; b++) {
        char *buf = (b == 0) ? buf1 : (b == 1) ? buf2 : buf3;
        size_t len = (b < 2) ? 1024 : 2048;
        for (size_t i = 0; i < len && valid; i++) {
            if (buf[i] != 'A' + ((file_offset + i) % 26)) valid = 0;
        }
        file_offset += len;
    }

    if (!valid) {
        FAIL("preadv_basic", "data integrity check failed");
        close(fd); unlink(filepath);
        return 1;
    }

    close(fd); unlink(filepath);
    PASS("preadv_basic (3 iovectors, total 4096 bytes)");
    return 0;
}

static int test_pwritev_basic(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev_basic", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev_basic", "open failed: %s", strerror(errno));
        return 1;
    }

    char buf1[1024], buf2[1024], buf3[2048];
    memset(buf1, 'X', 1024);
    memset(buf2, 'Y', 1024);
    memset(buf3, 'Z', 2048);

    struct iovec iov[3];
    iov[0].iov_base = buf1; iov[0].iov_len = 1024;
    iov[1].iov_base = buf2; iov[1].iov_len = 1024;
    iov[2].iov_base = buf3; iov[2].iov_len = 2048;

    ssize_t nwritten = pwritev(fd, iov, 3, 0);
    if (nwritten < 0) {
        FAIL("pwritev_basic", "pwritev failed: %s", strerror(errno));
        close(fd); unlink(filepath);
        return 1;
    }

    if (nwritten != 4096) {
        FAIL("pwritev_basic", "pwritev returned %zd, expected 4096", nwritten);
        close(fd); unlink(filepath);
        return 1;
    }

    fsync(fd);
    lseek(fd, 0, SEEK_SET);

    char read_buf[4096];
    ssize_t nread = read(fd, read_buf, 4096);
    close(fd);

    if (nread != 4096) {
        FAIL("pwritev_basic", "verification read returned %zd", nread);
        unlink(filepath);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < 1024 && valid; i++)
        if (read_buf[i] != 'X') valid = 0;
    for (int i = 1024; i < 2048 && valid; i++)
        if (read_buf[i] != 'Y') valid = 0;
    for (int i = 2048; i < 4096 && valid; i++)
        if (read_buf[i] != 'Z') valid = 0;

    if (!valid) {
        FAIL("pwritev_basic", "data integrity check failed");
        unlink(filepath);
        return 1;
    }

    unlink(filepath);
    PASS("pwritev_basic (3 iovectors with different patterns)");
    return 0;
}

static int test_preadv_with_offset(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_offset", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("preadv_offset", "open failed: %s", strerror(errno));
        return 1;
    }

    char write_buf[BLOCK_SIZE * 4];
    for (int i = 0; i < (int)sizeof(write_buf); i++)
        write_buf[i] = '0' + ((i / BLOCK_SIZE) % 10);

    if (write(fd, write_buf, sizeof(write_buf)) != (ssize_t)sizeof(write_buf)) {
        FAIL("preadv_offset", "write failed");
        close(fd); unlink(filepath);
        return 1;
    }
    fsync(fd);

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    off_t offset = BLOCK_SIZE * 2;
    ssize_t nread = preadv(fd, &iov, 1, offset);
    if (nread < 0) {
        FAIL("preadv_offset", "preadv at offset %ld failed: %s", (long)offset, strerror(errno));
        close(fd); unlink(filepath);
        return 1;
    }

    if (nread != BLOCK_SIZE) {
        FAIL("preadv_offset", "read %zd bytes, expected %d", nread, BLOCK_SIZE);
        close(fd); unlink(filepath);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE && valid; i++)
        if (buf[i] != '2') valid = 0;

    if (!valid) {
        FAIL("preadv_offset", "data at offset mismatch");
        close(fd); unlink(filepath);
        return 1;
    }

    close(fd); unlink(filepath);
    PASS("preadv_with_offset (read at offset=8192, verify correct data)");
    return 0;
}

static int test_preadv_many_iov(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_many_iov", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv_many_iov", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv_many_iov", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    #define NUM_IOV 16
    char *bufs[NUM_IOV];
    struct iovec iov[NUM_IOV];
    size_t chunk = BLOCK_SIZE / NUM_IOV;

    for (int i = 0; i < NUM_IOV; i++) {
        bufs[i] = malloc(chunk);
        if (!bufs[i]) {
            for (int j = 0; j < i; j++) free(bufs[j]);
            close(fd); unlink(filepath);
            FAIL("preadv_many_iov", "malloc failed");
            return 1;
        }
        memset(bufs[i], 0, chunk);
        iov[i].iov_base = bufs[i];
        iov[i].iov_len = chunk;
    }

    ssize_t nread = preadv(fd, iov, NUM_IOV, 0);
    if (nread < 0) {
        FAIL("preadv_many_iov", "preadv with %d iov failed: %s", NUM_IOV, strerror(errno));
        for (int i = 0; i < NUM_IOV; i++) free(bufs[i]);
        close(fd); unlink(filepath);
        return 1;
    }

    if (nread != (ssize_t)BLOCK_SIZE) {
        FAIL("preadv_many_iov", "read %zd bytes, expected %d", nread, BLOCK_SIZE);
        for (int i = 0; i < NUM_IOV; i++) free(bufs[i]);
        close(fd); unlink(filepath);
        return 1;
    }

    int valid = 1;
    size_t file_offset = 0;
    for (int i = 0; i < NUM_IOV && valid; i++) {
        for (size_t j = 0; j < chunk && valid; j++) {
            if (bufs[i][j] != 'A' + ((file_offset + j) % 26)) valid = 0;
        }
        file_offset += chunk;
    }

    for (int i = 0; i < NUM_IOV; i++) free(bufs[i]);
    close(fd); unlink(filepath);

    if (!valid) {
        FAIL("preadv_many_iov", "data integrity check failed");
        return 1;
    }

    PASS("preadv_many_iov (16 iovectors, total 4096 bytes)");
    return 0;
    #undef NUM_IOV
}

static int test_pwritev_preadv_roundtrip(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev_preadv_roundtrip", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev_preadv_roundtrip", "open failed: %s", strerror(errno));
        return 1;
    }

    #define N_IOV 4
    char write_bufs[N_IOV][1024];
    char read_bufs[N_IOV][1024];
    struct iovec wiov[N_IOV], riov[N_IOV];

    for (int i = 0; i < N_IOV; i++) {
        memset(write_bufs[i], 'A' + i, 1024);
        memset(read_bufs[i], 0, 1024);
        wiov[i].iov_base = write_bufs[i];
        wiov[i].iov_len = 1024;
        riov[i].iov_base = read_bufs[i];
        riov[i].iov_len = 1024;
    }

    ssize_t nwritten = pwritev(fd, wiov, N_IOV, 0);
    if (nwritten != N_IOV * 1024) {
        FAIL("pwritev_preadv_roundtrip", "pwritev returned %zd", nwritten);
        close(fd); unlink(filepath);
        return 1;
    }

    fsync(fd);

    ssize_t nread = preadv(fd, riov, N_IOV, 0);
    if (nread != N_IOV * 1024) {
        FAIL("pwritev_preadv_roundtrip", "preadv returned %zd", nread);
        close(fd); unlink(filepath);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < N_IOV && valid; i++) {
        for (int j = 0; j < 1024 && valid; j++) {
            if (read_bufs[i][j] != write_bufs[i][j]) {
                valid = 0;
            }
        }
    }

    close(fd); unlink(filepath);

    if (!valid) {
        FAIL("pwritev_preadv_roundtrip", "data mismatch between pwritev and preadv");
        return 1;
    }

    PASS("pwritev_preadv_roundtrip (write 4 iov -> read 4 iov, verify match)");
    return 0;
    #undef N_IOV
}

static int test_preadv_partial_read(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_partial", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("preadv_partial", "open failed: %s", strerror(errno));
        return 1;
    }

    char write_buf[1024];
    memset(write_buf, 'P', 1024);
    write(fd, write_buf, 1024);
    fsync(fd);

    char buf1[512], buf2[512], buf3[512];
    struct iovec iov[3];
    iov[0].iov_base = buf1; iov[0].iov_len = 512;
    iov[1].iov_base = buf2; iov[1].iov_len = 512;
    iov[2].iov_base = buf3; iov[2].iov_len = 512;

    ssize_t nread = preadv(fd, iov, 3, 0);

    close(fd); unlink(filepath);

    if (nread < 0) {
        FAIL("preadv_partial", "preadv failed: %s", strerror(errno));
        return 1;
    }

    if (nread != 1024) {
        FAIL("preadv_partial", "read %zd bytes, expected 1024 (partial fill)", nread);
        return 1;
    }

    PASS("preadv_partial (3x512B iov but file only 1024B, partial fill ok)");
    return 0;
}

static int test_preadv_at_end_of_file(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_eof", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("preadv_eof", "open failed: %s", strerror(errno));
        return 1;
    }

    char write_buf[256];
    memset(write_buf, 'E', 256);
    write(fd, write_buf, 256);
    fsync(fd);

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv(fd, &iov, 1, 256);

    close(fd); unlink(filepath);

    if (nread < 0) {
        FAIL("preadv_eof", "preadv at EOF failed: %s", strerror(errno));
        return 1;
    }

    if (nread != 0) {
        FAIL("preadv_eof", "preadv at EOF returned %zd, expected 0", nread);
        return 1;
    }

    PASS("preadv_at_eof (read past end of file returns 0)");
    return 0;
}

static int test_pwritev_at_offset(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/pwritev_offset", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        FAIL("pwritev_offset", "open failed: %s", strerror(errno));
        return 1;
    }

    char initial[BLOCK_SIZE * 4];
    memset(initial, '.', sizeof(initial));
    write(fd, initial, sizeof(initial));

    char buf1[100], buf2[100];
    memset(buf1, '1', 100);
    memset(buf2, '2', 100);

    struct iovec iov[2];
    iov[0].iov_base = buf1; iov[0].iov_len = 100;
    iov[1].iov_base = buf2; iov[1].iov_len = 100;

    off_t offset = BLOCK_SIZE;
    ssize_t nwritten = pwritev(fd, iov, 2, offset);
    if (nwritten != 200) {
        FAIL("pwritev_offset", "pwritev returned %zd, expected 200", nwritten);
        close(fd); unlink(filepath);
        return 1;
    }

    fsync(fd);

    char read_buf[200];
    ssize_t nread = pread(fd, read_buf, 200, offset);
    close(fd); unlink(filepath);

    if (nread != 200) {
        FAIL("pwritev_offset", "verification read returned %zd", nread);
        return 1;
    }

    int valid = 1;
    for (int i = 0; i < 100 && valid; i++)
        if (read_buf[i] != '1') valid = 0;
    for (int i = 100; i < 200 && valid; i++)
        if (read_buf[i] != '2') valid = 0;

    if (!valid) {
        FAIL("pwritev_offset", "data at offset mismatch");
        return 1;
    }

    PASS("pwritev_at_offset (write 2 iov at offset=4096, verify data)");
    return 0;
}

static int test_preadv_single_iov(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_single", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv_single", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv_single", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf[BLOCK_SIZE];
    struct iovec iov;
    iov.iov_base = buf;
    iov.iov_len = BLOCK_SIZE;

    ssize_t nread = preadv(fd, &iov, 1, 0);
    close(fd); unlink(filepath);

    if (nread != BLOCK_SIZE) {
        FAIL("preadv_single", "preadv with 1 iov returned %zd", nread);
        return 1;
    }

    PASS("preadv_single_iov (1 iovector, equivalent to pread)");
    return 0;
}

static int test_preadv_zero_len_iov(void)
{
    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/preadv_zero_len", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        FAIL("preadv_zero_len", "create test file failed");
        return 1;
    }

    int fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        FAIL("preadv_zero_len", "open failed: %s", strerror(errno));
        unlink(filepath);
        return 1;
    }

    char buf1[512], buf2[512], dummy;
    struct iovec iov[3];
    iov[0].iov_base = buf1; iov[0].iov_len = 512;
    iov[1].iov_base = &dummy; iov[1].iov_len = 0;
    iov[2].iov_base = buf2; iov[2].iov_len = 512;

    ssize_t nread = preadv(fd, iov, 3, 0);
    close(fd); unlink(filepath);

    if (nread < 0) {
        FAIL("preadv_zero_len", "preadv with zero-len iov failed: %s", strerror(errno));
        return 1;
    }

    if (nread != 1024) {
        FAIL("preadv_zero_len", "read %zd bytes, expected 1024", nread);
        return 1;
    }

    PASS("preadv_zero_len_iov (middle iov has len=0, should be skipped)");
    return 0;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== preadv/pwritev Basic Tests ===\n\n");

    test_preadv_basic();
    test_pwritev_basic();
    test_preadv_with_offset();
    test_preadv_many_iov();
    test_pwritev_preadv_roundtrip();
    test_preadv_partial_read();
    test_preadv_at_end_of_file();
    test_pwritev_at_offset();
    test_preadv_single_iov();
    test_preadv_zero_len_iov();

    print_summary("preadv/pwritev Basic Tests");
    return g_fail > 0 ? 1 : 0;
}
