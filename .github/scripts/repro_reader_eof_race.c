/*
 * Reproducer for JuiceFS concurrent read-EOF race.
 *
 * Sequence:
 *   p1 writes 100 bytes, signals p2, waits PRECLOSE_MS, then close().
 *   p2 stat() enters replyAttr with stale length=0 from meta.
 *   p1 re-opens and reads first 50 bytes (creates fileReader length=100).
 *   replyAttr debug_sleep ends: UpdateLength -> reader.Truncate(inode, 0).
 *   p1 reads second 50 bytes: offset=50 >= f.length=0 -> EOF.
 *   replyAttr second debug_sleep ends: ModifiedSince refresh -> Truncate(100).
 *
 * Requirements:
 *   - JuiceFS mounted with JUICEFS_REPLY_ATTR_RACE_SLEEP=Xs
 *   - Run from inside the JuiceFS mountpoint directory
 *   - File must be at d1/f1 (inode 5, matching debug gate)
 *   - REPRO_SLEEP_MS env: must match Xs above (default 2000)
 *   - REPRO_PRECLOSE_MS env: delay before close to let p2 read meta=0 (default 500)
 */
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
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
#include <time.h>
#include <unistd.h>
#include <pthread.h>

/* O_DIRECT alignment: reads must be sector-aligned */
#define ALIGN 512
#define FILE_SIZE 100
/* we read in two halves; use 512-byte aligned buffers */
#define HALF  512  /* actual file has only 100 bytes, so first read gets 100 */

typedef struct {
    int rc;
    int err;
    off_t size;
    int64_t elapsed_us;
    int64_t start_us;  /* when the stat() call was issued */
} stat_result_t;

static int64_t now_us(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000LL + ts.tv_nsec / 1000;
}

static void sleep_us(int64_t us) {
    if (us <= 0) return;
    struct timespec ts;
    ts.tv_sec  = us / 1000000LL;
    ts.tv_nsec = (us % 1000000LL) * 1000LL;
    while (nanosleep(&ts, &ts) != 0 && errno == EINTR) {}
}

typedef struct {
    const char *path;
    pthread_mutex_t mu;
    pthread_cond_t  start_cv;
    pthread_cond_t  done_cv;
    bool start;
    bool done;
    stat_result_t res;
} stat_worker_t;

static void *stat_worker_main(void *arg) {
    stat_worker_t *w = (stat_worker_t *)arg;

    pthread_mutex_lock(&w->mu);
    while (!w->start) pthread_cond_wait(&w->start_cv, &w->mu);
    pthread_mutex_unlock(&w->mu);

    struct stat st;
    int64_t t0 = now_us();
    int rc = stat(w->path, &st);
    int64_t t1 = now_us();

    w->res.rc         = rc;
    w->res.err        = rc ? errno : 0;
    w->res.size       = rc ? -1 : st.st_size;
    w->res.elapsed_us = t1 - t0;
    w->res.start_us   = t0;

    pthread_mutex_lock(&w->mu);
    w->done = true;
    pthread_cond_signal(&w->done_cv);
    pthread_mutex_unlock(&w->mu);
    return NULL;
}

static ssize_t write_all(int fd, const void *buf, size_t n) {
    const char *p = (const char *)buf;
    size_t left = n;
    while (left > 0) {
        ssize_t w = write(fd, p, left);
        if (w < 0) { if (errno == EINTR) continue; return -1; }
        left -= (size_t)w; p += w;
    }
    return (ssize_t)n;
}

static ssize_t read_all(int fd, void *buf, size_t n) {
    char *p = (char *)buf;
    size_t got = 0;
    while (got < n) {
        ssize_t r = read(fd, p + got, n - got);
        if (r < 0) { if (errno == EINTR) continue; return -1; }
        if (r == 0) break;
        got += (size_t)r;
    }
    return (ssize_t)got;
}

