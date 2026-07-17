/*
 * Reproducer for stale size=0 in replyAttr TOCTOU race (#7238).
 *
 * Sequence:
 *   1) p1 creates d1/f2, writes 100 bytes.
 *   2) p2 issues stat() while meta length is still 0.
 *   3) p1 closes shortly after p2 starts (flush commits length=100).
 *   4) replyAttr is intentionally slowed by JUICEFS_REPLY_ATTR_RACE_SLEEP.
 *
 * Buggy behavior (before 292f44cde0794b5fae3c128afc0ce70457106bae):
 *   p2 stat returns size=0, and a following stat still gets stale size=0.
 *
 * Fixed behavior:
 *   stat should report size=100 (or at least not keep stale 0).
 */
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>
#include <fcntl.h>

typedef struct {
    int rc;
    int err;
    off_t size;
    int64_t elapsed_us;
} stat_result_t;

typedef struct {
    const char *path;
    pthread_mutex_t mu;
    pthread_cond_t start_cv;
    pthread_cond_t done_cv;
    bool start;
    bool done;
    stat_result_t res;
} stat_worker_t;

static int64_t now_us(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000LL + ts.tv_nsec / 1000;
}

static void sleep_us(int64_t us) {
    if (us <= 0) return;
    struct timespec ts;
    ts.tv_sec = us / 1000000LL;
    ts.tv_nsec = (us % 1000000LL) * 1000LL;
    while (nanosleep(&ts, &ts) != 0 && errno == EINTR) {
    }
}

static int64_t env_int(const char *name, int64_t def) {
    const char *v = getenv(name);
    if (!v || !*v) return def;
    long long n = strtoll(v, NULL, 10);
    return n > 0 ? (int64_t)n : def;
}

static ssize_t write_all(int fd, const void *buf, size_t n) {
    const char *p = (const char *)buf;
    size_t left = n;
    while (left > 0) {
        ssize_t w = write(fd, p, left);
        if (w < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        left -= (size_t)w;
        p += w;
    }
    return (ssize_t)n;
}

static void *stat_worker_main(void *arg) {
    stat_worker_t *w = (stat_worker_t *)arg;

    pthread_mutex_lock(&w->mu);
    while (!w->start) {
        pthread_cond_wait(&w->start_cv, &w->mu);
    }
    pthread_mutex_unlock(&w->mu);

    struct stat st;
    int64_t t0 = now_us();
    int rc = stat(w->path, &st);
    int64_t t1 = now_us();

    w->res.rc = rc;
    w->res.err = rc ? errno : 0;
    w->res.size = rc ? -1 : st.st_size;
    w->res.elapsed_us = t1 - t0;

    pthread_mutex_lock(&w->mu);
    w->done = true;
    pthread_cond_signal(&w->done_cv);
    pthread_mutex_unlock(&w->mu);
    return NULL;
}

static int run_once(const char *path, int iter, bool verbose, int64_t preclose_ms) {
    int fd = open(path, O_CREAT | O_TRUNC | O_RDWR, 0644);
    if (fd < 0) {
        fprintf(stderr, "iter=%d open-write: %s\n", iter, strerror(errno));
        return -1;
    }

    char payload[100];
    memset(payload, 'x', sizeof(payload));
    if (write_all(fd, payload, sizeof(payload)) != (ssize_t)sizeof(payload)) {
        fprintf(stderr, "iter=%d write: %s\n", iter, strerror(errno));
        close(fd);
        return -1;
    }

    stat_worker_t worker;
    memset(&worker, 0, sizeof(worker));
    worker.path = path;
    pthread_mutex_init(&worker.mu, NULL);
    pthread_cond_init(&worker.start_cv, NULL);
    pthread_cond_init(&worker.done_cv, NULL);

    pthread_t tid;
    if (pthread_create(&tid, NULL, stat_worker_main, &worker) != 0) {
        fprintf(stderr, "iter=%d pthread_create: %s\n", iter, strerror(errno));
        close(fd);
        return -1;
    }

    pthread_mutex_lock(&worker.mu);
    worker.start = true;
    pthread_cond_signal(&worker.start_cv);
    pthread_mutex_unlock(&worker.mu);

    sleep_us(preclose_ms * 1000LL);

    int64_t t_close_begin = now_us();
    if (close(fd) != 0) {
        fprintf(stderr, "iter=%d close: %s\n", iter, strerror(errno));
        pthread_join(tid, NULL);
        return -1;
    }
    int64_t t_close_end = now_us();

    pthread_mutex_lock(&worker.mu);
    while (!worker.done) {
        pthread_cond_wait(&worker.done_cv, &worker.mu);
    }
    stat_result_t sr = worker.res;
    pthread_mutex_unlock(&worker.mu);

    struct stat st_after;
    int rc_after = stat(path, &st_after);
    off_t size_after = rc_after == 0 ? st_after.st_size : -1;

    pthread_join(tid, NULL);
    pthread_cond_destroy(&worker.done_cv);
    pthread_cond_destroy(&worker.start_cv);
    pthread_mutex_destroy(&worker.mu);

    bool hit = (sr.rc == 0 && sr.size == 0 && rc_after == 0 && size_after == 0);

    if (verbose || hit) {
        printf("iter=%d close_us=%" PRId64 " p2_stat={rc=%d,size=%lld,us=%" PRId64 "} post_stat={rc=%d,size=%lld}%s\n",
               iter,
               t_close_end - t_close_begin,
               sr.rc,
               (long long)sr.size,
               sr.elapsed_us,
               rc_after,
               (long long)size_after,
               hit ? "  <-- HIT(stale-size-0)" : "");
    }

    return hit ? 1 : 0;
}

int main(int argc, char **argv) {
    const char *path = "d1/f2";
    bool verbose = (argc >= 2 && atoi(argv[1]) != 0);
    int64_t preclose_ms = env_int("REPRO_PRECLOSE_MS", 400);
    int64_t iters = env_int("REPRO_ITERS", 8);

    printf("path=%s preclose_ms=%lld iters=%lld\n",
           path, (long long)preclose_ms, (long long)iters);

    int hits = 0;
    int errors = 0;
    for (int i = 1; i <= (int)iters; i++) {
        int rc = run_once(path, i, verbose, preclose_ms);
        if (rc < 0) {
            errors++;
        } else if (rc > 0) {
            hits++;
        }
    }

    printf("Done: hits=%d errors=%d total=%lld\n", hits, errors, (long long)iters);
    return hits > 0 ? 1 : 0;
}
