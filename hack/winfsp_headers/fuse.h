/**
 * @file fuse/fuse.h
 * WinFsp FUSE compatible API.
 *
 * This file is derived from libfuse/include/fuse.h:
 *     FUSE: Filesystem in Userspace
 *     Copyright (C) 2001-2007  Miklos Szeredi <miklos@szeredi.hu>
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

#ifndef FUSE_H_
#define FUSE_H_

#include "fuse_common.h"

#ifdef __cplusplus
extern "C" {
#endif

struct fuse;

typedef int (*fuse_fill_dir_t)(void *buf, const char *name,
    const struct fuse_stat *stbuf, fuse_off_t off);
typedef struct fuse_dirhandle *fuse_dirh_t;
typedef int (*fuse_dirfil_t)(fuse_dirh_t h, const char *name,
    int type, fuse_ino_t ino);

struct fuse_operations
{
    /* S - supported by WinFsp */
    /* S */ int (*getattr)(const char *path, struct fuse_stat *stbuf);
    /* S */ int (*getdir)(const char *path, fuse_dirh_t h, fuse_dirfil_t filler);
    /* S */ int (*readlink)(const char *path, char *buf, size_t size);
    /* S */ int (*mknod)(const char *path, fuse_mode_t mode, fuse_dev_t dev);
    /* S */ int (*mkdir)(const char *path, fuse_mode_t mode);
    /* S */ int (*unlink)(const char *path);
    /* S */ int (*rmdir)(const char *path);
    /* S */ int (*symlink)(const char *dstpath, const char *srcpath);
    /* S */ int (*rename)(const char *oldpath, const char *newpath);
    /* _ */ int (*link)(const char *srcpath, const char *dstpath);
    /* S */ int (*chmod)(const char *path, fuse_mode_t mode);
    /* S */ int (*chown)(const char *path, fuse_uid_t uid, fuse_gid_t gid);
    /* S */ int (*truncate)(const char *path, fuse_off_t size);
    /* S */ int (*utime)(const char *path, struct fuse_utimbuf *timbuf);
    /* S */ int (*open)(const char *path, struct fuse_file_info *fi);
    /* S */ int (*read)(const char *path, char *buf, size_t size, fuse_off_t off,
        struct fuse_file_info *fi);
    /* S */ int (*write)(const char *path, const char *buf, size_t size, fuse_off_t off,
        struct fuse_file_info *fi);
    /* S */ int (*statfs)(const char *path, struct fuse_statvfs *stbuf);
    /* S */ int (*flush)(const char *path, struct fuse_file_info *fi);
    /* S */ int (*release)(const char *path, struct fuse_file_info *fi);
    /* S */ int (*fsync)(const char *path, int datasync, struct fuse_file_info *fi);
    /* S */ int (*setxattr)(const char *path, const char *name, const char *value, size_t size,
        int flags);
    /* S */ int (*getxattr)(const char *path, const char *name, char *value, size_t size);
    /* S */ int (*listxattr)(const char *path, char *namebuf, size_t size);
    /* S */ int (*removexattr)(const char *path, const char *name);
    /* S */ int (*opendir)(const char *path, struct fuse_file_info *fi);
    /* S */ int (*readdir)(const char *path, void *buf, fuse_fill_dir_t filler, fuse_off_t off,
        struct fuse_file_info *fi);
    /* S */ int (*releasedir)(const char *path, struct fuse_file_info *fi);
    /* S */ int (*fsyncdir)(const char *path, int datasync, struct fuse_file_info *fi);
    /* S */ void *(*init)(struct fuse_conn_info *conn);
    /* S */ void (*destroy)(void *data);
    /* _ */ int (*access)(const char *path, int mask);
    /* S */ int (*create)(const char *path, fuse_mode_t mode, struct fuse_file_info *fi);
    /* S */ int (*ftruncate)(const char *path, fuse_off_t off, struct fuse_file_info *fi);
    /* S */ int (*fgetattr)(const char *path, struct fuse_stat *stbuf, struct fuse_file_info *fi);
    /* _ */ int (*lock)(const char *path,
        struct fuse_file_info *fi, int cmd, struct fuse_flock *lock);
    /* S */ int (*utimens)(const char *path, const struct fuse_timespec tv[2]);
    /* _ */ int (*bmap)(const char *path, size_t blocksize, uint64_t *idx);
    /* _ */ unsigned int flag_nullpath_ok:1;
    /* _ */ unsigned int flag_nopath:1;
    /* _ */ unsigned int flag_utime_omit_ok:1;
    /* _ */ unsigned int flag_reserved:29;
    /* S */ int (*ioctl)(const char *path, int cmd, void *arg, struct fuse_file_info *fi,
        unsigned int flags, void *data);
    /* _ */ int (*poll)(const char *path, struct fuse_file_info *fi,
        struct fuse_pollhandle *ph, unsigned *reventsp);
    /* FUSE 2.9 */
    /* _ */ int (*write_buf)(const char *path,
        struct fuse_bufvec *buf, fuse_off_t off, struct fuse_file_info *fi);
    /* _ */ int (*read_buf)(const char *path,
        struct fuse_bufvec **bufp, size_t size, fuse_off_t off, struct fuse_file_info *fi);
    /* _ */ int (*flock)(const char *path, struct fuse_file_info *, int op);
    /* _ */ int (*fallocate)(const char *path, int mode, fuse_off_t off, fuse_off_t len,
        struct fuse_file_info *fi);
    /* OSXFUSE */
    /* _ */ int (*reserved00)();
    /* _ */ int (*reserved01)();
    /* _ */ int (*reserved02)();
    /* _ */ int (*statfs_x)(const char *path, struct fuse_statfs *stbuf);
    /* _ */ int (*setvolname)(const char *volname);
    /* _ */ int (*exchange)(const char *oldpath, const char *newpath, unsigned long flags);
    /* _ */ int (*getxtimes)(const char *path,
        struct fuse_timespec *bkuptime, struct fuse_timespec *crtime);
    /* _ */ int (*setbkuptime)(const char *path, const struct fuse_timespec *tv);
    /* S */ int (*setchgtime)(const char *path, const struct fuse_timespec *tv);
    /* S */ int (*setcrtime)(const char *path, const struct fuse_timespec *tv);
    /* S */ int (*chflags)(const char *path, uint32_t flags);
    /* _ */ int (*setattr_x)(const char *path, struct fuse_setattr_x *attr);
    /* _ */ int (*fsetattr_x)(const char *path, struct fuse_setattr_x *attr,
        struct fuse_file_info *fi);
};

