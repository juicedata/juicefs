/**
 * @file fuse/winfsp_fuse.h
 * WinFsp FUSE compatible API.
 *
 * @copyright 2015-2020 Bill Zissimopoulos
 */
/*
 * This file is part of WinFsp.
 *
 * You can redistribute it and/or modify it under the terms of the GNU
 * General Public License version 3 as published by the Free Software
 * Foundation.
 *
 * Licensees holding a valid commercial license may use this software
 * in accordance with the commercial license agreement provided in
 * conjunction with the software.  The terms and conditions of any such
 * commercial license agreement shall govern, supersede, and render
 * ineffective any application of the GPLv3 license to this software,
 * notwithstanding of any reference thereto in the software or
 * associated repository.
 */

#ifndef FUSE_WINFSP_FUSE_H_INCLUDED
#define FUSE_WINFSP_FUSE_H_INCLUDED

#include <errno.h>
#include <stdint.h>
#if !defined(WINFSP_DLL_INTERNAL)
#include <stdlib.h>
#endif

#ifdef __cplusplus
extern "C" {
#endif

#if !defined(FSP_FUSE_API)
#if defined(WINFSP_DLL_INTERNAL)
#define FSP_FUSE_API                    __declspec(dllexport)
#else
#define FSP_FUSE_API                    __declspec(dllimport)
#endif
#endif

#if !defined(FSP_FUSE_API_NAME)
#define FSP_FUSE_API_NAME(n)            (n)
#endif

#if !defined(FSP_FUSE_API_CALL)
#define FSP_FUSE_API_CALL(n)            (n)
#endif

#if !defined(FSP_FUSE_SYM)
#if !defined(CYGFUSE)
#define FSP_FUSE_SYM(proto, ...)        static inline proto { __VA_ARGS__ }
#else
#define FSP_FUSE_SYM(proto, ...)        proto;
#endif
#endif

#define FSP_FUSE_DEVICE_TYPE            (0x8000 | 'W' | 'F' * 0x100) /* DeviceIoControl -> ioctl */
#define FSP_FUSE_CTLCODE_FROM_IOCTL(cmd)\
    (FSP_FUSE_DEVICE_TYPE << 16) | (((cmd) & 0x0fff) << 2)
#define FSP_FUSE_IOCTL(cmd, isiz, osiz) \
    (                                   \
        (((osiz) != 0) << 31) |         \
        (((isiz) != 0) << 30) |         \
        (((isiz) | (osiz)) << 16) |     \
        (cmd)                           \
    )

/*
 * FUSE uses a number of types (notably: struct stat) that are OS specific.
 * Furthermore there are sometimes multiple definitions of the same type even
 * within the same OS. This is certainly true on Windows, where these types
 * are not even native.
 *
 * For this reason we will define our own fuse_* types which represent the
 * types as the WinFsp DLL expects to see them. We will define these types
 * to be compatible with the equivalent Cygwin types as we want WinFsp-FUSE
 * to be usable from Cygwin.
 */

#define FSP_FUSE_STAT_FIELD_DEFN        \
    fuse_dev_t st_dev;                  \
    fuse_ino_t st_ino;                  \
    fuse_mode_t st_mode;                \
    fuse_nlink_t st_nlink;              \
    fuse_uid_t st_uid;                  \
    fuse_gid_t st_gid;                  \
    fuse_dev_t st_rdev;                 \
    fuse_off_t st_size;                 \
    struct fuse_timespec st_atim;       \
    struct fuse_timespec st_mtim;       \
    struct fuse_timespec st_ctim;       \
    fuse_blksize_t st_blksize;          \
    fuse_blkcnt_t st_blocks;            \
    struct fuse_timespec st_birthtim;
#define FSP_FUSE_STAT_EX_FIELD_DEFN     \
    FSP_FUSE_STAT_FIELD_DEFN            \
    uint32_t st_flags;                  \
    uint32_t st_reserved32[3];          \
    uint64_t st_reserved64[2];

#if defined(_WIN64) || defined(_WIN32)

typedef uint32_t fuse_uid_t;
typedef uint32_t fuse_gid_t;
typedef int32_t fuse_pid_t;

typedef uint32_t fuse_dev_t;
typedef uint64_t fuse_ino_t;
typedef uint32_t fuse_mode_t;
typedef uint16_t fuse_nlink_t;
typedef int64_t fuse_off_t;

#if defined(_WIN64)
typedef uint64_t fuse_fsblkcnt_t;
typedef uint64_t fuse_fsfilcnt_t;
#else
typedef uint32_t fuse_fsblkcnt_t;
typedef uint32_t fuse_fsfilcnt_t;
#endif
typedef int32_t fuse_blksize_t;
typedef int64_t fuse_blkcnt_t;

#if defined(_WIN64)
struct fuse_utimbuf
{
    int64_t actime;
    int64_t modtime;
};
struct fuse_timespec
{
    int64_t tv_sec;
    int64_t tv_nsec;
};
#else
struct fuse_utimbuf
{
    int32_t actime;
    int32_t modtime;
};
struct fuse_timespec
{
    int32_t tv_sec;
    int32_t tv_nsec;
};
#endif

#if !defined(FSP_FUSE_USE_STAT_EX)
struct fuse_stat
{
    FSP_FUSE_STAT_FIELD_DEFN
};
#else
struct fuse_stat
{
    FSP_FUSE_STAT_EX_FIELD_DEFN
};
#endif

#if defined(_WIN64)
struct fuse_statvfs
{
    uint64_t f_bsize;
    uint64_t f_frsize;
    fuse_fsblkcnt_t f_blocks;
    fuse_fsblkcnt_t f_bfree;
    fuse_fsblkcnt_t f_bavail;
    fuse_fsfilcnt_t f_files;
    fuse_fsfilcnt_t f_ffree;
    fuse_fsfilcnt_t f_favail;
    uint64_t f_fsid;
    uint64_t f_flag;
    uint64_t f_namemax;
};
#else
struct fuse_statvfs
{
    uint32_t f_bsize;
    uint32_t f_frsize;
    fuse_fsblkcnt_t f_blocks;
    fuse_fsblkcnt_t f_bfree;
    fuse_fsblkcnt_t f_bavail;
    fuse_fsfilcnt_t f_files;
    fuse_fsfilcnt_t f_ffree;
    fuse_fsfilcnt_t f_favail;
    uint32_t f_fsid;
    uint32_t f_flag;
    uint32_t f_namemax;
};
#endif

struct fuse_flock
{
    int16_t l_type;
    int16_t l_whence;
    fuse_off_t l_start;
    fuse_off_t l_len;
    fuse_pid_t l_pid;
};

#if defined(WINFSP_DLL_INTERNAL)
#define FSP_FUSE_ENV_INIT               \
    {                                   \
        'W',                            \
        MemAlloc, MemFree,              \
        fsp_fuse_daemonize,             \
        fsp_fuse_set_signal_handlers,   \
        0/*conv_to_win_path*/,          \
        0/*winpid_to_pid*/,             \
        { 0 },                          \
    }
#else
#define FSP_FUSE_ENV_INIT               \
    {                                   \
        'W',                            \
        malloc, free,                   \
        fsp_fuse_daemonize,             \
        fsp_fuse_set_signal_handlers,   \
        0/*conv_to_win_path*/,          \
        0/*winpid_to_pid*/,             \
        { 0 },                          \
    }
#endif

#elif defined(__CYGWIN__)

#include <fcntl.h>
#include <pthread.h>
#include <signal.h>
#include <sys/stat.h>
#include <sys/statvfs.h>
#include <sys/types.h>
#include <utime.h>

#define fuse_uid_t                      uid_t
#define fuse_gid_t                      gid_t
#define fuse_pid_t                      pid_t

#define fuse_dev_t                      dev_t
#define fuse_ino_t                      ino_t
#define fuse_mode_t                     mode_t
#define fuse_nlink_t                    nlink_t
#define fuse_off_t                      off_t

#define fuse_fsblkcnt_t                 fsblkcnt_t
#define fuse_fsfilcnt_t                 fsfilcnt_t
#define fuse_blksize_t                  blksize_t
#define fuse_blkcnt_t                   blkcnt_t

#define fuse_utimbuf                    utimbuf
#define fuse_timespec                   timespec

#if !defined(FSP_FUSE_USE_STAT_EX)
#define fuse_stat                       stat
#else
struct fuse_stat
{
    FSP_FUSE_STAT_EX_FIELD_DEFN
};
#endif
#define fuse_statvfs                    statvfs
#define fuse_flock                      flock

#define FSP_FUSE_ENV_INIT               \
    {                                   \
        'C',                            \
        malloc, free,                   \
        fsp_fuse_daemonize,             \
        fsp_fuse_set_signal_handlers,   \
        fsp_fuse_conv_to_win_path,      \
        fsp_fuse_winpid_to_pid,         \
        { 0 },                          \
    }

/*
 * Note that long is 8 bytes long in Cygwin64 and 4 bytes long in Win64.
 * For this reason we avoid using long anywhere in these headers.
 */

#else
#error unsupported environment
#endif

struct fuse_stat_ex
{
    FSP_FUSE_STAT_EX_FIELD_DEFN
};

struct fsp_fuse_env
{
    unsigned environment;
    void *(*memalloc)(size_t);
    void (*memfree)(void *);
    int (*daemonize)(int);
    int (*set_signal_handlers)(void *);
    char *(*conv_to_win_path)(const char *);
    fuse_pid_t (*winpid_to_pid)(uint32_t);
    void (*reserved[2])();
};

FSP_FUSE_API void FSP_FUSE_API_NAME(fsp_fuse_signal_handler)(int sig);

#if defined(_WIN64) || defined(_WIN32)

static inline int fsp_fuse_daemonize(int foreground)
{
    (void)foreground;
    return 0;
}

static inline int fsp_fuse_set_signal_handlers(void *se)
{
    (void)se;
    return 0;
}

#elif defined(__CYGWIN__)

static inline int fsp_fuse_daemonize(int foreground)
{
    int daemon(int nochdir, int noclose);
    int chdir(const char *path);

    if (!foreground)
    {
        if (-1 == daemon(0, 0))
            return -1;
    }
    else
        chdir("/");

    return 0;
}

static inline void *fsp_fuse_signal_thread(void *psigmask)
{
    int sig;

    if (0 == sigwait((sigset_t *)psigmask, &sig))
        FSP_FUSE_API_CALL(fsp_fuse_signal_handler)(sig);

    return 0;
}

static inline int fsp_fuse_set_signal_handlers(void *se)
{
#define FSP_FUSE_SET_SIGNAL_HANDLER(sig, newha)\
    if (-1 != sigaction((sig), 0, &oldsa) &&\
        oldsa.sa_handler == (se ? SIG_DFL : (newha)))\
    {\
        newsa.sa_handler = se ? (newha) : SIG_DFL;\
        sigaction((sig), &newsa, 0);\
    }
#define FSP_FUSE_SIGADDSET(sig)\
    if (-1 != sigaction((sig), 0, &oldsa) &&\
        oldsa.sa_handler == SIG_DFL)\
        sigaddset(&sigmask, (sig));

    static sigset_t sigmask;
    static pthread_t sigthr;
    struct sigaction oldsa, newsa;

    // memset instead of initializer to avoid GCC -Wmissing-field-initializers warning
    memset(&newsa, 0, sizeof newsa);

    if (0 != se)
    {
        if (0 == sigthr)
        {
            FSP_FUSE_SET_SIGNAL_HANDLER(SIGPIPE, SIG_IGN);

            sigemptyset(&sigmask);
            FSP_FUSE_SIGADDSET(SIGHUP);
            FSP_FUSE_SIGADDSET(SIGINT);
            FSP_FUSE_SIGADDSET(SIGTERM);
            if (0 != pthread_sigmask(SIG_BLOCK, &sigmask, 0))
                return -1;

            if (0 != pthread_create(&sigthr, 0, fsp_fuse_signal_thread, &sigmask))
                return -1;
        }
    }
    else
    {
        if (0 != sigthr)
        {
            pthread_cancel(sigthr);
            pthread_join(sigthr, 0);
            sigthr = 0;

            if (0 != pthread_sigmask(SIG_UNBLOCK, &sigmask, 0))
                return -1;
            sigemptyset(&sigmask);

            FSP_FUSE_SET_SIGNAL_HANDLER(SIGPIPE, SIG_IGN);
        }
    }

    return 0;

#undef FSP_FUSE_SIGADDSET
#undef FSP_FUSE_SET_SIGNAL_HANDLER
}

static inline char *fsp_fuse_conv_to_win_path(const char *path)
{
    void *cygwin_create_path(unsigned, const void *);
    return (char *)cygwin_create_path(
        0/*CCP_POSIX_TO_WIN_A*/ | 0x100/*CCP_RELATIVE*/,
        path);
}

static inline fuse_pid_t fsp_fuse_winpid_to_pid(uint32_t winpid)
{
    pid_t cygwin_winpid_to_pid(int winpid);
    pid_t pid = cygwin_winpid_to_pid(winpid);
    return -1 != pid ? pid : (fuse_pid_t)winpid;
}
#endif


static inline struct fsp_fuse_env *fsp_fuse_env(void)
{
    static struct fsp_fuse_env env = FSP_FUSE_ENV_INIT;
    return &env;
}

#ifdef __cplusplus
}
#endif

#endif
