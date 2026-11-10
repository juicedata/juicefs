#ifndef IO_URING_TEST_COMMON_H
#define IO_URING_TEST_COMMON_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/uio.h>
#include <liburing.h>

#ifdef BLOCK_SIZE
#undef BLOCK_SIZE
#endif
#define QUEUE_DEPTH 64
#define BLOCK_SIZE  4096
#define TEST_FILE_SIZE (128 * 1024)

typedef enum {
    TEST_PASS = 0,
    TEST_FAIL = 1,
    TEST_SKIP = 2,
} test_result_t;

static int g_pass_count = 0;
static int g_fail_count = 0;
static int g_skip_count = 0;

#define TEST_PASS_MSG(name) do { \
    printf("  [PASS] %s\n", name); \
    g_pass_count++; \
} while(0)

#define TEST_FAIL_MSG(name, ...) do { \
    printf("  [FAIL] %s: ", name); \
    printf(__VA_ARGS__); \
    printf("\n"); \
    g_fail_count++; \
} while(0)

#define TEST_SKIP_MSG(name, ...) do { \
    printf("  [SKIP] %s: ", name); \
    printf(__VA_ARGS__); \
    printf("\n"); \
    g_skip_count++; \
} while(0)

static inline int create_test_file(const char *path, size_t size)
{
    int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0)
        return -1;

    char buf[BLOCK_SIZE];
    memset(buf, 'A', BLOCK_SIZE);
    for (size_t i = 0; i < BLOCK_SIZE; i++)
        buf[i] = 'A' + (i % 26);

    size_t written = 0;
    while (written < size) {
        size_t to_write = size - written;
        if (to_write > BLOCK_SIZE)
            to_write = BLOCK_SIZE;
        ssize_t ret = write(fd, buf, to_write);
        if (ret < 0) {
            close(fd);
            return -1;
        }
        written += ret;
    }

    fsync(fd);
    close(fd);
    return 0;
}

static inline int init_ring(struct io_uring *ring, unsigned int entries, unsigned int flags)
{
    int ret = io_uring_queue_init(entries, ring, flags);
    if (ret < 0) {
        fprintf(stderr, "io_uring_queue_init failed: %s\n", strerror(-ret));
        return ret;
    }
    return 0;
}

static inline int submit_and_wait(struct io_uring *ring, struct io_uring_cqe **cqe_ptr)
{
    int ret = io_uring_submit_and_wait(ring, 1);
    if (ret < 0) {
        fprintf(stderr, "io_uring_submit_and_wait failed: %s\n", strerror(-ret));
        return ret;
    }
    return io_uring_wait_cqe(ring, cqe_ptr);
}

static inline int submit_and_wait_timeout(struct io_uring *ring,
                                           struct io_uring_cqe **cqe_ptr,
                                           struct __kernel_timespec *ts)
{
    int ret = io_uring_submit_and_wait_timeout(ring, cqe_ptr, 1, ts, NULL);
    if (ret < 0) {
        fprintf(stderr, "io_uring_submit_and_wait_timeout failed: %s\n", strerror(-ret));
        return ret;
    }
    return 0;
}

static inline void print_summary(const char *suite_name)
{
    printf("\n========================================\n");
    printf("  %s Summary\n", suite_name);
    printf("========================================\n");
    printf("  PASS: %d  FAIL: %d  SKIP: %d\n", g_pass_count, g_fail_count, g_skip_count);
    printf("  Total: %d\n", g_pass_count + g_fail_count + g_skip_count);
    printf("========================================\n\n");
}

#endif