struct fuse_context
{
    struct fuse *fuse;
    fuse_uid_t uid;
    fuse_gid_t gid;
    fuse_pid_t pid;
    void *private_data;
    fuse_mode_t umask;
};

#define fuse_main(argc, argv, ops, data)\
    fuse_main_real(argc, argv, ops, sizeof *(ops), data)

FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_main_real)(struct fsp_fuse_env *env,
    int argc, char *argv[],
    const struct fuse_operations *ops, size_t opsize, void *data);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_is_lib_option)(struct fsp_fuse_env *env,
    const char *opt);
FSP_FUSE_API struct fuse *FSP_FUSE_API_NAME(fsp_fuse_new)(struct fsp_fuse_env *env,
    struct fuse_chan *ch, struct fuse_args *args,
    const struct fuse_operations *ops, size_t opsize, void *data);
FSP_FUSE_API void FSP_FUSE_API_NAME(fsp_fuse_destroy)(struct fsp_fuse_env *env,
    struct fuse *f);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_loop)(struct fsp_fuse_env *env,
    struct fuse *f);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_loop_mt)(struct fsp_fuse_env *env,
    struct fuse *f);
FSP_FUSE_API void FSP_FUSE_API_NAME(fsp_fuse_exit)(struct fsp_fuse_env *env,
    struct fuse *f);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_exited)(struct fsp_fuse_env *env,
    struct fuse *f);
FSP_FUSE_API struct fuse_context *FSP_FUSE_API_NAME(fsp_fuse_get_context)(struct fsp_fuse_env *env);

FSP_FUSE_SYM(
int fuse_main_real(int argc, char *argv[],
    const struct fuse_operations *ops, size_t opsize, void *data),
{
    return FSP_FUSE_API_CALL(fsp_fuse_main_real)
        (fsp_fuse_env(), argc, argv, ops, opsize, data);
})

FSP_FUSE_SYM(
int fuse_is_lib_option(const char *opt),
{
    return FSP_FUSE_API_CALL(fsp_fuse_is_lib_option)
        (fsp_fuse_env(), opt);
})

FSP_FUSE_SYM(
struct fuse *fuse_new(struct fuse_chan *ch, struct fuse_args *args,
    const struct fuse_operations *ops, size_t opsize, void *data),
{
    return FSP_FUSE_API_CALL(fsp_fuse_new)
        (fsp_fuse_env(), ch, args, ops, opsize, data);
})

FSP_FUSE_SYM(
void fuse_destroy(struct fuse *f),
{
    FSP_FUSE_API_CALL(fsp_fuse_destroy)
        (fsp_fuse_env(), f);
})

FSP_FUSE_SYM(
int fuse_loop(struct fuse *f),
{
    return FSP_FUSE_API_CALL(fsp_fuse_loop)
        (fsp_fuse_env(), f);
})

FSP_FUSE_SYM(
int fuse_loop_mt(struct fuse *f),
{
    return FSP_FUSE_API_CALL(fsp_fuse_loop_mt)
        (fsp_fuse_env(), f);
})

FSP_FUSE_SYM(
void fuse_exit(struct fuse *f),
{
    FSP_FUSE_API_CALL(fsp_fuse_exit)
        (fsp_fuse_env(), f);
})

FSP_FUSE_SYM(
int fuse_exited(struct fuse *f),
{
    return FSP_FUSE_API_CALL(fsp_fuse_exited)
        (fsp_fuse_env(), f);
})

FSP_FUSE_SYM(
struct fuse_context *fuse_get_context(void),
{
    return FSP_FUSE_API_CALL(fsp_fuse_get_context)
        (fsp_fuse_env());
})

FSP_FUSE_SYM(
int fuse_getgroups(int size, fuse_gid_t list[]),
{
    (void)size;
    (void)list;
    return -ENOSYS;
})

FSP_FUSE_SYM(
int fuse_interrupted(void),
{
    return 0;
})

FSP_FUSE_SYM(
int fuse_invalidate(struct fuse *f, const char *path),
{
    (void)f;
    (void)path;
    return -EINVAL;
})

FSP_FUSE_SYM(
int fuse_notify_poll(struct fuse_pollhandle *ph),
{
    (void)ph;
    return 0;
})

FSP_FUSE_SYM(
struct fuse_session *fuse_get_session(struct fuse *f),
{
    return (struct fuse_session *)f;
})

#ifdef __cplusplus
}
#endif

#endif
