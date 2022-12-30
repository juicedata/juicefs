---
title: Command Reference
sidebar_position: 1
slug: /command_reference
description: This article provides descriptions, usage and examples of all commands and options included in JuiceFS.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Overview

If you run `juicefs` by itself, it will print all available commands. In addition, you can add `-h/--help` flag after each command to get more information of it, e.g., `juicefs format -h`.

```bash
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.0.0+2022-08-01.0e7afe2d

COMMANDS:
   ADMIN:
     format   Format a volume
     config   Change configuration of a volume
     destroy  Destroy an existing volume
     gc       Garbage collector of objects in data storage
     fsck     Check consistency of a volume
     dump     Dump metadata into a JSON file
     load     Load metadata from a previously dumped JSON file
     version  Show version
   INSPECTOR:
     status   Show status of a volume
     stats    Show real time performance statistics of JuiceFS
     profile  Show profiling of operations completed in JuiceFS
     info     Show internal information of a path or inode
     debug    Show information from multiple dimensions such as the operating environment and system logs
   SERVICE:
     mount    Mount a volume
     umount   Unmount a volume
     gateway  Start an S3-compatible gateway
     webdav   Start a WebDAV server
   TOOL:
     bench     Run benchmarks on a path
     objbench  Run benchmarks on an object storage
     warmup    Build cache for target directories/files
     rmr       Remove directories recursively
     sync      Sync between two storages

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             show warning and errors only (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              disable pprof (:6060) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::note
If the command option is of boolean type, such as `--debug`, there is no need to set any value, just add `--debug` to the command to enable the function; this function is disabled if `--debug` is not added.
:::

## Auto Completion

:::note
This feature requires JuiceFS >= 0.15.2. It is implemented based on `github.com/urfave/cli/v2`. You can find more information [here](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling).
:::

To enable commands completion, simply source the script provided within [`hack/autocomplete`](https://github.com/juicedata/juicefs/tree/main/hack/autocomplete) directory. For example:

<Tabs groupId="juicefs-cli-autocomplete">
  <TabItem value="bash" label="Bash">

```shell
source hack/autocomplete/bash_autocomplete
```

  </TabItem>
  <TabItem value="zsh" label="Zsh">

```shell
source hack/autocomplete/zsh_autocomplete
```

  </TabItem>
</Tabs>

Please note the auto-completion is only enabled for the current session. If you want to apply it for all new sessions, add the `source` command to `.bashrc` or `.zshrc`:

<Tabs groupId="juicefs-cli-autocomplete">
  <TabItem value="bash" label="Bash">

```shell
echo "source path/to/bash_autocomplete" >> ~/.bashrc
```

  </TabItem>
  <TabItem value="zsh" label="Zsh">

```shell
echo "source path/to/zsh_autocomplete" >> ~/.zshrc
```

  </TabItem>
</Tabs>

Alternatively, if you are using bash on a Linux system, you may just copy the script to `/etc/bash_completion.d` and rename it to `juicefs`:

<Tabs>
  <TabItem value="bash" label="Bash">

```shell
sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
```

```shell
source /etc/bash_completion.d/juicefs
```

  </TabItem>
</Tabs>

## Commands

### `juicefs format` {#format}

Format a volume. It's the first step for initializing a new file system volume.

#### Synopsis

```
juicefs format [command options] META-URL NAME
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](../guide/how_to_set_up_metadata_engine.md)" for details.
- **NAME**: the name of the file system

#### Options

`--block-size value`<br />
size of block in KiB (default: 4096). 4M is usually a better default value because many object storage services use 4M as their internal block size, thus using the same block size in JuiceFS usually yields better performance

`--capacity value`<br />
storage space limit in GiB, set to 0 disable limit (default: 0). Capacity will include trash files, if trash is enabled

`--inodes value`<br />
the limit for number of inodes (0 means unlimited) (default: 0)

`--compress value`<br />
compression algorithm, choose from `lz4`, `zstd`, `none` (default: "none"). Enabling compression will inevitably affect performance, choose wisely

