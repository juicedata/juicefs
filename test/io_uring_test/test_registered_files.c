#include "common.h"

static const char *test_dir;

static int test_register_files(void)
{
    struct io_uring ring;
    char filepath[512];
    int fds[4];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/reg_files_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("register_files", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    for (int i = 0; i < 4; i++) {
        char path[512];
        snprintf(path, sizeof(path), "%s/reg_file_%d", test_dir, i);
        fds[i] = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
        if (fds[i] < 0) {
            for (int j = 0; j < i; j++) {
                char p[512];
                snprintf(p, sizeof(p), "%s/reg_file_%d", test_dir, j);
                close(fds[j]);
                unlink(p);
            }
            io_uring_queue_exit(&ring);
            TEST_FAIL_MSG("register_files", "open failed: %s", strerror(errno));
            return TEST_FAIL;
        }
    }

    ret = io_uring_register_files(&ring, fds, 4);
    if (ret < 0) {
        TEST_FAIL_MSG("register_files", "io_uring_register_files failed: %s", strerror(-ret));
        for (int i = 0; i < 4; i++) {
            close(fds[i]);
            char p[512];
            snprintf(p, sizeof(p), "%s/reg_file_%d", test_dir, i);
            unlink(p);
        }
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_unregister_files(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("register_files", "io_uring_unregister_files failed: %s", strerror(-ret));
    }

    for (int i = 0; i < 4; i++) {
        close(fds[i]);
        char p[512];
        snprintf(p, sizeof(p), "%s/reg_file_%d", test_dir, i);
        unlink(p);
    }
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("register_files (IORING_REGISTER_FILES)");
    return TEST_PASS;
}

static int test_read_with_fixed_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_read", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("read_with_fixed_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "create test file failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "open failed: %s", strerror(errno));
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "register_files failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    memset(buf, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, 0, buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "submit/wait failed: %s", strerror(-ret));
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("read_with_fixed_file", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("read_with_fixed_file", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    io_uring_unregister_files(&ring);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("read_with_fixed_file (IOSQE_FIXED_FILE + IORING_OP_READ)");
    return TEST_PASS;
}

static int test_write_with_fixed_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_write", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("write_with_fixed_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "register_files failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'Q';

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_write(sqe, 0, write_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "submit/wait failed: %s", strerror(-ret));
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "write cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("write_with_fixed_file", "wrote %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);

    if (nread != BLOCK_SIZE || memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("write_with_fixed_file", "data integrity check failed");
        io_uring_unregister_files(&ring);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_unregister_files(&ring);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("write_with_fixed_file (IOSQE_FIXED_FILE + IORING_OP_WRITE)");
    return TEST_PASS;
}

static int test_fixed_file_with_fixed_buffer(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct iovec iov;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fixed_file_fixed_buf", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fixed_file_fixed_buf", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (posix_memalign(&iov.iov_base, 4096, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "posix_memalign failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    iov.iov_len = BLOCK_SIZE;

    ret = io_uring_register_buffers(&ring, &iov, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "register_buffers failed: %s", strerror(-ret));
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "create test file failed");
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "open failed: %s", strerror(errno));
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_register_files(&ring, &fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "register_files failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_unregister_buffers(&ring);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    memset(iov.iov_base, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read_fixed(sqe, 0, iov.iov_base, BLOCK_SIZE, 0, 0);
    io_uring_sqe_set_flags(sqe, IOSQE_FIXED_FILE);
    io_uring_sqe_set_data64(sqe, 3);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "submit/wait failed: %s", strerror(-ret));
        io_uring_unregister_files(&ring);
        io_uring_unregister_buffers(&ring);
        close(fd);
        unlink(filepath);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        io_uring_unregister_buffers(&ring);
        close(fd);
        unlink(filepath);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("fixed_file_fixed_buf", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        io_uring_unregister_files(&ring);
        io_uring_unregister_buffers(&ring);
        close(fd);
        unlink(filepath);
        free(iov.iov_base);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    io_uring_unregister_files(&ring);
    io_uring_unregister_buffers(&ring);
    close(fd);
    unlink(filepath);
    free(iov.iov_base);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fixed_file_fixed_buf (IOSQE_FIXED_FILE + IORING_OP_READ_FIXED)");
    return TEST_PASS;
}

static int test_register_files_update(void)
{
    struct io_uring ring;
    char filepath1[512], filepath2[512];
    int fds[2];
    int ret;

    snprintf(filepath1, sizeof(filepath1), "%s/reg_update_1", test_dir);
    snprintf(filepath2, sizeof(filepath2), "%s/reg_update_2", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("register_files_update", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fds[0] = open(filepath1, O_RDWR | O_CREAT | O_TRUNC, 0644);
    fds[1] = open(filepath2, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fds[0] < 0 || fds[1] < 0) {
        TEST_FAIL_MSG("register_files_update", "open failed: %s", strerror(errno));
        if (fds[0] >= 0) close(fds[0]);
        if (fds[1] >= 0) close(fds[1]);
        unlink(filepath1);
        unlink(filepath2);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_register_files(&ring, fds, 2);
    if (ret < 0) {
        TEST_FAIL_MSG("register_files_update", "register_files failed: %s", strerror(-ret));
        close(fds[0]);
        close(fds[1]);
        unlink(filepath1);
        unlink(filepath2);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    close(fds[0]);
    char filepath3[512];
    snprintf(filepath3, sizeof(filepath3), "%s/reg_update_3", test_dir);
    int new_fd = open(filepath3, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (new_fd < 0) {
        TEST_FAIL_MSG("register_files_update", "open new file failed: %s", strerror(errno));
        io_uring_unregister_files(&ring);
        close(fds[1]);
        unlink(filepath1);
        unlink(filepath2);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_register_files_update(&ring, 0, &new_fd, 1);
    if (ret < 0) {
        TEST_FAIL_MSG("register_files_update", "register_files_update failed: %s", strerror(-ret));
        close(new_fd);
        close(fds[1]);
        io_uring_unregister_files(&ring);
        unlink(filepath1);
        unlink(filepath2);
        unlink(filepath3);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_unregister_files(&ring);
    close(new_fd);
    close(fds[1]);
    unlink(filepath1);
    unlink(filepath2);
    unlink(filepath3);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("register_files_update (IORING_REGISTER_FILES_UPDATE)");
    return TEST_PASS;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring Registered Files Tests ===\n\n");

    test_register_files();
    test_read_with_fixed_file();
    test_write_with_fixed_file();
    test_fixed_file_with_fixed_buffer();
    test_register_files_update();

    print_summary("Registered Files Tests");
    return g_fail_count > 0 ? 1 : 0;
}
