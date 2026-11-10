#ifndef PREADV_TEST_COMMON_H
#define PREADV_TEST_COMMON_H

#define _GNU_SOURCE

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/uio.h>
#include <sys/time.h>
#include <sys/resource.h>

#define BLOCK_SIZE 4096
#define TEST_FILE_SIZE (256 * 1024)

static int g_pass = 0;
static int g_fail = 0;
static int g_skip = 0;

#define PASS(name) do { printf("  [PASS] %s\n", name); g_pass++; } while(0)
#define FAIL(name, ...) do { printf("  [FAIL] %s: ", name); printf(__VA_ARGS__); printf("\n"); g_fail++; } while(0)
#define SKIP(name, ...) do { printf("  [SKIP] %s: ", name); printf(__VA_ARGS__); printf("\n"); g_skip++; } while(0)

static inline int create_test_file(const char *path, size_t size)
{
    int fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0644);
    if (fd < 0) return -1;

    char buf[BLOCK_SIZE];
    for (size_t i = 0; i < BLOCK_SIZE; i++)
        buf[i] = 'A' + (i % 26);

    size_t written = 0;
    while (written < size) {
        size_t to_write = size - written;
        if (to_write > BLOCK_SIZE) to_write = BLOCK_SIZE;
        ssize_t ret = write(fd, buf, to_write);
        if (ret < 0) { close(fd); return -1; }
        written += ret;
    }

    fsync(fd);
    close(fd);
    return 0;
}

static inline double get_time_sec(void)
{
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return tv.tv_sec + tv.tv_usec / 1000000.0;
}

static inline void print_summary(const char *name)
{
    printf("\n========================================\n");
    printf("  %s Summary\n", name);
    printf("========================================\n");
    printf("  PASS: %d  FAIL: %d  SKIP: %d\n", g_pass, g_fail, g_skip);
    printf("  Total: %d\n", g_pass + g_fail + g_skip);
    printf("========================================\n\n");
}

#endif