`--shards value`<br />
store the blocks into N buckets by hash of key (default: 0), when N is greater than 0, `bucket` should to be in the form of `%d`, e.g. `--bucket "juicefs-%d"`

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](../guide/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)

`--bucket value`<br />
A bucket URL to store data (default: `"$HOME/.juicefs/local"` or `"/var/jfs"`)

`--access-key value`<br />
Access Key for object storage (can also be set via the environment variable `ACCESS_KEY`)

`--secret-key value`<br />
Secret Key for object storage (can also be set via the environment variable `SECRET_KEY`)

`--session-token value`<br />
session token for object storage

`--encrypt-rsa-key value`<br />
A path to RSA private key (PEM)

`--trash-days value`<br />
number of days after which removed files will be permanently deleted (default: 1)

`--force`<br />
overwrite existing format (default: false)

`--no-update`<br />
don't update existing volume (default: false)

#### Examples

```bash
# Create a simple test volume (data will be stored in a local directory)
$ juicefs format sqlite3://myjfs.db myjfs

# Create a volume with Redis and S3
$ juicefs format redis://localhost myjfs --storage s3 --bucket https://mybucket.s3.us-east-2.amazonaws.com

# Create a volume with password protected MySQL
$ juicefs format mysql://jfs:mypassword@(127.0.0.1:3306)/juicefs myjfs
# A safer alternative
$ META_PASSWORD=mypassword juicefs format mysql://jfs:@(127.0.0.1:3306)/juicefs myjfs

# Create a volume with "quota" enabled
$ juicefs format sqlite3://myjfs.db myjfs --inode 1000000 --capacity 102400

# Create a volume with "trash" disabled
$ juicefs format sqlite3://myjfs.db myjfs --trash-days 0
```

### `juicefs mount` {#mount}

Mount a volume. The volume must be formatted in advance.

