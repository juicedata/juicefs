#include "common.h"
#include <poll.h>
#include <sys/eventfd.h>
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

    if (cqe->res != 0) {
        TEST_FAIL_MSG("nop", "nop cqe res=%d, expected 0", cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (io_uring_cqe_get_data64(cqe) != 0xFF) {
        TEST_FAIL_MSG("nop", "user_data mismatch");
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("nop (IORING_OP_NOP)");
    return TEST_PASS;
}

static int test_timeout(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct __kernel_timespec ts;
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("timeout", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    ts.tv_sec = 0;
    ts.tv_nsec = 100000000;

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout(sqe, &ts, 0, 0);
    io_uring_sqe_set_data64(sqe, 1);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("timeout", "submit/wait failed: %s", strerror(-ret));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0 && cqe->res != -ETIME) {
        TEST_FAIL_MSG("timeout", "timeout cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("timeout (IORING_OP_TIMEOUT)");
    return TEST_PASS;
}

static int test_timeout_cancel(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    struct __kernel_timespec ts;
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("timeout_cancel", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    ts.tv_sec = 10;
    ts.tv_nsec = 0;

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout(sqe, &ts, 0, 0);
    io_uring_sqe_set_data64(sqe, 10);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_timeout_remove(sqe, 10, 0);
    io_uring_sqe_set_data64(sqe, 11);

    ret = io_uring_submit(&ring);
    if (ret != 2) {
        TEST_FAIL_MSG("timeout_cancel", "submit failed: %d", ret);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int got_cancel = 0;
    for (int i = 0; i < 2; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("timeout_cancel", "wait_cqe failed: %s", strerror(-ret));
            io_uring_queue_exit(&ring);
            return TEST_FAIL;
        }

        if (cqe->user_data == 11) {
            if (cqe->res == 0)
                got_cancel = 1;
            else if (cqe->res == -EALREADY || cqe->res == -ENOENT)
                got_cancel = 1;
            else {
                TEST_FAIL_MSG("timeout_cancel", "cancel res=%d (%s)", cqe->res, strerror(-cqe->res));
                io_uring_cqe_seen(&ring, cqe);
                io_uring_queue_exit(&ring);
                return TEST_FAIL;
            }
        } else if (cqe->user_data == 10) {
            /* timeout CQE - expected after cancel */
        }

        io_uring_cqe_seen(&ring, cqe);
    }

    io_uring_queue_exit(&ring);
    if (got_cancel) {
        TEST_PASS_MSG("timeout_cancel (IORING_OP_TIMEOUT_REMOVE)");
        return TEST_PASS;
    }
    TEST_FAIL_MSG("timeout_cancel", "cancel not received");
    return TEST_FAIL;
}

static int test_linked_sqes(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char write_buf[BLOCK_SIZE];
    char read_buf[BLOCK_SIZE];
    int fd, ret;

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

    for (int i = 0; i < BLOCK_SIZE; i++)
        write_buf[i] = 'L';

    struct io_uring_sqe *sqe1 = io_uring_get_sqe(&ring);
    io_uring_prep_write(sqe1, fd, write_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe1, 20);
    io_uring_sqe_set_flags(sqe1, IOSQE_IO_LINK);

    struct io_uring_sqe *sqe2 = io_uring_get_sqe(&ring);
    io_uring_prep_fsync(sqe2, fd, 0);
    io_uring_sqe_set_data64(sqe2, 21);
    io_uring_sqe_set_flags(sqe2, IOSQE_IO_LINK);

    struct io_uring_sqe *sqe3 = io_uring_get_sqe(&ring);
    memset(read_buf, 0, BLOCK_SIZE);
    io_uring_prep_read(sqe3, fd, read_buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe3, 22);

    ret = io_uring_submit(&ring);
    if (ret != 3) {
        TEST_FAIL_MSG("linked_sqes", "submitted %d, expected 3", ret);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int all_ok = 1;
    for (int i = 0; i < 3; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("linked_sqes", "wait_cqe[%d] failed: %s", i, strerror(-ret));
            all_ok = 0;
            break;
        }
        if (cqe->res < 0) {
            TEST_FAIL_MSG("linked_sqes", "cqe[%d] res=%d (%s)", i, cqe->res, strerror(-cqe->res));
            all_ok = 0;
        }
        io_uring_cqe_seen(&ring, cqe);
    }

    if (all_ok && memcmp(write_buf, read_buf, BLOCK_SIZE) != 0) {
        TEST_FAIL_MSG("linked_sqes", "data integrity check failed");
        all_ok = 0;
    }

    close(fd);
    unlink(filepath);
    io_uring_queue_exit(&ring);

    if (all_ok) {
        TEST_PASS_MSG("linked_sqes (write -> fsync -> read chain)");
        return TEST_PASS;
    }
    return TEST_FAIL;
}

static int test_poll_add(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int efd, ret;
    uint64_t val = 1;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("poll_add", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    efd = eventfd(0, EFD_NONBLOCK);
    if (efd < 0) {
        TEST_FAIL_MSG("poll_add", "eventfd failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_poll_add(sqe, efd, POLLIN);
    io_uring_sqe_set_data64(sqe, 30);

    ret = io_uring_submit(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("poll_add", "submit failed: %s", strerror(-ret));
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (write(efd, &val, sizeof(val)) != sizeof(val)) {
        TEST_FAIL_MSG("poll_add", "eventfd write failed: %s", strerror(errno));
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ret = io_uring_wait_cqe(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("poll_add", "wait_cqe failed: %s", strerror(-ret));
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("poll_add", "poll_add cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (!(cqe->res & POLLIN)) {
        TEST_FAIL_MSG("poll_add", "POLLIN not set in result: 0x%x", cqe->res);
        io_uring_cqe_seen(&ring, cqe);
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(efd);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("poll_add (IORING_OP_POLL_ADD)");
    return TEST_PASS;
}

static int test_poll_remove(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int efd, ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("poll_remove", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    efd = eventfd(0, EFD_NONBLOCK);
    if (efd < 0) {
        TEST_FAIL_MSG("poll_remove", "eventfd failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_poll_add(sqe, efd, POLLIN);
    io_uring_sqe_set_data64(sqe, 40);

    sqe = io_uring_get_sqe(&ring);
    io_uring_prep_poll_remove(sqe, 40);
    io_uring_sqe_set_data64(sqe, 41);

    ret = io_uring_submit(&ring);
    if (ret != 2) {
        TEST_FAIL_MSG("poll_remove", "submit failed: %d", ret);
        close(efd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int got_cancel = 0;
    for (int i = 0; i < 2; i++) {
        ret = io_uring_wait_cqe(&ring, &cqe);
        if (ret < 0) {
            TEST_FAIL_MSG("poll_remove", "wait_cqe failed: %s", strerror(-ret));
            close(efd);
            io_uring_queue_exit(&ring);
            return TEST_FAIL;
        }

        if (cqe->user_data == 41) {
            if (cqe->res == 0)
                got_cancel = 1;
            else if (cqe->res == -EALREADY || cqe->res == -ENOENT)
                got_cancel = 1;
            else {
                TEST_FAIL_MSG("poll_remove", "poll_remove res=%d (%s)", cqe->res, strerror(-cqe->res));
                io_uring_cqe_seen(&ring, cqe);
                close(efd);
                io_uring_queue_exit(&ring);
                return TEST_FAIL;
            }
        }
        io_uring_cqe_seen(&ring, cqe);
    }

    close(efd);
    io_uring_queue_exit(&ring);
    if (got_cancel) {
        TEST_PASS_MSG("poll_remove (IORING_OP_POLL_REMOVE)");
        return TEST_PASS;
    }
    TEST_FAIL_MSG("poll_remove", "cancel not received");
    return TEST_FAIL;
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

    if (cqe->res < 0) {
        TEST_FAIL_MSG("provide_buffers", "provide_buffers cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
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
    int fd, ret;

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
        close(fd);
        unlink(filepath);
        TEST_SKIP_MSG("iopoll", "ring init with IOPOLL failed: %s (may not be supported on this fs)", strerror(-ret));
        return TEST_SKIP;
    }

    memset(buf, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 60);

    ret = io_uring_submit(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("iopoll", "submit failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    int got_cqe = 0;
    for (int i = 0; i < 100; i++) {
        ret = io_uring_wait_cqe_timeout(&ring, &cqe,
                &(struct __kernel_timespec){.tv_sec = 1, .tv_nsec = 0});
        if (ret == 0) {
            got_cqe = 1;
            break;
        }
        io_uring_submit(&ring);
    }

    if (!got_cqe) {
        TEST_FAIL_MSG("iopoll", "failed to get completion with IOPOLL");
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        if (cqe->res == -EOPNOTSUPP) {
            TEST_SKIP_MSG("iopoll", "IORING_SETUP_IOPOLL not supported on this filesystem");
            io_uring_cqe_seen(&ring, cqe);
            close(fd);
            unlink(filepath);
            io_uring_queue_exit(&ring);
            return TEST_SKIP;
        }
        TEST_FAIL_MSG("iopoll", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
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
    TEST_PASS_MSG("iopoll (IORING_SETUP_IOPOLL)");
    return TEST_PASS;
}

static int test_sqpoll(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char buf[BLOCK_SIZE];
    char filepath[512];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/sqpoll_testfile", test_dir);

    if (create_test_file(filepath, TEST_FILE_SIZE) < 0) {
        TEST_FAIL_MSG("sqpoll", "create test file failed");
        return TEST_FAIL;
    }

    fd = open(filepath, O_RDONLY);
    if (fd < 0) {
        TEST_FAIL_MSG("sqpoll", "open failed: %s", strerror(errno));
        unlink(filepath);
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, IORING_SETUP_SQPOLL);
    if (ret < 0) {
        close(fd);
        unlink(filepath);
        TEST_SKIP_MSG("sqpoll", "ring init with SQPOLL failed: %s (requires CAP_SYS_NICE or root)", strerror(-ret));
        return TEST_SKIP;
    }

    memset(buf, 0, BLOCK_SIZE);
    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_read(sqe, fd, buf, BLOCK_SIZE, 0);
    io_uring_sqe_set_data64(sqe, 70);

    ret = io_uring_submit(&ring);
    if (ret < 0) {
        TEST_FAIL_MSG("sqpoll", "submit failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    usleep(100000);

    ret = io_uring_wait_cqe(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("sqpoll", "wait_cqe failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("sqpoll", "read cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res != BLOCK_SIZE) {
        TEST_FAIL_MSG("sqpoll", "read %d bytes, expected %d", cqe->res, BLOCK_SIZE);
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
    TEST_PASS_MSG("sqpoll (IORING_SETUP_SQPOLL)");
    return TEST_PASS;
}

static int test_sync_file_range(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    char filepath[512];
    char buf[BLOCK_SIZE];
    int fd, ret;

    snprintf(filepath, sizeof(filepath), "%s/sync_file_range_testfile", test_dir);

    fd = open(filepath, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) {
        TEST_FAIL_MSG("sync_file_range", "open failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    memset(buf, 'R', BLOCK_SIZE);
    if (write(fd, buf, BLOCK_SIZE) < 0) {
        close(fd);
        unlink(filepath);
        TEST_FAIL_MSG("sync_file_range", "write failed: %s", strerror(errno));
        return TEST_FAIL;
    }

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("sync_file_range", "ring init failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        return TEST_SKIP;
    }

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_sync_file_range(sqe, fd, BLOCK_SIZE, 0, SYNC_FILE_RANGE_WRITE);
    io_uring_sqe_set_data64(sqe, 80);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("sync_file_range", "submit/wait failed: %s", strerror(-ret));
        close(fd);
        unlink(filepath);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("sync_file_range", "cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
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
    TEST_PASS_MSG("sync_file_range (IORING_OP_SYNC_FILE_RANGE)");
    return TEST_PASS;
}

static int test_epoll_ctl(void)
{
    struct io_uring ring;
    struct io_uring_cqe *cqe;
    int epfd, pfd[2];
    struct epoll_event ev;
    int ret;

    ret = init_ring(&ring, QUEUE_DEPTH, 0);
    if (ret < 0) {
        TEST_SKIP_MSG("epoll_ctl", "ring init failed: %s", strerror(-ret));
        return TEST_SKIP;
    }

    epfd = epoll_create1(0);
    if (epfd < 0) {
        TEST_FAIL_MSG("epoll_ctl", "epoll_create1 failed: %s", strerror(errno));
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (pipe(pfd) < 0) {
        TEST_FAIL_MSG("epoll_ctl", "pipe failed: %s", strerror(errno));
        close(epfd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    ev.events = EPOLLIN;
    ev.data.fd = pfd[0];

    struct io_uring_sqe *sqe = io_uring_get_sqe(&ring);
    io_uring_prep_epoll_ctl(sqe, epfd, pfd[0], EPOLL_CTL_ADD, &ev);
    io_uring_sqe_set_data64(sqe, 90);

    ret = submit_and_wait(&ring, &cqe);
    if (ret < 0) {
        TEST_FAIL_MSG("epoll_ctl", "submit/wait failed: %s", strerror(-ret));
        close(pfd[0]); close(pfd[1]); close(epfd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    if (cqe->res < 0) {
        TEST_FAIL_MSG("epoll_ctl", "epoll_ctl cqe res=%d (%s)", cqe->res, strerror(-cqe->res));
        io_uring_cqe_seen(&ring, cqe);
        close(pfd[0]); close(pfd[1]); close(epfd);
        io_uring_queue_exit(&ring);
        return TEST_FAIL;
    }

    io_uring_cqe_seen(&ring, cqe);
    close(pfd[0]); close(pfd[1]); close(epfd);
    io_uring_queue_exit(&ring);
    TEST_PASS_MSG("epoll_ctl (IORING_OP_EPOLL_CTL)");
    return TEST_PASS;
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
    test_timeout();
    test_timeout_cancel();
    test_linked_sqes();
    test_poll_add();
    test_poll_remove();
    test_provide_buffers();
    test_iopoll();
    test_sqpoll();
    test_sync_file_range();
    test_epoll_ctl();

    print_summary("Advanced Features Tests");
    return g_fail_count > 0 ? 1 : 0;
}
