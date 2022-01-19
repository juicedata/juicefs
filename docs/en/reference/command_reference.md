---
sidebar_label: Command Reference
sidebar_position: 1
slug: /command_reference
---
# Command Reference

There are many commands to help you manage your file system. This page provides a detailed reference for these commands.

## Overview

If you run `juicefs` by itself, it will print all available commands. In addition, you can add `-h/--help` flag after each command to get more information of it.

```bash
$ juicefs -h
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.0-dev (2021-12-27 3462bdbf)

COMMANDS:
   format   format a volume
   mount    mount a volume
   umount   unmount a volume
   gateway  S3-compatible gateway
   sync     sync between two storage
   rmr      remove directories recursively
   info     show internal information for paths or inodes
   bench    run benchmark to read/write/stat big/small files
   gc       collect any leaked objects
   fsck     Check consistency of file system
   profile  analyze access log
   stats    show runtime statistics
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump metadata into a JSON file
   load     load metadata from a previously dumped JSON file
   config   change config of a volume
   destroy  destroy an existing volume
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              Disable pprof (:6060) and gops (:6070) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   Apache License 2.0
```

:::note
If `juicefs` is not placed in your `$PATH`, you should run the script with the path to the script. For example, if `juicefs` is placed in current directory, you should use `./juicefs`. It is recommended to place `juicefs` in your `$PATH` for convenience.
:::

:::note
If the command option is boolean type (such as `--debug` option), there is no need to set a specific value when specifying this type of option. For example, it should not be written like `--debug true`, and directly written as `--debug`. If it is specified, it means this option takes effect, otherwise it does not take effect.
:::

## Auto Completion

:::note
This feature requires JuiceFS >= 0.15.2. It is implemented based on `github.com/urfave/cli/v2`. You can find more information [here](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling).
:::

To enable commands completion, simply source the script provided within `hack/autocomplete`. For example:

Bash:

```bash
$ source hack/autocomplete/bash_autocomplete
```

Zsh:

```bash
$ source hack/autocomplete/zsh_autocomplete
```

Please note the auto-completion is only enabled for the current session. If you want it for all new sessions, add the `source` command to `.bashrc` or `.zshrc`:

```bash
$ echo "source path/to/bash_autocomplete" >> ~/.bashrc
```

or

```bash
$ echo "source path/to/zsh_autocomplete" >> ~/.zshrc
```

Alternatively, if you are using bash on a Linux system, you may just copy the script to `/etc/bash_completion.d` and rename it to `juicefs`:

```bash
$ sudo cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
$ source /etc/bash_completion.d/juicefs
```

## Commands

### juicefs format

#### Description

Format a volume. It's the first step for initializing a new file system volume.

#### Synopsis

```
juicefs format [command options] META-URL NAME
```

#### Options

`--block-size value`<br />
size of block in KiB (default: 4096)

`--compress value`<br />
compression algorithm (lz4, zstd, none) (default: "none")

`--capacity value`<br />
the limit for space in GiB (default: unlimited)

`--inodes value`<br />
the limit for number of inodes (default: unlimited)

`--shards value`<br />
store the blocks into N buckets by hash of key (default: 0)

`--storage value`<br />
Object storage type (e.g. s3, gcs, oss, cos) (default: "file")

`--bucket value`<br />
A bucket URL to store data (default: `"$HOME/.juicefs/local"` or `"/var/jfs"`)

`--access-key value`<br />
Access key for object storage (env `ACCESS_KEY`)

`--secret-key value`<br />
Secret key for object storage (env `SECRET_KEY`)

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
customized endpoint to access object store

`--get-timeout value`<br />
the max number of seconds to download an object (default: 60)

`--put-timeout value`<br />
the max number of seconds to upload an object (default: 60)

`--io-retries value`<br />
number of retries after network failure (default: 30)

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
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `"/var/jfsCache"`)

`--cache-size value`<br />
size of cached objects in MiB (default: 102400)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1)

`--cache-partial-only`<br />
cache only random/small read (default: false)

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
unmount a busy mount point by force (default: false)

### juicefs gateway

#### Description

S3-compatible gateway.

#### Synopsis

```
juicefs gateway [command options] META-URL ADDRESS
```

#### Options

`--bucket value`<br />
customized endpoint to access object store

`--get-timeout value`<br />
the max number of seconds to download an object (default: 60)

`--put-timeout value`<br />
the max number of seconds to upload an object (default: 60)

`--io-retries value`<br />
number of retries after network failure (default: 30)

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
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`)

`--cache-size value`<br />
size of cached objects in MiB (default: 102400)

`--free-space-ratio value`<br />
min free space (ratio) (default: 0.1)

`--cache-partial-only`<br />
cache only random/small read (default: false)

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
Save the ETag for uploaded objects (default: false)


### juicefs sync

#### Description

Sync between two storage.

#### Synopsis

```
juicefs sync [command options] SRC DST
```

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
exclude keys containing PATTERN (POSIX regular expressions)

`--include PATTERN`<br />
only include keys containing PATTERN (POSIX regular expressions)

`--manager value`<br />
manager address

`--worker value`<br />
hosts (seperated by comma) to launch worker

`--bwlimit value`<br />
limit bandwidth in Mbps (0 means unlimited) (default: 0)

`--no-https`<br />
do not use HTTPS (default: false)

> **Note**: If source is the S3 storage with the public access setting, please use `anonymous` as access key ID.

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

Run benchmark, include read/write/stat big and small files.

#### Synopsis

```
juicefs bench [command options] PATH
```

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

### juicefs gc

#### Description

Collect any leaked objects.

#### Synopsis

```
juicefs gc [command options] META-URL
```

#### Options

`--delete`<br />
deleted leaked objects (default: false)

`--compact`<br />
compact all chunks with more than 1 slices (default: false).

`--threads value`<br />
number threads to delete leaked objects (default: 10)

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
track only specified UIDs(separated by comma ,)

`--gid value, -g value`<br />
track only specified GIDs(separated by comma ,)

`--pid value, -p value`<br />
track only specified PIDs(separated by comma ,)

`--interval value`<br />
flush interval in seconds; set it to 0 when replaying a log file to get an immediate result (default: 2)

### juicefs stats

#### Description

Show runtime statistics

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

Show status of JuiceFS

#### Synopsis

```
juicefs status [command options] META-URL
```

#### Options

`--session value, -s value`<br />
show detailed information (sustained inodes, locks) of the specified session (sid) (default: 0)

### juicefs warmup

#### Description

Build cache for target directories/files

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

Dump metadata into a JSON file

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

Load metadata from a previously dumped JSON file

#### Synopsis

```
juicefs load [command options] META-URL [FILE]
```

When the FILE is not provided, STDIN will be used instead.

### juicefs config

#### Description

Change config of a volume

#### Synopsis

```
juicefs config [command options] META-URL
```

#### Options

`--capacity value`<br />
the limit for space in GiB

`--inodes value`<br />
the limit for number of inodes

`--bucket value`<br />
a bucket URL to store data

`--access-key value`<br />
access key for object storage

`--secret-key value`<br />
secret key for object storage

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
