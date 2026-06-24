#include "common.h"
#include <poll.h>
#include <sys/epoll.h>

static const char *test_dir;

static int test_nop(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("nop", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_nop(sqe);
    io_uring_sqe_set_data64(sqe, 0xFF);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("nop", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != 0 || cqe->user_data != 0xFF) {
        TEST_FAIL_MSG("nop", "cqe user_data=%llu res=%d",
                      (unsigned long long)cqe->user_data, cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("nop (IORING_OP_NOP)");
    return TEST_PASS;
}

static int test_timeout_variants(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct __kernel_timespec ts_short = {.tv_sec = 0, .tv_nsec = 100000000};
    struct __kernel_timespec ts_long = {.tv_sec = 10, .tv_nsec = 0};
    int ret;
    int result = TEST_FAIL;
    int seen_short = 0;
    int seen_cancel = 0;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("timeout_variants", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout(sqe, &ts_short, 0, 0);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("timeout_variants", "short timeout submit/wait failed: %s", strerror(-ret));
        goto cleanup;
    }

    if (cqe->user_data != 1 || cqe->res != -ETIME) {
        TEST_FAIL_MSG("timeout_variants", "short timeout cqe user_data=%llu res=%d",
                      (unsigned long long)cqe->user_data, cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }
    io_uring_cqe_seen(&ring, cqe);
    seen_short = 1;

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout(sqe, &ts_long, 0, 0);
    io_uring_sqe_set_data64(sqe, 10);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout_remove(sqe, 10, 0);
    io_uring_sqe_set_data64(sqe, 11);

    ret = io_uring_submit(&ring);
    if (ret != 2) {
        TEST_FAIL_MSG("timeout_variants", "submit returned %d, expected 2", ret);
        goto cleanup;
    }

    for (int i = 0; i < 2; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("timeout_variants", "wait_cqe failed: %s", strerror(-ret));
            goto cleanup;
        }

        if (cqe->user_data == 11) {
            if (cqe->res == 0 || cqe->res == -EALREADY || cqe->res == -ENOENT)
                seen_cancel = 1;
            else {
                TEST_FAIL_MSG("timeout_variants", "timeout remove res=%d (%s)", cqe->res, strerror(-cqe->res));
                io_uring_cqe_seen(&ring, cqe);
                goto cleanup;
            }
        } else if (cqe->user_data == 10) {
            if (cqe->res != -ECANCELED && cqe->res != -ETIME) {
                TEST_FAIL_MSG("timeout_variants", "long timeout completion res=%d unexpected", cqe->res);
                io_uring_cqe_seen(&ring, cqe);
                goto cleanup;
            }
        } else {
            TEST_FAIL_MSG("timeout_variants", "unexpected user_data=%llu", (unsigned long long)cqe->user_data);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }
        io_uring_cqe_seen(&ring, cqe);
    }

    if (!seen_short || !seen_cancel) {
        TEST_FAIL_MSG("timeout_variants", "short=%d cancel=%d", seen_short, seen_cancel);
        goto cleanup;
    }

    result = TEST_PASS;

cleanup:
    io_uring_queue_exit(&ring);
    if (result == TEST_PASS)
        TEST_PASS_MSG("timeout_variants (IORING_OP_TIMEOUT + IORING_OP_TIMEOUT_REMOVE)");
    return result;
}

static int test_linked_sqes(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int fd = -1;
    int ret;
    int result = TEST_FAIL;
    int seen_write = 0;
    int seen_fsync = 0;
    int seen_read = 0;

    snprintf(filepath, sizeof(filepath), "%s/linked_sqes_testfile", test_dir);

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("linked_sqes", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("linked_sqes", "open failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    memset(write_buf, 'L', sizeof(write_buf));
    memset(read_buf, 0, sizeof(read_buf));

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_write(sqe, fd, write_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 20);
    io_uring_sqe_set_flags(sqe, IOSQE_IO_LINK);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_fsync(sqe, fd, 0);
    io_uring_sqe_set_data64(sqe, 21);
    io_uring_sqe_set_flags(sqe, IOSQE_IO_LINK);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, read_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 22);

    ret = io_uring_submit(&ring);
    if (ret != 3) {
        TEST_FAIL_MSG("linked_sqes", "submit returned %d, expected 3", ret);
        goto cleanup;
    }

    for (int i = 0; i < 3; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("linked_sqes", "wait_cqe[%d] failed: %s", i, strerror(-ret));
            goto cleanup;
        }

        if (cqe->res < 0) {
            TEST_FAIL_MSG("linked_sqes", "cqe user_data=%llu res=%d",
                          (unsigned long long)cqe->user_data, cqe->res);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }

        if (cqe->user_data == 20) {
            if (cqe->res != BLOCK_SIZE) {
                TEST_FAIL_MSG("linked_sqes", "write res=%d expected=%d", cqe->res, BLOCK_SIZE);
                io_uring_cqe_seen(&ring, cqe);
                goto cleanup;
            }
            seen_write++;
        } else if (cqe->user_data == 21) {
            if (cqe->res != 0) {
                TEST_FAIL_MSG("linked_sqes", "fsync res=%d expected=0", cqe->res);
                io_uring_cqe_seen(&ring, cqe);
                goto cleanup;
            }
            seen_fsync++;
        } else if (cqe->user_data == 22) {
            if (cqe->res != BLOCK_SIZE) {
                TEST_FAIL_MSG("linked_sqes", "read res=%d expected=%d", cqe->res, BLOCK_SIZE);
                io_uring_cqe_seen(&ring, cqe);
                goto cleanup;
            }
            seen_read++;
        } else {
            TEST_FAIL_MSG("linked_sqes", "unexpected user_data=%llu", (unsigned long long)cqe->user_data);
            io_uring_cqe_seen(&ring, cqe);
            goto cleanup;
        }

        io_uring_cqe_seen(&ring, cqe);
    }

    if (seen_write != 1 || seen_fsync != 1 || seen_read != 1) {
        TEST_FAIL_MSG("linked_sqes", "mapping mismatch: w=%d f=%d r=%d", seen_write, seen_fsync, seen_read);
        goto cleanup;
    }

    if (memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("linked_sqes", "data integrity check failed");
        goto cleanup;
    }

    result = TEST_PASS;

cleanup:
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (result == TEST_PASS)
        TEST_PASS_MSG("linked_sqes (write -> fsync -> read chain)");
    return result;
}


static int test_provide_buffers(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char *buf;
    int ret;
    int bgid = 1;
    int bid = 0;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("provide_buffers", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    buf = malloc(BLOCK_SIZE);
    if (!buf) {
        TEST_FAIL_MSG("provide_buffers", "malloc failed");
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_provide_buffers(sqe, buf, BLOCK_SIZE, 1, bgid, bid);
    io_uring_sqe_set_data64(sqe, 50);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("provide_buffers", "submit/wait failed: %s", strerror(-ret));
        free(buf);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->user_data != 50 || cqe->res < 0) {
        TEST_FAIL_MSG("provide_buffers", "cqe user_data=%llu res=%d",
                      (unsigned long long)cqe->user_data, cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        free(buf);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    free(buf);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("provide_buffers (IORING_OP_PROVIDE_BUFFERS)");
    return TEST_PASS;
}

static int test_iopoll(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf[BLOCK_SIZE];
    char filepath[512];
    int fd = -1;
    int ret;
    int result = TEST_FAIL;

    snprintf(filepath, sizeof(filepath), "%s/iopoll_testfile", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("iopoll", "create test file failed");
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("iopoll", "open failed: %s", strerror(errno));
        unlink(filepath);
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, IORING_SETUP_IOPOLL);
    if (ret < 0) {
        TEST_SKIP_MSG("iopoll", "ring init with IOPOLL failed: %s (may not be supported on this fs)", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    memset(buf, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 60);

    ret = io_uring_submit(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("iopoll", "submit failed: %s", strerror(-ret));
        goto cleanup;
    }

    int got_cqe = 0;
    for (int i = 0; i < 20; i++) {
        struct __kernel_timespec ts = {.tv_sec = 0, .tv_nsec = 50000000};
        ret = io_uring_wait_cqe_timeout(&ring, &cqe, &ts);
        if (ret == 0) {
            got_cqe = 1;
            break;
        }
        if (ret != -ETIME) {
            TEST_FAIL_MSG("iopoll", "wait_cqe_timeout failed: %s", strerror(-ret));
            goto cleanup;
        }
        io_uring_submit(&ring);
    }

    if (!got_cqe) {
        TEST_FAIL_MSG("iopoll", "failed to get completion with IOPOLL");
        goto cleanup;
    }

    if (cqe->user_data != 60) {
        TEST_FAIL_MSG("iopoll", "user_data mismatch: %llu", (unsigned long long)cqe->user_data);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res < 0) {
        if (cqe->res == -EOPNOTSUPP || cqe->res == -ENOTSUP) {
            TEST_SKIP_MSG("iopoll", "IORING_SETUP_IOPOLL not supported on this filesystem");
            io_uring_cqe_seen(&ring, cqe);
            close(fd);
            unlink(filepath);
            io_uring_queue_exit(&ring);
            return TEST_SKIP;
        }
        TEST_FAIL_MSG("iopoll", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("iopoll", "read res=%d expected=%d", cqe->res, BLOCK_SIZE);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    if (buf[0] != 'A') {
        TEST_FAIL_MSG("iopoll", "unexpected first byte=%c", buf[0]);
        io_uring_cqe_seen(&ring, cqe);
        goto cleanup;
    }

    io_uring_cqe_seen(&ring, cqe);
    result = TEST_PASS;

cleanup:
    if (fd >= 0)
        close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (result == TEST_PASS)
        TEST_PASS_MSG("iopoll (IORING_SETUP_IOPOLL)");
    return result;
}

int main(int argc, char *argv[])
{
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <test_dir>\n", argv[0]);
        return 1;
    }

    test_dir = argv[1];

    printf("\n=== io_uring Advanced Features Tests ===\n\n");

    test_nop();
    test_timeout_variants();
    test_linked_sqes();
    test_provide_buffers();
    test_iopoll();

    print_summary("Advanced Features Tests");
    return g_fail_count > 0 ? 1 : 0;
}