You can use any user to execute the mount command, but please ensure that the user has write permission to the cache directory (`--cache-dir`), please read ["Cache directory"](../guide/cache_management.md#cache-dir) documentation for more information.

#### Synopsis

```
juicefs mount [command options] META-URL MOUNTPOINT
```

- `META-URL`: Database URL for metadata storage, see "[JuiceFS supported metadata engines](../guide/how_to_set_up_metadata_engine.md)" for details.
- `MOUNTPOINT`: file system mount point, e.g. `/mnt/jfs`, `Z:`.

#### Options

`--metrics value`<br />
address to export metrics (default: "127.0.0.1:9567")

`--consul value`<br />
Consul address to register (default: "127.0.0.1:8500")

`--no-usage-report`<br />
do not send usage report (default: false)

`-d, --background`<br />
run in background (default: false)

`--no-syslog`<br />
disable syslog (default: false)

`--log value`<br />
path of log file when running in background (default: `$HOME/.juicefs/juicefs.log` or `/var/log/juicefs.log`)

`-o value`<br />
other FUSE options, see [FUSE Mount Options](../reference/fuse_mount_options.md)

`--attr-cache value`<br />
attributes cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--enable-xattr`<br />
enable extended attributes (xattr) (default: false)

`--bucket value`<br />
customized endpoint to access object storage

`--get-timeout value`<br />
the max number of seconds to download an object (default: 60)

`--put-timeout value`<br />
the max number of seconds to upload an object (default: 60)

`--io-retries value`<br />
number of retries after network failure (default: 10)

`--max-uploads value`<br />
number of connections to upload (default: 20)

`--max-deletes value`<br />
number of threads to delete objects (default: 10)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false), see [Client write data cache](../guide/cache_management.md#writeback)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `"/var/jfsCache"`), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-size value`<br />
size of cached object for read in MiB (default: 102400), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--free-space-ratio value`<br />
min free space ratio (default: 0.1), if [Client write data cache](../guide/cache_management.md#writeback) is enabled, this option also controls write cache size, see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-partial-only`<br />
cache random/small read only (default: false), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--read-only`<br />
allow lookup/read operations only (default: false)

`--open-cache value`<br />
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`<br />
mount a sub-directory as root (default: "")

`--backup-meta value`<br />
interval (in seconds) to automatically backup metadata in the object storage (0 means disable backup) (default: "3600")

`--heartbeat value`<br />
interval (in seconds) to send heartbeat; it's recommended that all clients use the same heartbeat value (default: "12")

`--upload-delay value`<br />
delayed duration for uploading objects ("s", "m", "h") (default: 0s)

`--no-bgjob`<br />
disable background jobs (clean-up, backup, etc.) (default: false)

#### Examples

```bash
# Mount in foreground
$ juicefs mount redis://localhost /mnt/jfs

# Mount in background with password protected Redis
$ juicefs mount redis://:mypassword@localhost /mnt/jfs -d
# A safer alternative
$ META_PASSWORD=mypassword juicefs mount redis://localhost /mnt/jfs -d

# Mount with a sub-directory as root
$ juicefs mount redis://localhost /mnt/jfs --subdir /dir/in/jfs

# Enable "writeback" mode, which improves performance at the risk of losing objects
$ juicefs mount redis://localhost /mnt/jfs -d --writeback

# Enable "read-only" mode
$ juicefs mount redis://localhost /mnt/jfs -d --read-only

# Disable metadata backup
$ juicefs mount redis://localhost /mnt/jfs --backup-meta 0
```

### `juicefs umount`

Unmount a volume.

#### Synopsis

```
juicefs umount [command options] MOUNTPOINT
```

#### Options

`-f, --force`<br />
force unmount a busy mount point (default: false)

#### Examples

```bash
juicefs umount /mnt/jfs
```

### `juicefs gateway`

Start an S3-compatible gateway.

#### Synopsis

```
juicefs gateway [command options] META-URL ADDRESS
```

- **META-URL**: Database URL for metadata storage, see ["JuiceFS supported metadata engines"](../guide/how_to_set_up_metadata_engine.md) for details.
- **ADDRESS**: S3 gateway address and listening port, for example: `localhost:9000`

#### Options

`--bucket value`<br />
customized endpoint to access an object storage

`--get-timeout value`<br />
the max number of seconds to download an object (default: 60)

`--put-timeout value`<br />
the max number of seconds to upload an object (default: 60)

`--io-retries value`<br />
number of retries after network failure (default: 10)

`--max-uploads value`<br />
number of connections to upload (default: 20)

`--max-deletes value`<br />
number of threads to delete objects (default: 10)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false), see [Client write data cache](../guide/cache_management.md#writeback)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-size value`<br />
size of cached object for read in MiB (default: 102400), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-partial-only`<br />
cache random/small read only (default: false), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--read-only`<br />
allow lookup/read operations only (default: false)

`--open-cache value`<br />
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`<br />
mount a sub-directory as root (default: "")

`--attr-cache value`<br />
attributes cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 0), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--access-log value`<br />
path for JuiceFS access log

`--metrics value`<br />
address to export metrics (default: "127.0.0.1:9567")

`--no-usage-report`<br />
do not send usage report (default: false)

`--no-banner`<br />
disable MinIO startup information (default: false)

`--multi-buckets`<br />
use top level of directories as buckets (default: false)

`--keep-etag`<br />
save the ETag for uploaded objects (default: false)

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](../guide/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)

`--upload-delay value`<br />
delayed duration (in seconds) for uploading objects (default: "0")

`--backup-meta value`<br />
interval (in seconds) to automatically backup metadata in the object storage (0 means disable backup) (default: "3600")

`--heartbeat value`<br />
interval (in seconds) to send heartbeat; it's recommended that all clients use the same heartbeat value (default: "12")

`--no-bgjob`<br />
disable background jobs (clean-up, backup, etc.) (default: false)

`--umask value`<br />
umask for new file and directory in octal (default: "022")

`--consul value`<br />
Consul address to register (default: "127.0.0.1:8500")

#### Examples

