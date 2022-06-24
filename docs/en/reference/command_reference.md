---
sidebar_label: Command Reference
sidebar_position: 1
slug: /command_reference
---
# Command Reference

There are many commands to help you manage your file system. This page provides a detailed reference for these commands.

## Overview

If you run `juicefs` by itself, it will print all available commands. In addition, you can add `-h/--help` flag after each command to get more information of it, e.g., `juicefs format -h`.

```bash
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.0.0-rc2+2022-06-24.fc6b1206

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
   --no-agent              disable pprof (:6060) and gops (:6070) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::note
If `juicefs` is not placed in your `$PATH`, you should run the script with the path to the script. For example, if `juicefs` is in current directory, you should use `./juicefs`. It is recommended to place `juicefs` in your `$PATH` for convenience. You can refer to [Installation & Upgrade](../getting-started/installation.md) for more information.
:::

:::note
If the command option is of boolean type, such as `--debug`, there is no need to set any value, just add `--debug` to the command to enable the function; this function is disabled if `--debug` is not added.
:::

## Auto Completion

:::note
This feature requires JuiceFS >= 0.15.2. It is implemented based on `github.com/urfave/cli/v2`. You can find more information [here](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling).
:::

To enable commands completion, simply source the script provided within `hack/autocomplete`. For example:

Bash:

```bash
source hack/autocomplete/bash_autocomplete
```

Zsh:

```bash
source hack/autocomplete/zsh_autocomplete
```

Please note the auto-completion is only enabled for the current session. If you want to apply it for all new sessions, add the `source` command to `.bashrc` or `.zshrc`:

```bash
echo "source path/to/bash_autocomplete" >> ~/.bashrc
```

or

```bash
echo "source path/to/zsh_autocomplete" >> ~/.zshrc
```

Alternatively, if you are using bash on a Linux system, you may just copy the script to `/etc/bash_completion.d` and rename it to `juicefs`:

```bash
sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
```

```shell
source /etc/bash_completion.d/juicefs
```

## Commands

### juicefs format

#### Description

Format a volume. It's the first step for initializing a new file system volume.

#### Synopsis

```
juicefs format [command options] META-URL NAME
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](how_to_setup_metadata_engine.md)" for details.
- **NAME**: the name of the file system

#### Options

`--block-size value`<br />
size of block in KiB (default: 4096)

`--capacity value`<br />
the limit for space in GiB (default: unlimited)

`--inodes value`<br />
the limit for number of inodes (default: unlimited)

`--compress value`<br />
compression algorithm (lz4, zstd, none) (default: "none")

`--shards value`<br />
store the blocks into N buckets by hash of key (default: 0)

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](how_to_setup_object_storage.md#supported-object-storage) for all supported object storage types)

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

### juicefs mount

#### Description

Mount a volume. The volume shoud be formatted first.

#### Synopsis

```
juicefs mount [command options] META-URL MOUNTPOINT
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](how_to_setup_metadata_engine.md)" for details.
- **MOUNTPOINT**: file system mount point, e.g. `/mnt/jfs`, `Z:`.

#### Options

`--metrics value`<br />
address to export metrics (default: "127.0.0.1:9567")

`--consul value`<br />
consul address to register (default: "127.0.0.1:8500")

`--no-usage-report`<br />
do not send usage report (default: false)

`-d, --background`<br />
run in background (default: false)

`--no-syslog`<br />
disable syslog (default: false)

`--log value`<br />
path of log file when running in background (default: `$HOME/.juicefs/juicefs.log` or `/var/log/juicefs.log`)

`-o value`<br />
other FUSE options (see [this document](../reference/fuse_mount_options.md) for more information)

`--attr-cache value`<br />
attributes cache timeout in seconds (default: 1)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 1)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1)

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
number of threads to delete objects (default: 2)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `"/var/jfsCache"`)

`--cache-size value`<br />
size of cached objects in MiB (default: 102400)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1)

`--cache-partial-only`<br />
cache random/small read only (default: false)

`--read-only`<br />
allow lookup/read operations only (default: false)

`--open-cache value`<br />
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`<br />
mount a sub-directory as root (default: "")

### juicefs umount

#### Description

Unmount a volume.

#### Synopsis

```
juicefs umount [command options] MOUNTPOINT
```

#### Options

`-f, --force`<br />
force unmount a busy mount point (default: false)

### juicefs gateway

#### Description

Start an S3-compatible gateway.

#### Synopsis

```
juicefs gateway [command options] META-URL ADDRESS
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](how_to_setup_metadata_engine.md)" for details.
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
number of threads to delete objects (default: 2)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`)

`--cache-size value`<br />
size of cached objects in MiB (default: 102400)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1)

`--cache-partial-only`<br />
cache random/small read only (default: false)

`--read-only`<br />
allow lookup/read operations only (default: false)

