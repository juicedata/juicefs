#include "common.h"

static const char *test_dir;

static int test_mkdirat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char dirpath[512];
    int ret;

    snprintf(dirpath, sizeof(dirpath), "%s/mkdirat_testdir", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("mkdirat", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_mkdirat(sqe, AT_FDCWD, dirpath, 0755);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("mkdirat", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("mkdirat", "mkdirat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    struct stat st;
    if (stat(dirpath, &st) < 0 || !S_ISDIR(st.st_mode)) {
        TEST_FAIL_MSG("mkdirat", "directory not created properly");
        rmdir(dirpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    rmdir(dirpath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("mkdirat (IORING_OP_MKDIRAT)");
    return TEST_PASS;
}

static int test_unlinkat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/unlinkat_testfile", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("unlinkat", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    close(fd);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("unlinkat", "ring init failed: %s", strerror(-ret));
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_unlinkat(sqe, AT_FDCWD, filepath, 0);
    io_uring_sqe_set_data64(sqe, 2);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("unlinkat", "submit/wait failed: %s", strerror(-ret));
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("unlinkat", "unlinkat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (access(filepath, F_OK) == 0) {
        TEST_FAIL_MSG("unlinkat", "file still exists after unlinkat");
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("unlinkat (IORING_OP_UNLINKAT)");
    return TEST_PASS;
}

static int test_renameat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char oldpath[512], newpath[512];
    int ret;

    snprintf(oldpath, sizeof(oldpath), "%s/renameat_old", test_dir);
    snprintf(newpath, sizeof(newpath), "%s/renameat_new", test_dir);

    int fd = open(oldpath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("renameat", "open old failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    close(fd);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("renameat", "ring init failed: %s", strerror(-ret));
        unlink(oldpath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_renameat(sqe, AT_FDCWD, oldpath, AT_FDCWD, newpath, 0);
    io_uring_sqe_set_data64(sqe, 3);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("renameat", "submit/wait failed: %s", strerror(-ret));
        unlink(oldpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("renameat", "renameat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        unlink(oldpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (access(oldpath, F_OK) == 0 || access(newpath, F_OK) != 0) {
        TEST_FAIL_MSG("renameat", "rename did not work correctly");
        unlink(oldpath);
        unlink(newpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    unlink(newpath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("renameat (IORING_OP_RENAMEAT)");
    return TEST_PASS;
}

static int test_unlinkat_directory(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char dirpath[512];
    int ret;

    snprintf(dirpath, sizeof(dirpath), "%s/unlinkat_testdir", test_dir);

    if (mkdir(dirpath, 0755) < 0) {
        TEST_FAIL_MSG("unlinkat_directory", "mkdir failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("unlinkat_directory", "ring init failed: %s", strerror(-ret));
        rmdir(dirpath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_unlinkat(sqe, AT_FDCWD, dirpath, AT_REMOVEDIR);
    io_uring_sqe_set_data64(sqe, 4);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("unlinkat_directory", "submit/wait failed: %s", strerror(-ret));
        rmdir(dirpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("unlinkat_directory", "unlinkat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        rmdir(dirpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (access(dirpath, F_OK) == 0) {
        TEST_FAIL_MSG("unlinkat_directory", "directory still exists after unlinkat");
        rmdir(dirpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("unlinkat_directory (IORING_OP_UNLINKAT with AT_REMOVEDIR)");
    return TEST_PASS;
}

static int test_linkat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512], linkpath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/linkat_original", test_dir);
    snprintf(linkpath, sizeof(linkpath), "%s/linkat_link", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("linkat", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    close(fd);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("linkat", "ring init failed: %s", strerror(-ret));
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_linkat(sqe, AT_FDCWD, filepath, AT_FDCWD, linkpath, 0);
    io_uring_sqe_set_data64(sqe, 5);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("linkat", "submit/wait failed: %s", strerror(-ret));
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("linkat", "linkat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    if (access(linkpath, F_OK) != 0) {
        TEST_FAIL_MSG("linkat", "hard link not created");
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    unlink(filepath);
    unlink(linkpath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("linkat (IORING_OP_LINKAT)");
    return TEST_PASS;
}

static int test_symlinkat(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512], linkpath[512];
    int ret;

    snprintf(filepath, sizeof(filepath), "%s/symlinkat_target", test_dir);
    snprintf(linkpath, sizeof(linkpath), "%s/symlinkat_link", test_dir);

    int fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("symlinkat", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }
    close(fd);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("symlinkat", "ring init failed: %s", strerror(-ret));
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_symlinkat(sqe, filepath, AT_FDCWD, linkpath);
    io_uring_sqe_set_data64(sqe, 6);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("symlinkat", "submit/wait failed: %s", strerror(-ret));
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("symlinkat", "symlinkat cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);

    struct stat st;
    if (lstat(linkpath, &st) < 0 || !S_ISLNK(st.st_mode)) {
        TEST_FAIL_MSG("symlinkat", "symlink not created properly");
        unlink(filepath);
        unlink(linkpath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    unlink(filepath);
    unlink(linkpath);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("symlinkat (IORING_OP_SYMLINKAT)");
    return TEST_PASS;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring Directory Operations Tests ===\n\n");

    test_mkdirat();
    test_unlinkat();
    test_renameat();
    test_unlinkat_directory();
    test_linkat();
    test_symlinkat();

    print_summary("Directory Operations Tests");
    return g_fail_count > 0 ? 1 : 0;
}
