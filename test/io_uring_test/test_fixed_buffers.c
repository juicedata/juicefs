#include "common.h"

static const char *test_dir;

static int test_register_buffers(void)
{
    struct io_uring ring;
    struct iovec iov;
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("register_buffers", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("register_buffers", "posix_memalign failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("register_buffers", "io_uring_register_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_unregister_buffers(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("register_buffers", "io_uring_unregister_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    free(iov.iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("register_buffers (IORING_REGISTER_BUFFERS)");
    return TEST_PASS;
}

static int test_register_multiple_buffers(void)
{
    struct io_uring ring;
    struct iovec iovs[4];
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("register_multiple_buffers", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    for (int i = 0; i < 4; i++) {
        if (posix_memalign(&iovs[i].iov_base, 4096, BLOCK_SIZE) != 0) {
            for (int j = 0; j < i; j++) free(iovs[j].iov_base);
            io_uring_queue_exit(&ring);
            TEST_FAIL_MSG("register_multiple_buffers", "posix_memalign failed");
            return TEST_FAIL;
        }
        iovs[i].iov_len = BLOCK_SIZE;
    }

    ret = io_uring_register_buffers(&ring, iovs, 4);
    if (ret < 0) {
        TEST_FAIL_MSG("register_multiple_buffers", "register 4 buffers failed: %s", strerror(-ret));
        for (int i = 0; i < 4; i++) free(iovs[i].iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_unregister_buffers(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("register_multiple_buffers", "unregister buffers failed: %s", strerror(-ret));
    }

    for (int i = 0; i < 4; i++) free(iovs[i].iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("register_multiple_buffers (4 buffers)");
    return TEST_PASS;
}

static int test_read_fixed(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_read_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("read_fixed", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("read_fixed", "posix_memalign failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("read_fixed", "register_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("read_fixed", "create test file failed");
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("read_fixed", "open failed: %s", strerror(errno));
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    memset(iov.iov_base, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("read_fixed", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("read_fixed", "read_fixed cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("read_fixed", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    free(iov.iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("read_fixed (IORING_OP_READ_FIXED)");
    return TEST_PASS;
}

static int test_write_fixed(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char verify_buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_write_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("write_fixed", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("write_fixed", "posix_memalign failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    iov.iov_len = BLOCK_SIZE;

    for (int i = 0; i < BLOCK_SIZE; i++)
        ((char *)iov.iov_base)[i] = 'Z';

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("write_fixed", "register_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("write_fixed", "open failed: %s", strerror(errno));
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_write_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("write_fixed", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("write_fixed", "write_fixed cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_fixed", "wrote %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, verify_buf, BLOCK_SIZE);
    close(fd);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_fixed", "verification read returned %zd", nread);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
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
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    free(iov.iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("write_fixed (IORING_OP_WRITE_FIXED)");
    return TEST_PASS;
}

static int test_fixed_buffers_rw_consistency(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_rw_consistency", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fixed_rw_consistency", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "posix_memalign failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "register_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("fixed_rw_consistency", "open failed: %s", strerror(errno));
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        ((char *)iov.iov_base)[i] = (char)(i & 0xFF);

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_write_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 10);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_rw_consistency", "write_fixed failed");
        if (cqe) io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    memset(iov.iov_base, 0, BLOCK_SIZE);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read_fixed(sqe, fd, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_data64(sqe, 11);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_rw_consistency", "read_fixed failed");
        if (cqe) io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE; i++) {
        if (((char *)iov.iov_base)[i] != (char)(i & 0xFF)) {
            valid = 0;
            break;
        }
    }

    if (!valid) {
        TEST_FAIL_MSG("fixed_rw_consistency", "data mismatch after fixed write+read");
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    free(iov.iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fixed_rw_consistency (write_fixed + read_fixed verify)");
    return TEST_PASS;
}

static int test_fixed_buffers_multiple_indices(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iovs[3];
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_multi_idx_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fixed_multi_idx", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    for (int i = 0; i < 3; i++) {
        if (posix_memalign(&iovs[i].iov_base, 4096, BLOCK_SIZE) != 0) {
            for (int j = 0; j < i; j++) free(iovs[j].iov_base);
            io_uring_queue_exit(&ring);
            TEST_FAIL_MSG("fixed_multi_idx", "posix_memalign failed");
            return TEST_FAIL;
        }
        iovs[i].iov_len = BLOCK_SIZE;
    }

    for (int i = 0; i < 3; i++)
        memset(iovs[i].iov_base, 'A' + i, BLOCK_SIZE);

    ret = io_uring_register_buffers(&ring, iovs, 3);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_multi_idx", "register_buffers(3) failed: %s", strerror(-ret));
        for (int i = 0; i < 3; i++) free(iovs[i].iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("fixed_multi_idx", "create test file failed");
        io_uring_unregister_buffers(&ring);
        for (int i = 0; i < 3; i++) free(iovs[i].iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("fixed_multi_idx", "open failed: %s", strerror(errno));
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        for (int i = 0; i < 3; i++) free(iovs[i].iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int all_ok = 1;
    for (int i = 0; i < 3; i++) {
        memset(iovs[i].iov_base, 0, BLOCK_SIZE);
        struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
        io_uring_prep_read_fixed(sqe, fd, iovs[i].iov_base, BLOCK_SIZE, i * BLOCK_SIZE, i);
        io_uring_sqe_set_data64(sqe, 20 + i);
    }

    ret = io_uring_submit(&ring);
    if (ret != 3) {
        TEST_FAIL_MSG("fixed_multi_idx", "submitted %d, expected 3", ret);
        all_ok = 0;
    }

    for (int i = 0; i < 3 && all_ok; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0 || cqe->res != BLOCK_SIZE) {
            TEST_FAIL_MSG("fixed_multi_idx", "cqe[%d] failed", i);
            all_ok = 0;
        }
        io_uring_cqe_seen(&ring, cqe);
    }

    close(fd);
    unlink(filepath);
    io_uring_unregister_buffers(&ring);
    for (int i = 0; i < 3; i++) free(iovs[i].iov_base);
    io_uring_queue_exit(&ring);

    if (all_ok) {
        TEST_PASS_MSG("fixed_multi_idx (3 registered buffer indices)");
        return TEST_PASS;
    }
    return TEST_FAIL;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring Fixed Buffers Tests ===\n\n");

    test_register_buffers();
    test_register_multiple_buffers();
    test_read_fixed();
    test_write_fixed();
    test_fixed_buffers_rw_consistency();
    test_fixed_buffers_multiple_indices();

    print_summary("Fixed Buffers Tests");
    return g_fail_count > 0 ? 1 : 0;
}
