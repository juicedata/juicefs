#include "common.h"

static const char *test_dir;

static int verify_alpha_pattern(const char *buf, size_t len, size_t file_offset)
{
    for (size_t i = 0; i < len; i++) {
        size_t in_block = (file_offset + i) % BLOCK_SIZE;
        char expected = 'A' + (in_block % 26);
        if (buf[i] != expected)
            return 0;
    }
    return 1;
}

static int test_read_fixed(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;
    off_t pos_after;

    snprintf(filepath, sizeof(filepath), "%s/fixed_read_testfile", test_dir);
    memset(&iov, 0, sizeof(iov));
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("read_fixed", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("read_fixed", "posix_memalign failed");
        goto cleanup;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("read_fixed", "register_buffers failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("read_fixed", "create test file failed");
        goto cleanup;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("read_fixed", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    memset(iov.iov_base, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("read_fixed", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }
    io_uring_prep_read_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 0x101);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("read_fixed", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("read_fixed", "read_fixed cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("read_fixed", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x101) {
        TEST_FAIL_MSG("read_fixed", "unexpected user_data=%llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (!verify_alpha_pattern((const char *)iov.iov_base, BLOCK_SIZE, BLOCK_SIZE)) {
        TEST_FAIL_MSG("read_fixed", "data integrity check failed");
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    pos_after = lseek(fd, 0, SEEK_CUR);
    if (pos_after != 0) {
        TEST_FAIL_MSG("read_fixed", "file position changed by pread-style read: pos=%lld", (long long)pos_after);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);
    TEST_PASS_MSG("read_fixed (IORING_OP_READ_FIXED)");
    result = TEST_PASS;

cleanup:
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    if (iov.iov_base)
        free(iov.iov_base);
    io_uring_queue_exit(&ring);
    return result;
}

static int test_write_fixed(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char verify_buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;

    snprintf(filepath, sizeof(filepath), "%s/fixed_write_testfile", test_dir);
    memset(&iov, 0, sizeof(iov));
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("write_fixed", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("write_fixed", "posix_memalign failed");
        goto cleanup;
    }
    iov.iov_len = BLOCK_SIZE;

    for (int i = 0; i < BLOCK_SIZE; i++)
        ((char *)iov.iov_base)[i] = 'Z';

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("write_fixed", "register_buffers failed: %s", strerror(-ret));
        goto cleanup;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("write_fixed", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("write_fixed", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }
    io_uring_prep_write_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 0x102);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("write_fixed", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("write_fixed", "write_fixed cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_fixed", "wrote %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x102) {
        TEST_FAIL_MSG("write_fixed", "unexpected user_data=%llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, verify_buf, BLOCK_SIZE);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_fixed", "verification read returned %zd", nread);
        goto cleanup;
    }

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE; i++) {
        if (verify_buf[i] != 'Z') {
            valid = 0;
            break;
        }
    }

    if (!valid) {
        TEST_FAIL_MSG("write_fixed", "data integrity check failed");
        goto cleanup;
    }

    TEST_PASS_MSG("write_fixed (IORING_OP_WRITE_FIXED)");
    result = TEST_PASS;

cleanup:
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    if (iov.iov_base)
        free(iov.iov_base);
    io_uring_queue_exit(&ring);
    return result;
}

static int test_fixed_buffers_rw_consistency(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;
    unsigned char expected[BLOCK_SIZE];

    snprintf(filepath, sizeof(filepath), "%s/fixed_rw_consistency", test_dir);
    memset(&iov, 0, sizeof(iov));
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fixed_rw_consistency", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "posix_memalign failed");
        goto cleanup;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "register_buffers failed: %s", strerror(-ret));
        goto cleanup;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    for (int i = 0; i < BLOCK_SIZE; i++) {
        expected[i] = (unsigned char)(i & 0xFF);
        ((unsigned char *)iov.iov_base)[i] = expected[i];
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("fixed_rw_consistency", "io_uring_get_sqe for write returned NULL");
        goto cleanup;
    }
    io_uring_prep_write_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 0x201);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "write submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }
    if (cqe->res < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_rw_consistency", "write_fixed failed: res=%d", cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    if (io_uring_cqe_get_data64(cqe) != 0x201) {
        TEST_FAIL_MSG("fixed_rw_consistency", "write user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    io_uring_cqe_seen(&ring, cqe);

    memset(iov.iov_base, 0, BLOCK_SIZE);

    sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("fixed_rw_consistency", "io_uring_get_sqe for read returned NULL");
        goto cleanup;
    }
    io_uring_prep_read_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 0x202);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "read submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }
    if (cqe->res < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_rw_consistency", "read_fixed failed: res=%d", cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    if (io_uring_cqe_get_data64(cqe) != 0x202) {
        TEST_FAIL_MSG("fixed_rw_consistency", "read user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    io_uring_cqe_seen(&ring, cqe);

    if (memcmp(iov.iov_base, expected, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "data mismatch after fixed write+read");
        goto cleanup;
    }

    TEST_PASS_MSG("fixed_rw_consistency (write_fixed + read_fixed verify)");
    result = TEST_PASS;

cleanup:
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    if (iov.iov_base)
        free(iov.iov_base);
    io_uring_queue_exit(&ring);
    return result;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring Fixed Buffers Tests ===\n\n");

    test_read_fixed();
    test_write_fixed();
    test_fixed_buffers_rw_consistency();

    print_summary("Fixed Buffers Tests");
    return g_fail_count > 0 ? 1 : 0;
}
