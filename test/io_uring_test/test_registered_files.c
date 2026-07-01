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

static int test_read_with_fixed_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;
    off_t pos_after;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_read", test_dir);
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("read_with_fixed_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "create test file failed");
        goto cleanup;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "register_files failed: %s", strerror(-ret));
        goto cleanup;
    }

    memset(buf, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("read_with_fixed_file", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }
    io_uring_prep_read(sqe, 0, buf, BLOCK_SIZE, BLOCK_SIZE);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 0x311);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("read_with_fixed_file", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x311) {
        TEST_FAIL_MSG("read_with_fixed_file", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (!verify_alpha_pattern(buf, BLOCK_SIZE, BLOCK_SIZE)) {
        TEST_FAIL_MSG("read_with_fixed_file", "data verification failed");
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    pos_after = lseek(fd, 0, SEEK_CUR);
    if (pos_after != 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "file position changed unexpectedly: pos=%lld", (long long)pos_after);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);
    TEST_PASS_MSG("read_with_fixed_file (IOSQE_FIXED_FILE + IORING_OP_READ)");
    result = TEST_PASS;

cleanup:
    io_uring_unregister_files(&ring);
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    return result;
}

static int test_write_with_fixed_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_write", test_dir);
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("write_with_fixed_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "register_files failed: %s", strerror(-ret));
        goto cleanup;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'Q';

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("write_with_fixed_file", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }
    io_uring_prep_write(sqe, 0, write_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 0x312);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "write cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_with_fixed_file", "wrote %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x312) {
        TEST_FAIL_MSG("write_with_fixed_file", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);

    if (nread != BLOCK_SIZE || memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "data integrity check failed");
        goto cleanup;
    }

    TEST_PASS_MSG("write_with_fixed_file (IOSQE_FIXED_FILE + IORING_OP_WRITE)");
    result = TEST_PASS;

cleanup:
    io_uring_unregister_files(&ring);
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    return result;
}

static int test_fixed_file_with_fixed_buffer(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;
    int result = TEST_FAIL;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_fixed_buf", test_dir);
    memset(&iov, 0, sizeof(iov));
    fd = -1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fixed_file_fixed_buf", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "posix_memalign failed");
        goto cleanup;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "register_buffers failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "create test file failed");
        goto cleanup;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "open failed: %s", strerror(errno));
        goto cleanup;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "register_files failed: %s", strerror(-ret));
        goto cleanup;
    }

    memset(iov.iov_base, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }
    io_uring_prep_read_fixed(sqe, 0, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 0x313);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x313) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (!verify_alpha_pattern((const char *)iov.iov_base, BLOCK_SIZE, 0)) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "data verification failed");
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);
    TEST_PASS_MSG("fixed_file_fixed_buf (IOSQE_FIXED_FILE + IORING_OP_READ_FIXED)");
    result = TEST_PASS;

cleanup:
    io_uring_unregister_files(&ring);
    io_uring_unregister_buffers(&ring);
    if (fd >= 0)
        close(fd);
    unlink(filepath);
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

    printf("\n=== io_uring Registered Files Tests ===\n\n");

    test_read_with_fixed_file();
    test_write_with_fixed_file();
    test_fixed_file_with_fixed_buffer();

    print_summary("Registered Files Tests");
    return g_fail_count > 0 ? 1 : 0;
}
