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

static int test_splice_offset_and_small_chunks(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2] = {-1, -1};
    int fd = -1, ret;
    int result = TEST_FAIL;
    int chunk_size = 512;
    int chunks = BLOCK_SIZE / chunk_size;
    off_t offset = BLOCK_SIZE * 2;
    int total_spliced = 0;
    int seen[8];
    off_t pos_before;
    off_t pos_after;

    snprintf(filepath, sizeof(filepath), "%s/splice_offset_chunks_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_offset_chunks", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_offset_chunks", "open file failed: %s", strerror(errno));
        goto cleanup;
    }

    memset(write_buf, 'C', BLOCK_SIZE);
    if (lseek(fd, offset, SEEK_SET) < 0 || write(fd, write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_offset_chunks", "write at offset failed: %s", strerror(errno));
        goto cleanup;
    }

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_offset_chunks", "pipe failed: %s", strerror(errno));
        goto cleanup;
    }

    pos_before = lseek(fd, 0, SEEK_CUR);

    for (int i = 0; i < chunks; i++) {
        struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
        if (!sqe) {
            TEST_FAIL_MSG("splice_offset_chunks", "io_uring_get_sqe failed at chunk=%d", i);
            goto cleanup;
        }

        io_uring_prep_splice(sqe, fd, offset + i * chunk_size, pipefd[1], -1, chunk_size, 0);
        io_uring_sqe_set_data64(sqe, 0x500 + i);
    }

    ret = io_uring_submit(&ring);
    if (ret != chunks) {
        TEST_FAIL_MSG("splice_offset_chunks", "submitted %d chunks, expected %d", ret, chunks);
        goto cleanup;
    }

    memset(seen, 0, sizeof(seen));

    for (int i = 0; i < chunks; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("splice_offset_chunks", "wait_cqe failed: %s", strerror(-ret));
            goto cleanup;
        }

        unsigned long long ud = (unsigned long long)io_uring_cqe_get_data64(cqe);
        if (ud < 0x500 || ud >= (unsigned long long)(0x500 + chunks)) {
            TEST_FAIL_MSG("splice_offset_chunks", "unexpected user_data=%llu", ud);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }

        int idx = (int)(ud - 0x500);
        if (seen[idx]) {
            TEST_FAIL_MSG("splice_offset_chunks", "duplicate completion for chunk=%d", idx);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }
        seen[idx] = 1;

        if (cqe->res != chunk_size) {
            TEST_FAIL_MSG("splice_offset_chunks", "chunk %d moved %d bytes, expected %d", idx, cqe->res, chunk_size);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }

        total_spliced += cqe->res;
        io_uring_cqe_seen(&ring, cqe);
    }

    if (total_spliced != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_offset_chunks", "total spliced %d, expected %d", total_spliced, BLOCK_SIZE);
        goto cleanup;
    }

    ssize_t nread = read(pipefd[0], read_buf, BLOCK_SIZE);
    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_offset_chunks", "pipe read returned %zd", nread);
        goto cleanup;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_offset_chunks", "data integrity check failed");
        goto cleanup;
    }

    pos_after = lseek(fd, 0, SEEK_CUR);
    if (pos_before != pos_after) {
        TEST_FAIL_MSG("splice_offset_chunks", "file position changed unexpectedly: before=%lld after=%lld",
                      (long long)pos_before, (long long)pos_after);
        goto cleanup;
    }

    TEST_PASS_MSG("splice_offset_chunks (IORING_OP_SPLICE: offset + 8x512B chunks)");
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

static int test_tee(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int pipe_in[2] = {-1, -1};
    int pipe_out[2] = {-1, -1};
    char write_buf[BLOCK_SIZE];
    char read_buf1[BLOCK_SIZE];
    char read_buf2[BLOCK_SIZE];
    int ret;
    int result = TEST_FAIL;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("tee", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (pipe(pipe_in) < 0 || pipe(pipe_out) < 0) {
        TEST_FAIL_MSG("tee", "pipe failed: %s", strerror(errno));
        goto cleanup;
    }

    memset(write_buf, 'T', BLOCK_SIZE);
    if (write(pipe_in[1], write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "write to pipe_in failed: %s", strerror(errno));
        goto cleanup;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    if (!sqe) {
        TEST_FAIL_MSG("tee", "io_uring_get_sqe returned NULL");
        goto cleanup;
    }

    io_uring_prep_tee(sqe, pipe_in[0], pipe_out[1], BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 0x403);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("tee", "submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->res < 0 || cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "tee cqe res=%d, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (io_uring_cqe_get_data64(cqe) != 0x403) {
        TEST_FAIL_MSG("tee", "user_data mismatch: %llu", (unsigned long long)io_uring_cqe_get_data64(cqe));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);

    ssize_t nread1 = read(pipe_in[0], read_buf1, BLOCK_SIZE);
    ssize_t nread2 = read(pipe_out[0], read_buf2, BLOCK_SIZE);

    if (nread1 != BLOCK_SIZE || nread2 != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "pipe reads returned %zd, %zd", nread1, nread2);
        goto cleanup;
    }

    if (memcmp(write_buf, read_buf1, BLOCK_SIZE) != 0 || memcmp(write_buf, read_buf2, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("tee", "data integrity check failed");
        goto cleanup;
    }

    TEST_PASS_MSG("tee (IORING_OP_TEE: pipe -> pipe duplicate)");
    result = TEST_PASS;

cleanup:
    if (pipe_in[0] >= 0)
        close(pipe_in[0]);
    if (pipe_in[1] >= 0)
        close(pipe_in[1]);
    if (pipe_out[0] >= 0)
        close(pipe_out[0]);
    if (pipe_out[1] >= 0)
        close(pipe_out[1]);
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
    //test_splice_pipe_to_file();
    //test_splice_offset_and_small_chunks();
    //test_tee();

    print_summary("Splice Tests");
    return g_fail_count > 0 ? 1 : 0;
}
