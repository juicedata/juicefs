/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