/* Allocate an aligned buffer for O_DIRECT */
static void *alloc_aligned(size_t sz) {
    void *p = NULL;
    if (posix_memalign(&p, ALIGN, sz) != 0) return NULL;
    return p;
}

static int run_once(const char *path, int iter, bool verbose, int64_t sleep_ms, int64_t preclose_ms) {
    /* --- p1: create / truncate / write 100 bytes --- */
    int fd = open(path, O_CREAT | O_TRUNC | O_RDWR, 0644);
    if (fd < 0) { fprintf(stderr, "iter=%d open-write: %s\n", iter, strerror(errno)); return -1; }

    char payload[FILE_SIZE];
    for (int i = 0; i < FILE_SIZE; i++) payload[i] = (char)('A' + (i % 26));
    if (write_all(fd, payload, FILE_SIZE) != FILE_SIZE) {
        fprintf(stderr, "iter=%d write: %s\n", iter, strerror(errno)); close(fd); return -1;
    }

    /* --- setup stat worker thread (p2) --- */
    stat_worker_t worker;
    memset(&worker, 0, sizeof(worker));
    worker.path = path;
    pthread_mutex_init(&worker.mu, NULL);
    pthread_cond_init(&worker.start_cv, NULL);
    pthread_cond_init(&worker.done_cv, NULL);

    pthread_t tid;
    if (pthread_create(&tid, NULL, stat_worker_main, &worker) != 0) {
        fprintf(stderr, "iter=%d pthread_create: %s\n", iter, strerror(errno));
        close(fd); return -1;
    }

    /* --- signal p2 to start stat, record signal time --- */
    pthread_mutex_lock(&worker.mu);
    worker.start = true;
    pthread_cond_signal(&worker.start_cv);
    pthread_mutex_unlock(&worker.mu);

    int64_t t_signal = now_us();

    /*
     * PRECLOSE delay: give p2's getattr time to reach meta.GetAttr and read
     * length=0 BEFORE we flush/commit. Without this, close() would commit
     * first and p2 would see length=100, never entering the stale-zero path.
     *
     * The debug gate in replyAttr fires only when entry.Attr.Length==0,
     * which means p2 read 0 from meta. This delay ensures that happens.
     */
    sleep_us(preclose_ms * 1000LL);

    int64_t t_close_begin = now_us();
    if (close(fd) != 0) {
        fprintf(stderr, "iter=%d close: %s\n", iter, strerror(errno));
        pthread_join(tid, NULL); return -1;
    }
    int64_t t_close_end = now_us();

    /*
     * p1 reopens for reading with O_DIRECT to bypass kernel page cache.
     * Without O_DIRECT the kernel would prefetch all 100 bytes in one
     * FUSE read request on the first read(50), so the second read(50)
     * would be served from cache and never see the truncated reader.
     */
    int fd2 = open(path, O_RDONLY | O_DIRECT);
    if (fd2 < 0) {
        /* Fallback: kernel may not support O_DIRECT on this fs; print warning */
        fprintf(stderr, "iter=%d open O_DIRECT failed (%s), falling back\n", iter, strerror(errno));
        fd2 = open(path, O_RDONLY);
    }
    if (fd2 < 0) {
        fprintf(stderr, "iter=%d reopen: %s\n", iter, strerror(errno));
        pthread_join(tid, NULL); return -1;
    }

    /* Read first 512 bytes (only 100 are available; read_all will get 100) */
    void *buf1 = alloc_aligned(HALF);
    void *buf2 = alloc_aligned(HALF);
    if (!buf1 || !buf2) { fprintf(stderr, "iter=%d alloc\n", iter); close(fd2); pthread_join(tid, NULL); return -1; }

    ssize_t r1 = read_all(fd2, buf1, HALF);
    if (r1 < 0) {
        fprintf(stderr, "iter=%d read1: %s\n", iter, strerror(errno));
        free(buf1); free(buf2); close(fd2); pthread_join(tid, NULL); return -1;
    }
    /* r1 should be FILE_SIZE (100) since the file has 100 bytes */

    /*
     * Timing: relative to t_signal (moment p2's stat started):
     *
     *   t_signal + preclose_ms          : p1 sends close()
     *   t_signal + preclose_ms + close_us: p1 close returns
     *   (p2 entered replyAttr, debug_sleep #1 started ~t_signal)
     *   t_signal + sleep_ms             : debug_sleep #1 ends,
     *                                     UpdateLength -> Truncate(inode, 0)  <- WINDOW OPENS
     *   t_signal + 2*sleep_ms           : debug_sleep #2 ends,
     *                                     ModifiedSince refresh -> Truncate(inode, 100) <- WINDOW CLOSES
     *
     * We want to issue the second read INSIDE (sleep_ms, 2*sleep_ms).
     * Aim for the midpoint: t_signal + 1.5*sleep_ms.
     * But only wait if we haven't already passed that point.
     */
    int64_t target_us = t_signal + (int64_t)(sleep_ms * 1500LL);
    int64_t now = now_us();
    if (target_us > now) {
        sleep_us(target_us - now);
    }

    ssize_t r2 = read_all(fd2, buf2, HALF);
    if (r2 < 0) {
        fprintf(stderr, "iter=%d read2: %s\n", iter, strerror(errno));
        free(buf1); free(buf2); close(fd2); pthread_join(tid, NULL); return -1;
    }

    /* Wait for p2 */
    pthread_mutex_lock(&worker.mu);
    while (!worker.done) pthread_cond_wait(&worker.done_cv, &worker.mu);
    stat_result_t sr = worker.res;
    pthread_mutex_unlock(&worker.mu);

    free(buf1); free(buf2);
    close(fd2);
    pthread_join(tid, NULL);
    pthread_cond_destroy(&worker.done_cv);
    pthread_cond_destroy(&worker.start_cv);
    pthread_mutex_destroy(&worker.mu);

    struct stat verify;
    int vrc = stat(path, &verify);

    /* HIT: first read returned bytes, second returned EOF/short */
    bool hit = (r1 > 0 && r2 == 0);

    if (verbose || hit) {
        printf("iter=%d close_us=%" PRId64 " p2_stat={rc=%d,size=%lld,us=%" PRId64 "} "
               "read={%zd,%zd} verify={rc=%d,size=%lld}%s\n",
               iter,
               t_close_end - t_close_begin,
               sr.rc, (long long)sr.size, sr.elapsed_us,
               r1, r2,
               vrc, (long long)(vrc == 0 ? verify.st_size : -1),
               hit ? "  <-- HIT(EOF)" : "");
    }

    return hit ? 1 : 0;
}

static int64_t env_int(const char *name, int64_t def) {
    const char *v = getenv(name);
    if (!v || !*v) return def;
    long long n = strtoll(v, NULL, 10);
    return n > 0 ? (int64_t)n : def;
}

int main(int argc, char **argv) {
    const char *path   = "d1/f1";
    int verbose = (argc >= 2 && atoi(argv[1]));
    int64_t sleep_ms    = env_int("REPRO_SLEEP_MS",    2000);
    int64_t preclose_ms = env_int("REPRO_PRECLOSE_MS", 500);
    int64_t iters       = env_int("REPRO_ITERS",       1);

    printf("path=%s sleep_ms=%lld preclose_ms=%lld iters=%lld\n",
           path, (long long)sleep_ms, (long long)preclose_ms, (long long)iters);

    int hits = 0, errors = 0;
    for (int i = 1; i <= (int)iters; i++) {
        int rc = run_once(path, i, verbose, sleep_ms, preclose_ms);
        if (rc < 0) errors++;
        else if (rc > 0) hits++;
    }
    printf("Done: hits=%d errors=%d total=%lld\n", hits, errors, (long long)iters);
    return hits > 0 ? 1 : 0;
}
