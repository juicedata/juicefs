#include <stdio.h>

static void (*log_callback)(const char *msg);

typedef void LogCallBack(const char *msg);

void jfs_set_logger(void*p);

void jfs_set_callback(LogCallBack *callback)
{
    log_callback = callback;
    jfs_set_logger(callback);
}

void jfs_callback(const char *msg)
{
    if (log_callback != NULL) {
        (*log_callback)(msg);
    } else {
        fprintf(stderr, "%s", msg);
    }
}
