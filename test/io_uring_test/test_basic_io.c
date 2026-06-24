#include "common.h"

static const char *test_file_path;

static int verify_alpha_pattern(const char *buf, size_t len, off_t start)
{
    for (size_t i = 0; i < len; i++) {
        off_t in_block = (start + (off_t)i) % BLOCK_SIZE;
        char expected = 'A' + (in_block % 26);
        if (buf[i] != expected)
            return 0;
    }
    return 1;
}

static int test_basic_read(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf0[BLOCK_SIZE];
    char buf1[BLOCK_SIZE];
    int fd, ret;
    off_t initial_pos;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("basic_read", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(test_file_path, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("basic_read", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    initial_pos = lseek(fd, 0, SEEK_CUR);
    if (initial_pos < 0) {
        TEST_FAIL_MSG("basic_read", "lseek failed: %s", strerror(errno));
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    memset(buf0, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, buf0, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("basic_read", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->user_data != 1) {
        TEST_FAIL_MSG("basic_read", "unexpected user_data=%llu", (unsigned long long)cqe->user_data);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("basic_read", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("basic_read", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (!verify_alpha_pattern(buf0, BLOCK_SIZE, 0)) {
        TEST_FAIL_MSG("basic_read", "data integrity check failed");
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    memset(buf1, 0, BLOCK_SIZE);
    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, buf1, BLOCK_SIZE, BLOCK_SIZE);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("basic_read", "offset read submit/wait failed: %s", strerror(-ret));
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->user_data != 2) {
        TEST_FAIL_MSG("basic_read", "offset read unexpected user_data=%llu", (unsigned long long)cqe->user_data);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("basic_read", "offset read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (!verify_alpha_pattern(buf1, BLOCK_SIZE, BLOCK_SIZE)) {
        TEST_FAIL_MSG("basic_read", "offset read data integrity check failed");
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (lseek(fd, 0, SEEK_CUR) != initial_pos) {
        TEST_FAIL_MSG("basic_read", "file position changed by offset read");
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    close(fd);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("basic_read (IORING_OP_READ + offset semantics)");
    return TEST_PASS;
}

static int test_readv(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf1[BLOCK_SIZE / 2];
    char buf2[BLOCK_SIZE / 2];
    struct iovec iov[2];
    int fd, ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("readv", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(test_file_path, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("readv", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    iov[0].iov_base = buf1;
    iov[0].iov_len = BLOCK_SIZE / 2;
    iov[1].iov_base = buf2;
    iov[1].iov_len = BLOCK_SIZE / 2;

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_readv(sqe, fd, iov, 2, 0);
    io_uring_sqe_set_data64(sqe, 3);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("readv", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("readv", "readv cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("readv", "readv returned %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (!verify_alpha_pattern(buf1, BLOCK_SIZE / 2, 0) ||
        !verify_alpha_pattern(buf2, BLOCK_SIZE / 2, BLOCK_SIZE / 2)) {
        TEST_FAIL_MSG("readv", "scatter data integrity check failed");
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(fd);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("readv (IORING_OP_READV)");
    return TEST_PASS;
}

static int test_writev(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf1[BLOCK_SIZE / 2];
    char buf2[BLOCK_SIZE / 2];
    char verify_buf[BLOCK_SIZE];
    struct iovec iov[2];
    char tmp_path[512];
    int fd, ret;

    snprintf(tmp_path, sizeof(tmp_path), "%s.writev_test", test_file_path);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("writev", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(tmp_path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("writev", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE / 2; i++) {
        buf1[i] = 'X';
        buf2[i] = 'Y';
    }

    iov[0].iov_base = buf1;
    iov[0].iov_len = BLOCK_SIZE / 2;
    iov[1].iov_base = buf2;
    iov[1].iov_len = BLOCK_SIZE / 2;

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_writev(sqe, fd, iov, 2, 0);
    io_uring_sqe_set_data64(sqe, 4);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("writev", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("writev", "writev cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("writev", "writev returned %d bytes, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    fsync(fd);
    lseek(fd, 0, SEEK_SET);
    ssize_t nread = read(fd, verify_buf, BLOCK_SIZE);
    close(fd);

    if (nread != BLOCK_SIZE) {
        TEST_FAIL_MSG("writev", "verification read returned %zd", nread);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int valid = 1;
    for (int i = 0; i < BLOCK_SIZE / 2; i++) {
        if (verify_buf[i] != 'X' || verify_buf[i + BLOCK_SIZE / 2] != 'Y') {
            valid = 0;
            break;
        }
    }

    if (!valid) {
        TEST_FAIL_MSG("writev", "data integrity check failed");
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    unlink(tmp_path);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("writev (IORING_OP_WRITEV)");
    return TEST_PASS;
}

static int test_batch_io(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char *bufs[4];
    int seen[4] = {0};
    int fd, ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("batch_io", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(test_file_path, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("batch_io", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < 4; i++) {
        bufs[i] = malloc(BLOCK_SIZE);
        if (!bufs[i]) {
            for (int j = 0; j < i; j++) free(bufs[j]);
            close(fd);
            io_uring_queue_exit(&ring);
            TEST_FAIL_MSG("batch_io", "malloc failed");
            return TEST_FAIL;
        }
        memset(bufs[i], 0, BLOCK_SIZE);
    }

    for (int i = 0; i < 4; i++) {
        struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
        io_uring_prep_read(sqe, fd, bufs[i], BLOCK_SIZE, i * BLOCK_SIZE);
        io_uring_sqe_set_data64(sqe, 100 + i);
    }

    ret = io_uring_submit(&ring);
    if (ret != 4) {
        TEST_FAIL_MSG("batch_io", "submitted %d, expected 4", ret);
        for (int i = 0; i < 4; i++) free(bufs[i]);
        close(fd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int completed = 0;
    int all_ok = 1;
    for (int i = 0; i < 4; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("batch_io", "wait_cqe failed: %s", strerror(-ret));
            all_ok = 0;
            break;
        }

        int idx = (int)cqe->user_data - 100;
        if (idx < 0 || idx >= 4 || seen[idx]) {
            TEST_FAIL_MSG("batch_io", "unexpected or duplicate user_data=%llu",
                          (unsigned long long)cqe->user_data);
            all_ok = 0;
            io_uring_cqe_seen(&ring, cqe);
            completed++;
            continue;
        }

        seen[idx] = 1;

        if (cqe->res != BLOCK_SIZE) {
            TEST_FAIL_MSG("batch_io", "cqe(user_data=%llu) res=%d, expected %d",
                          (unsigned long long)cqe->user_data, cqe->res, BLOCK_SIZE);
            all_ok = 0;
        }

        if (cqe->res == BLOCK_SIZE && !verify_alpha_pattern(bufs[idx], BLOCK_SIZE, idx * BLOCK_SIZE)) {
            TEST_FAIL_MSG("batch_io", "data mismatch for request index=%d", idx);
            all_ok = 0;
        }

        io_uring_cqe_seen(&ring, cqe);
        completed++;
    }

    for (int i = 0; i < 4; i++) free(bufs[i]);
    close(fd);
    io_uring_queue_exit(&ring);

    if (all_ok && completed == 4) {
        TEST_PASS_MSG("batch_io (4 parallel IORING_OP_READ + user_data mapping)");
        return TEST_PASS;
    }
    return TEST_FAIL;
}

static int test_read_write_consistency(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe = NULL;
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    char tmp_path[512];
    int fd, ret;

    snprintf(tmp_path, sizeof(tmp_path), "%s.rw_consistency", test_file_path);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("rw_consistency", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(tmp_path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("rw_consistency", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = (char)(i & 0xFF);

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_write(sqe, fd, write_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 10);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("rw_consistency", "write submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("rw_consistency", "write cqe res=%d, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, read_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 11);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("rw_consistency", "read submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("rw_consistency", "read cqe res=%d, expected %d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("rw_consistency", "data mismatch after write+read");
        close(fd);
        unlink(tmp_path);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    close(fd);
    unlink(tmp_path);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("rw_consistency (write then read verify)");
    return TEST_PASS;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    char filepath[512];
    snprintf(filepath, sizeof(filepath), "%s/io_uring_basic_testfile", argv[1]);
    test_file_path = filepath;

    printf("\n=== io_uring Basic I/O Tests ===\n\n");

    if (create_test_file(test_file_path, TEST_FILE_SIZE) < 0) {
        fprintf(stderr, "Failed to create test file: %s\n", test_file_path);
        return 1;
    }

    test_basic_read();
    test_readv();
    test_writev();
    test_batch_io();
    test_read_write_consistency();

    unlink(test_file_path);

    print_summary("Basic I/O Tests");
    return g_fail_count > 0 ? 1 : 0;
}
