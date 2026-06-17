#include "common.h"

static const char *test_dir;

static int test_splice_file_to_pipe(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/splice_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_file_to_pipe", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "open file failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'S';

    if (write(fd, write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "write file failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    lseek(fd, 0, SEEK_SET);

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "pipe failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_splice(sqe, fd, 0, pipefd[1], -1, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "submit/wait failed: %s", strerror(-ret));
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "splice cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "splice moved %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    ssize_t nread = read(pipefd[0], read_buf, BLOCK_SIZE);
    close(pipefd[0]);
    close(pipefd[1]);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_file_to_pipe", "pipe read returned %zd", nread);
        return TEST_FAIL;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_file_to_pipe", "data integrity check failed");
        return TEST_FAIL;
    }

    TEST_PASS_MSG("splice_file_to_pipe (IORING_OP_SPLICE: file -> pipe)");
    return TEST_PASS;
}

static int test_splice_pipe_to_file(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/splice_pipe_to_file", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_pipe_to_file", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "pipe failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'P';

    if (write(pipefd[1], write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "write to pipe failed: %s", strerror(errno));
        close(pipefd[0]);
        close(pipefd[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "open file failed: %s", strerror(errno));
        close(pipefd[0]);
        close(pipefd[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_splice(sqe, pipefd[0], -1, fd, 0, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "submit/wait failed: %s", strerror(-ret));
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "splice cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "splice moved %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, read_buf, BLOCK_SIZE);
    close(pipefd[0]);
    close(pipefd[1]);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_pipe_to_file", "file read returned %zd", nread);
        return TEST_FAIL;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_pipe_to_file", "data integrity check failed");
        return TEST_FAIL;
    }

    TEST_PASS_MSG("splice_pipe_to_file (IORING_OP_SPLICE: pipe -> file)");
    return TEST_PASS;
}

static int test_splice_with_offset(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2];
    int fd, ret;
    off_t offset = BLOCK_SIZE * 2;

    snprintf(filepath, sizeof(filepath), "%s/splice_offset_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_with_offset", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_with_offset", "open file failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'O';

    if (lseek(fd, offset, SEEK_SET) < 0 || write(fd, write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_with_offset", "write file at offset failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    lseek(fd, offset, SEEK_SET);

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_with_offset", "pipe failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_splice(sqe, fd, offset, pipefd[1], -1, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 3);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("splice_with_offset", "submit/wait failed: %s", strerror(-ret));
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("splice_with_offset", "splice cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_with_offset", "splice moved %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(pipefd[0]);
        close(pipefd[1]);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    ssize_t nread = read(pipefd[0], read_buf, BLOCK_SIZE);
    close(pipefd[0]);
    close(pipefd[1]);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_with_offset", "pipe read returned %zd", nread);
        return TEST_FAIL;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_with_offset", "data integrity check failed");
        return TEST_FAIL;
    }

    TEST_PASS_MSG("splice_with_offset (IORING_OP_SPLICE with file offset)");
    return TEST_PASS;
}

static int test_tee(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int pipe_in[2], pipe_out[2];
    char write_buf[BLOCK_SIZE];
    char read_buf1[BLOCK_SIZE];
    char read_buf2[BLOCK_SIZE];
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("tee", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    if (pipe(pipe_in) < 0 || pipe(pipe_out) < 0) {
        TEST_FAIL_MSG("tee", "pipe failed: %s", strerror(errno));
        if (pipe_in[0] >= 0) { close(pipe_in[0]); close(pipe_in[1]); }
        if (pipe_out[0] >= 0) { close(pipe_out[0]); close(pipe_out[1]); }
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'T';

    if (write(pipe_in[1], write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "write to pipe_in failed: %s", strerror(errno));
        close(pipe_in[0]); close(pipe_in[1]);
        close(pipe_out[0]); close(pipe_out[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_tee(sqe, pipe_in[0], pipe_out[1], BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 4);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("tee", "submit/wait failed: %s", strerror(-ret));
        close(pipe_in[0]); close(pipe_in[1]);
        close(pipe_out[0]); close(pipe_out[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("tee", "tee cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(pipe_in[0]); close(pipe_in[1]);
        close(pipe_out[0]); close(pipe_out[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "tee moved %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(pipe_in[0]); close(pipe_in[1]);
        close(pipe_out[0]); close(pipe_out[1]);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    io_uring_cqe_seen(&ring, cqe);

    ssize_t nread1 = read(pipe_in[0], read_buf1, BLOCK_SIZE);
    ssize_t nread2 = read(pipe_out[0], read_buf2, BLOCK_SIZE);
    close(pipe_in[0]); close(pipe_in[1]);
    close(pipe_out[0]); close(pipe_out[1]);
    io_uring_queue_exit(&ring);

    if (nread1 != BLOCK_SIZE || nread2 != BLOCK_SIZE) {
        TEST_FAIL_MSG("tee", "pipe reads returned %zd, %zd", nread1, nread2);
        return TEST_FAIL;
    }

    if (memcmp(write_buf, read_buf1, BLOCK_SIZE) != 0 ||
        memcmp(write_buf, read_buf2, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("tee", "data integrity check failed");
        return TEST_FAIL;
    }

    TEST_PASS_MSG("tee (IORING_OP_TEE: pipe -> pipe duplicate)");
    return TEST_PASS;
}

static int test_splice_small_chunks(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int pipefd[2];
    int fd, ret;
    int chunk_size = 512;
    int chunks = BLOCK_SIZE / chunk_size;

    snprintf(filepath, sizeof(filepath), "%s/splice_chunks_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("splice_small_chunks", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("splice_small_chunks", "open file failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'C';

    if (write(fd, write_buf, BLOCK_SIZE) != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_small_chunks", "write file failed");
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }
    lseek(fd, 0, SEEK_SET);

    if (pipe(pipefd) < 0) {
        TEST_FAIL_MSG("splice_small_chunks", "pipe failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int total_spliced = 0;
    int all_ok = 1;
    for (int i = 0; i < chunks && all_ok; i++) {
        struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
        io_uring_prep_splice(sqe, fd, i * chunk_size, pipefd[1], -1, chunk_size, 0);
        io_uring_sqe_set_data64(sqe, 50 + i);

        ret = io_uring_submit(&ring);
        if (ret < 0) {
            TEST_FAIL_MSG("splice_small_chunks", "submit failed: %s", strerror(-ret));
            all_ok = 0;
            break;
        }

        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("splice_small_chunks", "wait_cqe failed: %s", strerror(-ret));
            all_ok = 0;
            break;
        }

        if (cqe->res != chunk_size) {
            TEST_FAIL_MSG("splice_small_chunks", "chunk %d: splice res=%d, expected %d",
                           i, cqe->res, chunk_size);
            all_ok = 0;
        } else {
            total_spliced += cqe->res;
        }
        io_uring_cqe_seen(&ring, cqe);
    }

    ssize_t nread = read(pipefd[0], read_buf, BLOCK_SIZE);
    close(pipefd[0]);
    close(pipefd[1]);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (!all_ok) return TEST_FAIL;

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("splice_small_chunks", "pipe read returned %zd", nread);
        return TEST_FAIL;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("splice_small_chunks", "data integrity check failed");
        return TEST_FAIL;
    }

    TEST_PASS_MSG("splice_small_chunks (IORING_OP_SPLICE: 8x512B chunks)");
    return TEST_PASS;
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
    test_splice_with_offset();
    test_tee();
    test_splice_small_chunks();

    print_summary("Splice Tests");
    return g_fail_count > 0 ? 1 : 0;
}
