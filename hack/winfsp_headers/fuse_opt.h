/**
 * @file fuse/fuse_opt.h
 * WinFsp FUSE compatible API.
 *
 * This file is derived from libfuse/include/fuse_opt.h:
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

#ifndef FUSE_OPT_H_
#define FUSE_OPT_H_

#include "winfsp_fuse.h"

#ifdef __cplusplus
extern "C" {
#endif

#define FUSE_OPT_KEY(templ, key)        { templ, -1, key }
#define FUSE_OPT_END                    { NULL, 0, 0 }

#define FUSE_OPT_KEY_OPT                -1
#define FUSE_OPT_KEY_NONOPT             -2
#define FUSE_OPT_KEY_KEEP               -3
#define FUSE_OPT_KEY_DISCARD            -4

#define FUSE_ARGS_INIT(argc, argv)      { argc, argv, 0 }

struct fuse_opt
{
    const char *templ;
    unsigned int offset;
    int value;
};

struct fuse_args
{
    int argc;
    char **argv;
    int allocated;
};

typedef int (*fuse_opt_proc_t)(void *data, const char *arg, int key,
    struct fuse_args *outargs);

FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_parse)(struct fsp_fuse_env *env,
    struct fuse_args *args, void *data,
    const struct fuse_opt opts[], fuse_opt_proc_t proc);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_add_arg)(struct fsp_fuse_env *env,
    struct fuse_args *args, const char *arg);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_insert_arg)(struct fsp_fuse_env *env,
    struct fuse_args *args, int pos, const char *arg);
FSP_FUSE_API void FSP_FUSE_API_NAME(fsp_fuse_opt_free_args)(struct fsp_fuse_env *env,
    struct fuse_args *args);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_add_opt)(struct fsp_fuse_env *env,
    char **opts, const char *opt);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_add_opt_escaped)(struct fsp_fuse_env *env,
    char **opts, const char *opt);
FSP_FUSE_API int FSP_FUSE_API_NAME(fsp_fuse_opt_match)(struct fsp_fuse_env *env,
    const struct fuse_opt opts[], const char *opt);

FSP_FUSE_SYM(
int fuse_opt_parse(struct fuse_args *args, void *data,
    const struct fuse_opt opts[], fuse_opt_proc_t proc),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_parse)
        (fsp_fuse_env(), args, data, opts, proc);
})

FSP_FUSE_SYM(
int fuse_opt_add_arg(struct fuse_args *args, const char *arg),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_add_arg)
        (fsp_fuse_env(), args, arg);
})

FSP_FUSE_SYM(
int fuse_opt_insert_arg(struct fuse_args *args, int pos, const char *arg),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_insert_arg)
        (fsp_fuse_env(), args, pos, arg);
})

FSP_FUSE_SYM(
void fuse_opt_free_args(struct fuse_args *args),
{
    FSP_FUSE_API_CALL(fsp_fuse_opt_free_args)
        (fsp_fuse_env(), args);
})

FSP_FUSE_SYM(
int fuse_opt_add_opt(char **opts, const char *opt),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_add_opt)
        (fsp_fuse_env(), opts, opt);
})

FSP_FUSE_SYM(
int fuse_opt_add_opt_escaped(char **opts, const char *opt),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_add_opt_escaped)
        (fsp_fuse_env(), opts, opt);
})

FSP_FUSE_SYM(
int fuse_opt_match(const struct fuse_opt opts[], const char *opt),
{
    return FSP_FUSE_API_CALL(fsp_fuse_opt_match)
        (fsp_fuse_env(), opts, opt);
})

#ifdef __cplusplus
}
#endif

#endif