`--open-cache value`<br />
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`<br />
mount a sub-directory as root (default: "")

`--attr-cache value`<br />
attributes cache timeout in seconds (default: 1)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 0)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1)

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


### juicefs webdav

#### Description

Start a WebDAV server.

#### Synopsis

```
juicefs webdav [command options] META-URL ADDRESS
```

- **META-URL**: Database URL for metadata storage, see "[JuiceFS supported metadata engines](how_to_setup_metadata_engine.md)" for details.
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
number of threads to delete objects (default: 2)

`--buffer-size value`<br />
total read/write buffering in MiB (default: 300)

`--upload-limit value`<br />
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`<br />
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`<br />
prefetch N blocks in parallel (default: 1)

`--writeback`<br />
upload objects in background (default: false)

`--upload-delay`<br />
delayed duration for uploading objects ("s", "m", "h") (default: 0s)

`--cache-dir value`<br />
directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`)

`--cache-size value`<br />
size of cached objects in MiB (default: 102400)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1)

`--cache-partial-only`<br />
cache random/small read only (default: false)

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
attributes cache timeout in seconds (default: 1)

`--entry-cache value`<br />
file entry cache timeout in seconds (default: 0)

`--dir-entry-cache value`<br />
dir entry cache timeout in seconds (default: 1)

`--gzip`<br />
compress served files via gzip (default: false)

`--disallowList`<br />
disallow list a directory (default: false)

`--access-log value`<br />
path for JuiceFS access log

`--metrics value`<br />
address to export metrics (default: "127.0.0.1:9567")

`--consul value`<br />
consul address to register (default: "127.0.0.1:8500")

`--no-usage-report`<br />
do not send usage report (default: false)

### juicefs sync

#### Description

Sync between two storage.

#### Synopsis

```
juicefs sync [command options] SRC DST
```

- **SRC**: source path
- **DST**: destination path

The format of both source and destination paths is `[NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]`, in which:

- `NAME`: JuiceFS supported data storage types (e.g. `s3`, `oss`) (please refer to [this document](how_to_setup_object_storage.md#supported-object-storage)).
- `ACCESS_KEY` and `SECRET_KEY`: The credential required to access the data storage (please refer to [this document](how_to_setup_object_storage.md#access-key-and-secret-key)).
- `BUCKET[.ENDPOINT]`: The access address of the data storage service. The format may be different for different storage types, and please refer to [the document](how_to_setup_object_storage.md#supported-object-storage).
- `[/PREFIX]`: Optional, a prefix for the source and destination paths that can be used to limit synchronization of data only in certain paths.

For a detailed introduction to the `sync` subcommand, please refer to the [documentation](../administration/sync.md).

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

` --limit value`<br />
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

### juicefs rmr

#### Description

Remove all files in directories recursively.

#### Synopsis

```
juicefs rmr PATH ...
```

### juicefs info

#### Description

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

### juicefs bench

#### Description

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

### juicefs objbench

#### Description

Run basic benchmarks on the target object storage to test if it works as expected.

#### Synopsis

```shell
juicefs objbench [command options] BUCKET
```

For a detailed introduction to the `objbench` subcommand, please refer to the [documentation](../benchmark/performance_evaluation_guide.md#juicefs-objbench).

#### Options

`--storage value`<br />
Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, please refer to [documentation](how_to_setup_object_storage.md#supported-object-storage) for all supported object storage types)

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

### juicefs gc

#### Description

Collect leaked objects.

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

### juicefs fsck

#### Description

Check consistency of file system.

#### Synopsis

```
juicefs fsck [command options] META-URL
```

### juicefs profile

#### Description

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

### juicefs stats

#### Description

Show runtime statistics.

#### Synopsis

```
juicefs stats [command options] MOUNTPOINT
```

#### Options

`--schema value`<br />
schema string that controls the output sections (u: usage, f: fuse, m: meta, c: blockcache, o: object, g: go) (default: "ufmco")

`--interval value`<br />
interval in seconds between each update (default: 1)

`--verbosity value`<br />
verbosity level, 0 or 1 is enough for most cases (default: 0)

`--nocolor`<br />
disable colors (default: false)

### juicefs status

#### Description

Show status of JuiceFS.

#### Synopsis

```
juicefs status [command options] META-URL
```

#### Options

`--session value, -s value`<br />
show detailed information (sustained inodes, locks) of the specified session (sid) (default: 0)

### juicefs warmup

#### Description

Build cache for target directories/files.

#### Synopsis

```
juicefs warmup [command options] [PATH ...]
```

#### Options

`--file value, -f value`<br />
file containing a list of paths

`--threads value, -p value`<br />
number of concurrent workers (default: 50)

`--background, -b`<br />
run in background (default: false)

### juicefs dump

#### Description

Dump metadata into a JSON file.

#### Synopsis

```
juicefs dump [command options] META-URL [FILE]
```

When the FILE is not provided, STDOUT will be used instead.

#### Options

`--subdir value`<br />
only dump a sub-directory.

### juicefs load

#### Description

Load metadata from a previously dumped JSON file.

#### Synopsis

```
juicefs load [command options] META-URL [FILE]
```

When the FILE is not provided, STDIN will be used instead.

### juicefs config

#### Description

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

### juicefs destroy

#### Description

Destroy an existing volume

#### Synopsis

```
juicefs destroy [command options] META-URL UUID
```

#### Options

`--force`<br />
skip sanity check and force destroy the volume (default: false)
