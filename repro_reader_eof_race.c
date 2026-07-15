#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

typedef struct {
    int rc;
    int err;
    off_t size;
    int64_t elapsed_us;
} stat_result_t;

static int64_t now_us(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000LL + ts.tv_nsec / 1000;
}

static ssize_t write_full(int fd, const void *buf, size_t n) {
    const char *p = (const char *)buf;
    size_t left = n;
    while (left > 0) {
        ssize_t w = write(fd, p, left);
        if (w < 0) {
            if (errno == EINTR) {
                continue;
            }
            return -1;
        }
        left -= (size_t)w;
        p += w;
    }
    return (ssize_t)n;
}

static ssize_t read_full(int fd, void *buf, size_t n) {
    char *p = (char *)buf;
    size_t got = 0;
    while (got < n) {
        ssize_t r = read(fd, p + got, n - got);
        if (r < 0) {
            if (errno == EINTR) {
                continue;
            }
            return -1;
        }
        if (r == 0) {
            break;
        }
        got += (size_t)r;
    }
    return (ssize_t)got;
}

static int run_once(const char *path, int iter, bool verbose) {
    int fd = open(path, O_CREAT | O_TRUNC | O_RDWR, 0644);
    if (fd < 0) {
        fprintf(stderr, "iter=%d open for write failed: %s\n", iter, strerror(errno));
        return -1;
    }

    char payload[100];
    for (int i = 0; i < 100; i++) {
        payload[i] = (char)('A' + (i % 26));
    }
    if (write_full(fd, payload, sizeof(payload)) != (ssize_t)sizeof(payload)) {
        fprintf(stderr, "iter=%d write 100 failed: %s\n", iter, strerror(errno));
        close(fd);
        return -1;
    }

    int start_pipe[2];
    int result_pipe[2];
    if (pipe(start_pipe) != 0 || pipe(result_pipe) != 0) {
        fprintf(stderr, "iter=%d pipe failed: %s\n", iter, strerror(errno));
        close(fd);
        if (start_pipe[0] >= 0) {
            close(start_pipe[0]);
            close(start_pipe[1]);
        }
        if (result_pipe[0] >= 0) {
            close(result_pipe[0]);
            close(result_pipe[1]);
        }
        return -1;
    }

    pid_t pid = fork();
    if (pid < 0) {
        fprintf(stderr, "iter=%d fork failed: %s\n", iter, strerror(errno));
        close(fd);
        close(start_pipe[0]);
        close(start_pipe[1]);
        close(result_pipe[0]);
        close(result_pipe[1]);
        return -1;
    }

    if (pid == 0) {
        close(start_pipe[1]);
        close(result_pipe[0]);

        char token = 0;
        if (read_full(start_pipe[0], &token, 1) != 1) {
            _exit(2);
        }

        stat_result_t res;
        memset(&res, 0, sizeof(res));

        struct stat st;
        int64_t t0 = now_us();
        int rc = stat(path, &st);
        int64_t t1 = now_us();

        res.rc = rc;
        res.err = (rc == 0) ? 0 : errno;
        res.size = (rc == 0) ? st.st_size : -1;
        res.elapsed_us = t1 - t0;

        if (write_full(result_pipe[1], &res, sizeof(res)) != (ssize_t)sizeof(res)) {
            _exit(3);
        }
        _exit(0);
    }

    close(start_pipe[0]);
    close(result_pipe[1]);

    int64_t t_close_begin = now_us();

    char token = 'S';
    if (write_full(start_pipe[1], &token, 1) != 1) {
        fprintf(stderr, "iter=%d signal p2 failed: %s\n", iter, strerror(errno));
        close(fd);
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    if (close(fd) != 0) {
        fprintf(stderr, "iter=%d close(write fd) failed: %s\n", iter, strerror(errno));
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    int64_t t_close_end = now_us();

    int fd2 = open(path, O_RDONLY);
    if (fd2 < 0) {
        fprintf(stderr, "iter=%d reopen for read failed: %s\n", iter, strerror(errno));
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    char first50[50];
    ssize_t r1 = read_full(fd2, first50, sizeof(first50));
    if (r1 < 0) {
        fprintf(stderr, "iter=%d read first 50 failed: %s\n", iter, strerror(errno));
        close(fd2);
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    stat_result_t sr;
    memset(&sr, 0, sizeof(sr));
    if (read_full(result_pipe[0], &sr, sizeof(sr)) != (ssize_t)sizeof(sr)) {
        fprintf(stderr, "iter=%d read p2 result failed\n", iter);
        close(fd2);
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    char second50[50];
    ssize_t r2 = read_full(fd2, second50, sizeof(second50));
    if (r2 < 0) {
        fprintf(stderr, "iter=%d read second 50 failed: %s\n", iter, strerror(errno));
        close(fd2);
        close(start_pipe[1]);
        close(result_pipe[0]);
        waitpid(pid, NULL, 0);
        return -1;
    }

    close(fd2);
    close(start_pipe[1]);
    close(result_pipe[0]);

    int st = 0;
    waitpid(pid, &st, 0);

    struct stat verify;
    int vrc = stat(path, &verify);

    bool hit = (r1 == 50 && r2 < 50);

    if (verbose || hit) {
        printf("iter=%d close_us=%" PRId64 " p2_stat={rc=%d,err=%d,size=%lld,us=%" PRId64 "} read={%zd,%zd} verify={rc=%d,size=%lld}%s\n",
               iter,
               t_close_end - t_close_begin,
               sr.rc, sr.err, (long long)sr.size, sr.elapsed_us,
               r1, r2,
               vrc, (long long)(vrc == 0 ? verify.st_size : -1),
               hit ? "  <-- HIT(second read short/EOF)" : "");
    }

    return hit ? 1 : 0;
}

int main(int argc, char **argv) {
    if (argc < 2 || argc > 4) {
        fprintf(stderr, "Usage: %s <file-path> [iterations=1000] [verbose=0|1]\n", argv[0]);
        return 2;
    }

    const char *path = argv[1];
    int iterations = 1000;
    int verbose = 0;

    if (argc >= 3) {
        iterations = atoi(argv[2]);
        if (iterations <= 0) {
            iterations = 1000;
        }
    }
    if (argc >= 4) {
        verbose = atoi(argv[3]) ? 1 : 0;
    }

    int hits = 0;
    int errors = 0;

    printf("Start reproducer: path=%s iterations=%d\n", path, iterations);
    printf("Expected useful env on JuiceFS daemon: JUICEFS_REPLY_ATTR_RACE_SLEEP (e.g. 2s)\n");

    for (int i = 1; i <= iterations; i++) {
        int rc = run_once(path, i, verbose != 0);
        if (rc < 0) {
            errors++;
        } else if (rc > 0) {
            hits++;
        }
    }

    printf("Done: hits=%d errors=%d total=%d\n", hits, errors, iterations);
    return (hits > 0) ? 1 : 0;
}
