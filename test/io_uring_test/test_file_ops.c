#include "common.h"
#include <linux/stat.h>

static const char *test_dir;

static int test_openat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/openat_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("openat", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_openat(sqe, AT_FDCWD, filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("openat", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("openat", "openat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int opened_fd = cqe->res;
    io_uring_cqe_seen(&ring, cqe);
    close(opened_fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("openat (IORING_OP_OPENAT)");
    return TEST_PASS;
}

static int test_close(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/close_testfile", test_dir);

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("close", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("close", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_close(sqe, fd);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("close", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        unlink(filepath);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("close", "close cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        unlink(filepath);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("close (IORING_OP_CLOSE)");
    return TEST_PASS;
}

static int test_statx(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct statx stxbuf;
    char filepath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/statx_testfile", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("statx", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    char data[] = "statx test data";
    if (write(fd, data, sizeof(data)) < 0) {
        close(fd);
        unlink(filepath);
        TEST_FAIL_MSG("statx", "write failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    close(fd);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("statx", "ring init failed: %s", strerror(-ret));
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_statx(sqe, AT_FDCWD, filepath, 0, STATX_ALL, &stxbuf);
    io_uring_sqe_set_data64(sqe, 3);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("statx", "submit/wait failed: %s", strerror(-ret));
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("statx", "statx cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (!(stxbuf.stx_mask & STATX_SIZE)) {
        TEST_FAIL_MSG("statx", "STATX_SIZE not returned in mask");
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (stxbuf.stx_size != sizeof(data)) {
        TEST_FAIL_MSG("statx", "file size=%llu, expected %zu",
                       (unsigned long long)stxbuf.stx_size, sizeof(data));
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("statx (IORING_OP_STATX)");
    return TEST_PASS;
}

static int test_fsync(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char buf[BLOCK_SIZE];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fsync_testfile", test_dir);

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("fsync", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    memset(buf, 'F', BLOCK_SIZE);
    if (write(fd, buf, BLOCK_SIZE) < 0) {
        close(fd);
        unlink(filepath);
        TEST_FAIL_MSG("fsync", "write failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fsync", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_fsync(sqe, fd, 0);
    io_uring_sqe_set_data64(sqe, 4);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fsync", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fsync", "fsync cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fsync (IORING_OP_FSYNC)");
    return TEST_PASS;
}

static int test_fdatasync(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char buf[BLOCK_SIZE];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fdatasync_testfile", test_dir);

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("fdatasync", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    memset(buf, 'D', BLOCK_SIZE);
    if (write(fd, buf, BLOCK_SIZE) < 0) {
        close(fd);
        unlink(filepath);
        TEST_FAIL_MSG("fdatasync", "write failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fdatasync", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_fsync(sqe, fd, IORING_FSYNC_DATASYNC);
    io_uring_sqe_set_data64(sqe, 5);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fdatasync", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fdatasync", "fdatasync cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fdatasync (IORING_OP_FSYNC with IORING_FSYNC_DATASYNC)");
    return TEST_PASS;
}

static int test_fallocate(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fallocate_testfile", test_dir);

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("fallocate", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fallocate", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_fallocate(sqe, fd, 0, 0, BLOCK_SIZE * 4);
    io_uring_sqe_set_data64(sqe, 6);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fallocate", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fallocate", "fallocate cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    struct stat st;
    if (fstat(fd, &st) < 0) {
        TEST_FAIL_MSG("fallocate", "fstat failed: %s", strerror(errno));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (st.st_size != BLOCK_SIZE * 4) {
        TEST_FAIL_MSG("fallocate", "file size=%ld, expected %d", (long)st.st_size, BLOCK_SIZE * 4);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fallocate (IORING_OP_FALLOCATE)");
    return TEST_PASS;
}

static int test_openat2(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct open_how how;
    char filepath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/openat2_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("openat2", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    memset(&how, 0, sizeof(how));
    how.flags = O_RDWR | O_CREAT | O_TRUNC;
    how.mode = 0644;
    how.resolve = 0;

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_openat2(sqe, AT_FDCWD, filepath, &how);
    io_uring_sqe_set_data64(sqe, 7);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("openat2", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("openat2", "openat2 cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int opened_fd = cqe->res;
    io_uring_cqe_seen(&ring, cqe);
    close(opened_fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("openat2 (IORING_OP_OPENAT2)");
    return TEST_PASS;
}

static int test_fadvise(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/fadvise_testfile", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("fadvise", "create test file failed");
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("fadvise", "open failed: %s", strerror(errno));
        unlink(filepath);
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("fadvise", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_fadvise(sqe, fd, 0, TEST_FILE_SIZE, POSIX_FADV_SEQUENTIAL);
    io_uring_sqe_set_data64(sqe, 8);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("fadvise", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("fadvise", "fadvise cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("fadvise (IORING_OP_FADVISE)");
    return TEST_PASS;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring File Operations Tests ===\n\n");

    test_openat();
    test_close();
    test_statx();
    test_fsync();
    test_fdatasync();
    test_fallocate();
    test_openat2();
    test_fadvise();

    print_summary("File Operations Tests");
    return g_fail_count > 0 ? 1 : 0;
}
