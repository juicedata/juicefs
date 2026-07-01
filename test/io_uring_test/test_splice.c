#include "common.h"

static const char *test_dir;

static int test_splice_file_to_pipe(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2] = {-1, -1};
    int fd = -1, ret;
    int result = TEST_FAIL;
    off_t pos_before;
    off_t pos_after;

    snprintf(filepath, sizeof(filepath), "%s/splice_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_file_to_pipe", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "open file failed: %s", strerror(errno));
        goto cleanup;
    }

    memset(write_buf, 'S', BLOCK_SIZE);
    if (write(fd, write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "write file failed: %s", strerror(errno));
        goto cleanup;
    }

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "pipe failed: %s", strerror(errno));
        goto cleanup;
    }

    pos_before = lseek(fd, 0, SEEK_CUR);

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("splice_file_to_pipe", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }

    io_uring_prep_splice(sqe, fd, 0, pipefd[1], -1, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 0x401);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "splice cqe res=%d, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x401) {
        TEST_FAIL_MSG("splice_file_to_pipe", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);

    pos_after = lseek(fd, 0, SEEK_CUR);
    if (pos_before != pos_after) {
        TEST_FAIL_MSG("splice_file_to_pipe", "file position changed unexpectedly: before=%lld after=%lld",
                      (long long)pos_before, (long long)pos_after);
        goto cleanup;
    }

    ssize_t nread = read(pipefd[0], read_buf, BLOCK_SIZE);
    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "pipe read returned %zd", nread);
        goto cleanup;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "data integrity check failed");
        goto cleanup;
    }

    TEST_PASS_MSG("splice_file_to_pipe (IORING_OP_SPLICE: file -> pipe)");
    result = TEST_PASS;

cleanup:
    if (pipefd[0] >= 0)
        close(pipefd[0]);
    if (pipefd[1] >= 0)
        close(pipefd[1]);
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    return result;
}

static int test_splice_pipe_to_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2] = {-1, -1};
    int fd = -1, ret;
    int result = TEST_FAIL;

    snprintf(filepath, sizeof(filepath), "%s/splice_pipe_to_file", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_pipe_to_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "pipe failed: %s", strerror(errno));
        goto cleanup;
    }

    memset(write_buf, 'P', BLOCK_SIZE);
    if (write(pipefd[1], write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "write to pipe failed: %s", strerror(errno));
        goto cleanup;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "open file failed: %s", strerror(errno));
        goto cleanup;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("splice_pipe_to_file", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }

    io_uring_prep_splice(sqe, pipefd[0], -1, fd, 0, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 0x402);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "splice cqe res=%d, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x402) {
        TEST_FAIL_MSG("splice_pipe_to_file", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);
    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "file read returned %zd", nread);
        goto cleanup;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "data integrity check failed");
        goto cleanup;
    }

    TEST_PASS_MSG("splice_pipe_to_file (IORING_OP_SPLICE: pipe -> file)");
    result = TEST_PASS;

cleanup:
    if (pipefd[0] >= 0)
        close(pipefd[0]);
    if (pipefd[1] >= 0)
        close(pipefd[1]);
    if (fd >= 0)
        close(fd);
    unlink(filepath);
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

    printf("\n=== io_uring Splice Tests ===\n\n");

    test_splice_file_to_pipe();
    test_splice_pipe_to_file();

    print_summary("Splice Tests");
    return g_fail_count > 0 ? 1 : 0;
}