```bash
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
juicefs gateway redis://localhost localhost:9000
```

### `juicefs webdav`

Start a WebDAV server.

#### Synopsis

```
juicefs webdav [command options] META-URL ADDRESS
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](../guide/how_to_set_up_metadata_engine.md)" for details.
- **ADDRESS**: WebDAV address and listening port, for example: `localhost:9007`

#### Options

`--bucket value`<br />
customized endpoint to access an object storage

`--get-timeout value`<br />
the max number of seconds to download an object (default: 60)

`--put-timeout value`<br />
the max number of seconds to upload an object (default: 60)

`--io-retries value`<br />
number of retries after network failure (default: 10)

`--max-uploads value`<br />
number of connections to upload (default: 20)

`--max-deletes value`<br />
number of threads to delete objects (default: 10)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false), see [Client write data cache](../guide/cache_management.md#writeback)

`--upload-delay value`<br />
delayed duration for uploading objects ("s", "m", "h") (default: 0s)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-size value`<br />
size of cached object for read in MiB (default: 102400), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--cache-partial-only`<br />
cache random/small read only (default: false), see [Client read data cache](../guide/cache_management.md#client-read-cache)

`--read-only`<br />
allow lookup/read operations only (default: false)

`--backup-meta`<br />
interval to automatically backup metadata in the object storage (0 means disable backup) (default: 1h0m0s)

`--no-bgjob`<br />
disable background jobs (clean-up, backup, etc.) (default: false)

`--open-cache value`<br />
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`<br />
mount a sub-directory as root (default: "")

`--attr-cache value`<br />
attributes cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 0), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1), read [Kernel Metadata Cache](../guide/cache_management.md#kernel-metadata-cache)

`--gzip`<br />
compress served files via gzip (default: false)

`--disallowList`<br />
disallow list a directory (default: false)

`--access-log value`<br />
path for JuiceFS access log

`--metrics value`<br />
address to export metrics (default: "127.0.0.1:9567")

`--consul value`<br />
Consul address to register (default: "127.0.0.1:8500")

`--no-usage-report`<br />
do not send usage report (default: false)

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](../guide/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)

`--heartbeat value`<br />
interval (in seconds) to send heartbeat; it's recommended that all clients use the same heartbeat value (default: "12")

#### Examples

```bash
juicefs webdav redis://localhost localhost:9007
```

### `juicefs sync`

Sync between two storage.

#### Synopsis

```
juicefs sync [command options] SRC DST
```

- **SRC**: source path
- **DST**: destination path

The format of both source and destination paths is `[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]`, in which:

- `NAME`: JuiceFS supported data storage types (e.g. `s3`, `oss`) (please refer to [this document](../guide/how_to_set_up_object_storage.md#supported-object-storage)).
- `ACCESS_KEY` and `SECRET_KEY`: The credential required to access the data storage (please refer to [this document](../guide/how_to_set_up_object_storage.md#access-key-and-secret-key)).
- `TOKEN` token used to access the object storage, as some object storage supports the use of temporary token to obtain permission for a limited time
- `BUCKET[.ENDPOINT]`: The access address of the data storage service. The format may be different for different storage types, and please refer to [the document](../guide/how_to_set_up_object_storage.md#supported-object-storage).
- `[/PREFIX]`: Optional, a prefix for the source and destination paths that can be used to limit synchronization of data only in certain paths.

For a detailed introduction to the `sync` subcommand, please refer to the [documentation](../guide/sync.md).

#### Options

`--start KEY, -s KEY`<br />
the first KEY to sync

`--end KEY, -e KEY`<br />
the last KEY to sync

`--threads value, -p value`<br />
number of concurrent threads (default: 10)

`--http-port PORT`<br />
HTTP PORT to listen to (default: 6070)

`--update, -u`<br />
update existing file if the source is newer (default: false)

`--force-update, -f`<br />
always update existing file (default: false)

`--perms`<br />
preserve permissions (default: false)

`--dirs`<br />
Sync directories or holders (default: false)

`--dry`<br />
don't copy file (default: false)

`--delete-src, --deleteSrc`<br />
delete objects from source after synced (default: false)

`--delete-dst, --deleteDst`<br />
delete extraneous objects from destination (default: false)

`--exclude PATTERN`<br />
exclude Key matching PATTERN

`--include PATTERN`<br />
don't exclude Key matching PATTERN, need to be used with `--exclude` option

`--links, -l`<br />
copy symlinks as symlinks (default: false)

`--limit value`<br />
limit the number of objects that will be processed (default: -1)

`--manager value`<br />
manager address

`--worker value`<br />
hosts (seperated by comma) to launch worker

`--bwlimit value`<br />
limit bandwidth in Mbps (0 means unlimited) (default: 0)

`--no-https`<br />
do not use HTTPS (default: false)

`--check-all`<br />
verify integrity of all files in source and destination (default: false)

`--check-new`<br />
verify integrity of newly copied files (default: false)

#### Examples

```bash
# Sync object from OSS to S3
$ juicefs sync oss://mybucket.oss-cn-shanghai.aliyuncs.com s3://mybucket.s3.us-east-2.amazonaws.com

# Sync objects from S3 to JuiceFS
$ juicefs mount -d redis://localhost /mnt/jfs
$ juicefs sync s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# SRC: a1/b1,a2/b2,aaa/b1   DST: empty   sync result: aaa/b1
$ juicefs sync --exclude='a?/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# SRC: a1/b1,a2/b2,aaa/b1   DST: empty   sync result: a1/b1,aaa/b1
$ juicefs sync --include='a1/b1' --exclude='a[1-9]/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/

# SRC: a1/b1,a2/b2,aaa/b1,b1,b2  DST: empty   sync result: a1/b1,b2
$ juicefs sync --include='a1/b1' --exclude='a*' --include='b2' --exclude='b?' s3://mybucket.s3.us-east-2.amazonaws.com/ /mnt/jfs/
```

### `juicefs rmr`

Remove all the files and subdirectories, similar to rm -rf, except this command deals with metadata directly (bypassing POSIX API), thus is much faster.

If trash is enabled, deleted files are moved into trash. read more at [Trash](../security/trash.md).

#### Synopsis

```
juicefs rmr PATH ...
```

#### Examples

```bash
juicefs rmr /mnt/jfs/foo
```

### `juicefs info` {#info}

Show internal information for given paths or inodes.

#### Synopsis

```
juicefs info [command options] PATH or INODE
```

#### Options

`--inode, -i`<br />
use inode instead of path (current dir should be inside JuiceFS) (default: false)

`--recursive, -r`<br />
get summary of directories recursively (NOTE: it may take a long time for huge trees) (default: false)

`--raw`<br />
show internal raw information (default: false)

#### Examples

```bash
# Check a path
$ juicefs info /mnt/jfs/foo

# Check an inode
$ cd /mnt/jfs
$ juicefs info -i 100
```

### `juicefs bench`

Run benchmark, including read/write/stat for big and small files.

#### Synopsis

```
juicefs bench [command options] PATH
```

For a detailed introduction to the `bench` subcommand, please refer to the [documentation](../benchmark/performance_evaluation_guide.md#juicefs-bench).

#### Options

`--block-size value`<br />
block size in MiB (default: 1)

`--big-file-size value`<br />
size of big file in MiB (default: 1024)

`--small-file-size value`<br />
size of small file in MiB (default: 0.1)

`--small-file-count value`<br />
number of small files (default: 100)

`--threads value, -p value`<br />
number of concurrent threads (default: 1)

#### Examples

```bash
# Run benchmarks with 4 threads
$ juicefs bench /mnt/jfs -p 4

# Run benchmarks of only small files
$ juicefs bench /mnt/jfs --big-file-size 0
```

### `juicefs objbench`

Run basic benchmarks on the target object storage to test if it works as expected.

#### Synopsis

```shell
juicefs objbench [command options] BUCKET
```

For a detailed introduction to the `objbench` subcommand, please refer to the [documentation](../benchmark/performance_evaluation_guide.md#juicefs-objbench).

#### Options

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](../guide/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)

`--access-key value`<br />
Access Key for object storage (can also be set via the environment variable `ACCESS_KEY`)

`--secret-key value`<br />
Secret Key for object storage (can also be set via the environment variable `SECRET_KEY`)

`--block-size value`<br />
size of each IO block in KiB (default: 4096)

`--big-object-size value`<br />
size of each big object in MiB (default: 1024)

`--small-object-size value`<br />
size of each small object in KiB (default: 128)

`--skip-functional-tests`<br />
skip functional tests (default: false)

`--threads value, -p value`<br />
number of concurrent threads (default: 4)

#### Examples

```bash
# Run benchmarks on S3
$ ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage s3  https://mybucket.s3.us-east-2.amazonaws.com -p 6
```

### `juicefs gc` {#gc}

用来处理「对象泄漏」，以及因为覆盖写而产生的碎片数据的命令。详见[「状态检查 & 维护」](../administration/status_check_and_maintenance.md#gc)。

Deal with leaked objects, and garbage fragments produced by file overwrites. See [Status Check & Maintenance](../administration/status_check_and_maintenance.md#gc).

#### Synopsis

```
juicefs gc [command options] META-URL
```

#### Options

`--delete`<br />
delete leaked objects (default: false)

`--compact`<br />
compact all chunks with more than 1 slices (default: false).

`--threads value`<br />
number of threads to delete leaked objects (default: 10)

#### Examples

```bash
# Check only, no writable change
$ juicefs gc redis://localhost

# Trigger compaction of all slices
$ juicefs gc redis://localhost --compact

# Delete leaked objects
$ juicefs gc redis://localhost --delete
```

### `juicefs fsck`

Check consistency of file system.

#### Synopsis

```
juicefs fsck [command options] META-URL
```

#### Examples

```bash
juicefs fsck redis://localhost
```

### `juicefs profile`

Analyze [access log](../administration/fault_diagnosis_and_analysis.md#access-log).

#### Synopsis

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

#### Options

`--uid value, -u value`<br />
only track specified UIDs (separated by comma ,)

`--gid value, -g value`<br />
only track specified GIDs(separated by comma ,)

`--pid value, -p value`<br />
only track specified PIDs(separated by comma ,)

`--interval value`<br />
flush interval in seconds; set it to 0 when replaying a log file to get an immediate result (default: 2)

#### Examples

```bash
# Monitor real time operations
$ juicefs profile /mnt/jfs

# Replay an access log
$ cat /mnt/jfs/.accesslog > /tmp/jfs.alog
# Press Ctrl-C to stop the "cat" command after some time
$ juicefs profile /tmp/jfs.alog

# Analyze an access log and print the total statistics immediately
$ juicefs profile /tmp/jfs.alog --interval 0
```

### `juicefs stats`

Show runtime statistics.

#### Synopsis

```
juicefs stats [command options] MOUNTPOINT
```

#### Options

`--schema value`<br />
schema string that controls the output sections (u: `usage`, f: `fuse`, m: `meta`, c: `blockcache`, o: `object`, g: `go`) (default: "ufmco")

`--interval value`<br />
interval in seconds between each update (default: 1)

`--verbosity value`<br />
verbosity level, 0 or 1 is enough for most cases (default: 0)

#### Examples

```bash
$ juicefs stats /mnt/jfs

# More metrics
$ juicefs stats /mnt/jfs -l 1
```

### `juicefs status`

Show status of JuiceFS.

#### Synopsis

```
juicefs status [command options] META-URL
```

#### Options

`--session value, -s value`<br />
show detailed information (sustained inodes, locks) of the specified session (SID) (default: 0)

#### Examples

```bash
juicefs status redis://localhost
```

### `juicefs warmup` {#warmup}

Download data to local cache in advance, to achieve better performance on application's first read.

You can specify a mount point path to recursively warm-up all files under this path. You can also specify a file through the `--file` option to only warm-up the files contained in it.

#### Synopsis

```
juicefs warmup [command options] [PATH ...]
```

#### Options

`--file value, -f value`<br />
file containing a list of paths (each line is a file path)

`--threads value, -p value`<br />
number of concurrent workers (default: 50)

`--background, -b`<br />
run in background (default: false)

#### Examples

```bash
# Warm all files in datadir
$ juicefs warmup /mnt/jfs/datadir

# Warm only three files in datadir
$ cat /tmp/filelist
/mnt/jfs/datadir/f1
/mnt/jfs/datadir/f2
/mnt/jfs/datadir/f3
$ juicefs warmup -f /tmp/filelist
```

### `juicefs dump`

Dump metadata into a JSON file.

#### Synopsis

```
juicefs dump [command options] META-URL [FILE]
```

When the FILE is not provided, STDOUT will be used instead.

#### Options

`--subdir value`<br />
only dump a sub-directory.

#### Examples

```bash
$ juicefs dump redis://localhost meta-dump

# Dump only a subtree of the volume
$ juicefs dump redis://localhost sub-meta-dump --subdir /dir/in/jfs
```

### `juicefs load`

Load metadata from a previously dumped JSON file.

#### Synopsis

```
juicefs load [command options] META-URL [FILE]
```

When the FILE is not provided, STDIN will be used instead.

#### Examples

```bash
juicefs load redis://localhost/1 meta-dump
```

### `juicefs config`

Change config of a volume.

#### Synopsis

```
juicefs config [command options] META-URL
```

#### Options

`--capacity value`<br />
limit for space in GiB

`--inodes value`<br />
limit for number of inodes

`--bucket value`<br />
a bucket URL to store data

`--access-key value`<br />
access key for object storage

`--secret-key value`<br />
secret key for object storage

`--session-token value`<br />
session token for object storage

`--trash-days value`<br />
number of days after which removed files will be permanently deleted

`--force`<br />
skip sanity check and force update the configurations (default: false)

`--encrypt-secret`<br />
encrypt the secret key if it was previously stored in plain format (default: false)

`--min-client-version value`<br />
minimum client version allowed to connect

`--max-client-version value`<br />
maximum client version allowed to connect

#### Examples

```bash
# Show the current configurations
$ juicefs config redis://localhost

# Change volume "quota"
$ juicefs config redis://localhost --inode 10000000 --capacity 1048576

# Change maximum days before files in trash are deleted
$ juicefs config redis://localhost --trash-days 7

# Limit client version that is allowed to connect
$ juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0
```

### `juicefs destroy`

Destroy an existing volume, will delete relevant data in metadata engine and object storage. See [How to destroy a file system](../administration/destroy.md).

#### Synopsis

```
juicefs destroy [command options] META-URL UUID
```

#### Options

`--force`<br />
skip sanity check and force destroy the volume (default: false)

#### Examples

```bash
juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892
```

### `juicefs debug`

It collects and displays information from multiple dimensions such as the operating environment and system logs to help better locate errors

#### Synopsis

```
juicefs debug [command options] MOUNTPOINT
```

#### Options

`--out-dir value`<br />
The output directory of the results, automatically created if the directory does not exist (default: ./debug/)

`--stats-sec value`<br />
The number of seconds to sample .stats file (default: 5)

`--limit value`<br />
The number of log entries collected, from newest to oldest, if not specified, all entries will be collected

`--trace-sec value`<br />
The number of seconds to sample trace metrics (default: 5)

`--profile-sec value`<br />
The number of seconds to sample profile metrics (default: 30)

#### Examples

```bash
# Collect and display information about the mount point /mnt/jfs
$ juicefs debug /mnt/jfs

# Specify the output directory as /var/log
$ juicefs debug --out-dir=/var/log /mnt/jfs

# Get the last up to 1000 log entries
$ juicefs debug --out-dir=/var/log --limit=1000 /mnt/jfs
```
