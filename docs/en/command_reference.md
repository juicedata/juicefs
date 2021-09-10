# Command Reference

There are many commands to help you manage your file system. This page provides a detailed reference for these commands.

* [Overview](#Overview)
* [Auto Completion](#Auto-Completion)
* [Commands](#Commands)
   * [juicefs format](#juicefs-format)
   * [juicefs mount](#juicefs-mount)
   * [juicefs umount](#juicefs-umount)
   * [juicefs gateway](#juicefs-gateway)
   * [juicefs sync](#juicefs-sync)
   * [juicefs rmr](#juicefs-rmr)
   * [juicefs info](#juicefs-info)
   * [juicefs bench](#juicefs-bench)
   * [juicefs gc](#juicefs-gc)
   * [juicefs fsck](#juicefs-fsck)
   * [juicefs profile](#juicefs-profile)
   * [juicefs status](#juicefs-status)
   * [juicefs warmup](#juicefs-warmup)
   * [juicefs dump](#juicefs-dump)
   * [juicefs load](#juicefs-load)

## Overview

If you run `juicefs` by itself, it will print all available commands. In addition, you can add `-h/--help` flag after each command to get more information of it.

```bash
$ juicefs -h
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.16-dev (2021-08-09 2f17d86)

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
   stats    show runtime stats
   status   show status of JuiceFS
   warmup   build cache for target directories/files
   dump     dump metadata into a JSON file
   load     load metadata from a previously dumped JSON file
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             only warning and errors (default: false)
   --trace                 enable trace log (default: false)
   --no-agent              Disable pprof (:6060) and gops (:6070) agent (default: false)
   --help, -h              show help (default: false)
   --version, -V           print only the version (default: false)

COPYRIGHT:
   AGPLv3
```

> **Note**: If `juicefs` is not placed in your `$PATH`, you should run the script with the path to the script. For example, if `juicefs` is placed in current directory, you should use `./juicefs`. It is recommended to place `juicefs` in your `$PATH` for convenience.

> **Note**: If the command option is boolean type (such as `--debug` option), there is no need to set a specific value when specifying this type of option. For example, it should not be written like `--debug true`, and directly written as `--debug`. If it is specified, it means this option takes effect, otherwise it does not take effect.

## Auto Completion

> **Note**: This feature requires JuiceFS >= 0.15.0. It is implemented based on `github.com/urfave/cli/v2`. You can find more information [here](https://github.com/urfave/cli/blob/master/docs/v2/manual.md#enabling).

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

`--block-size value`\
size of block in KiB (default: 4096)

`--compress value`\
compression algorithm (lz4, zstd, none) (default: "none")

`--capacity value`\
the limit for space in GiB (default: unlimited)

`--inodes value`\
the limit for number of inodes (default: unlimited)

`--shards value`\
store the blocks into N buckets by hash of key (default: 0)

`--storage value`\
Object storage type (e.g. s3, gcs, oss, cos) (default: "file")

`--bucket value`\
A bucket URL to store data (default: `"$HOME/.juicefs/local"` or `"/var/jfs"`)

`--access-key value`\
Access key for object storage (env `ACCESS_KEY`)

`--secret-key value`\
Secret key for object storage (env `SECRET_KEY`)

`--encrypt-rsa-key value`\
A path to RSA private key (PEM)

`--force`\
overwrite existing format (default: false)

`--no-update`\
don't update existing volume (default: false)

### juicefs mount

#### Description

Mount a volume. The volume shoud be formatted first.

#### Synopsis

```
juicefs mount [command options] META-URL MOUNTPOINT
```

#### Options

`--metrics value`\
address to export metrics (default: "127.0.0.1:9567")

`--no-usage-report`\
do not send usage report (default: false)

`-d, --background`\
run in background (default: false)

`--no-syslog`\
disable syslog (default: false)

`--log value`\
path of log file when running in background (default: `$HOME/.juicefs/juicefs.log` or `/var/log/juicefs.log`)

`-o value`\
other FUSE options (see [this document](fuse_mount_options.md) for more information)

`--attr-cache value`\
attributes cache timeout in seconds (default: 1)

`--entry-cache value`\
file entry cache timeout in seconds (default: 1)

`--dir-entry-cache value`\
dir entry cache timeout in seconds (default: 1)

`--enable-xattr`\
enable extended attributes (xattr) (default: false)

`--get-timeout value`\
the max number of seconds to download an object (default: 60)

`--put-timeout value`\
the max number of seconds to upload an object (default: 60)

`--io-retries value`\
number of retries after network failure (default: 30)

`--max-uploads value`\
number of connections to upload (default: 20)

`--buffer-size value`\
total read/write buffering in MiB (default: 300)

`--upload-limit value`\
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`\
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`\
prefetch N blocks in parallel (default: 1)

`--writeback`\
upload objects in background (default: false)

`--cache-dir value`\
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `"/var/jfsCache"`)

`--cache-size value`\
size of cached objects in MiB (default: 1024)

`--free-space-ratio value`\
min free space (ratio) (default: 0.1)

`--cache-partial-only`\
cache only random/small read (default: false)

`--read-only`\
allow lookup/read operations only (default: false)

`--open-cache value`\
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`\
mount a sub-directory as root (default: "")

### juicefs umount

#### Description

Unmount a volume.

#### Synopsis

```
juicefs umount [command options] MOUNTPOINT
```

#### Options

`-f, --force`\
unmount a busy mount point by force (default: false)

### juicefs gateway

#### Description

S3-compatible gateway.

#### Synopsis

```
juicefs gateway [command options] META-URL ADDRESS
```

#### Options

`--get-timeout value`\
the max number of seconds to download an object (default: 60)

`--put-timeout value`\
the max number of seconds to upload an object (default: 60)

`--io-retries value`\
number of retries after network failure (default: 30)

`--max-uploads value`\
number of connections to upload (default: 20)

`--buffer-size value`\
total read/write buffering in MiB (default: 300)

`--upload-limit value`\
bandwidth limit for upload in Mbps (default: 0)

`--download-limit value`\
bandwidth limit for download in Mbps (default: 0)

`--prefetch value`\
prefetch N blocks in parallel (default: 1)

`--writeback`\
upload objects in background (default: false)

`--cache-dir value`\
directory paths of local cache, use colon to separate multiple paths (default: `"$HOME/.juicefs/cache"` or `/var/jfsCache`)

`--cache-size value`\
size of cached objects in MiB (default: 1024)

`--free-space-ratio value`\
min free space (ratio) (default: 0.1)

`--cache-partial-only`\
cache only random/small read (default: false)

`--read-only`\
allow lookup/read operations only (default: false)

`--open-cache value`\
open file cache timeout in seconds (0 means disable this feature) (default: 0)

`--subdir value`\
mount a sub-directory as root (default: "")

`--access-log value`\
path for JuiceFS access log

`--no-usage-report`\
do not send usage report (default: false)

`--no-banner`\
disable MinIO startup information (default: false)

### juicefs sync

#### Description

Sync between two storage.

#### Synopsis

```
juicefs sync [command options] SRC DST
```

#### Options

`--start KEY, -s KEY`\
the first KEY to sync

`--end KEY, -e KEY`\
the last KEY to sync

`--threads value, -p value`\
number of concurrent threads (default: 10)

`--http-port PORT`\
HTTP PORT to listen to (default: 6070)

`--update, -u`\
update existing file if the source is newer (default: false)

`--force-update, -f`\
always update existing file (default: false)

`--perms`\
preserve permissions (default: false)

`--dirs`\
Sync directories or holders (default: false)

`--dry`\
don't copy file (default: false)

`--delete-src, --deleteSrc`\
delete objects from source after synced (default: false)

`--delete-dst, --deleteDst`\
delete extraneous objects from destination (default: false)

`--exclude PATTERN`\
exclude keys containing PATTERN (POSIX regular expressions)

`--include PATTERN`\
only include keys containing PATTERN (POSIX regular expressions)

`--manager value`\
manager address

`--worker value`\
hosts (seperated by comma) to launch worker

`--bwlimit value`\
limit bandwidth in Mbps (0 means unlimited) (default: 0)

`--no-https`\
do not use HTTPS (default: false)

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

`--inode, -i`\
use inode instead of path (current dir should be inside JuiceFS) (default: false)


### juicefs bench

#### Description

Run benchmark, include read/write/stat big and small files.

#### Synopsis

```
juicefs bench [command options] PATH
```

#### Options

`--block-size value`\
block size in MiB (default: 1)

`--big-file-size value`\
size of big file in MiB (default: 1024)

`--small-file-size value`\
size of small file in MiB (default: 0.1)

`--small-file-count value`\
number of small files (default: 100)

`--threads value, -p value`\
number of concurrent threads (default: 1)

### juicefs gc

#### Description

Collect any leaked objects.

#### Synopsis

```
juicefs gc [command options] META-URL
```

#### Options

`--delete`\
deleted leaked objects (default: false)

`--compact`\
compact all chunks with more than 1 slices (default: false).

`--threads value`\
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

Analyze access log.

#### Synopsis

```
juicefs profile [command options] MOUNTPOINT/LOGFILE
```

#### Options

`--uid value, -u value`\
track only specified UIDs(separated by comma ,)

`--gid value, -g value`\
track only specified GIDs(separated by comma ,)

`--pid value, -p value`\
track only specified PIDs(separated by comma ,)

`--interval value`\
flush interval in seconds; set it to 0 when replaying a log file to get an immediate result (default: 2)

### juicefs stats

#### Description

show runtime stats.

#### Synopsis

```
juicefs stats [command options] MOUNTPOINT
```

#### Options

`--schema value`\
schema string that controls the output sections (u: usage, f: fuse, m: meta, c: blockcache, o: object, g: go) (default: "ufmco")

`--interval value`\
interval in seconds between each update (default: 1)

`--verbosity value`\
verbosity level, 0 or 1 is enough for most cases (default: 0)

`--nocolor`\
disable colors (default: false)

### juicefs status

#### Description

show status of JuiceFS

#### Synopsis

```
juicefs status [command options] META-URL
```

#### Options

`--session value, -s value`\
show detailed information (sustained inodes, locks) of the specified session (sid) (default: 0)

### juicefs warmup

#### Description

build cache for target directories/files

#### Synopsis

```
juicefs warmup [command options] [PATH ...]
```

#### Options

`--file value, -f value`\
file containing a list of paths

`--threads value, -p value`\
number of concurrent workers (default: 50)

`--background, -b`\
run in background (default: false)

### juicefs dump

#### Description

dump metadata into a JSON file

#### Synopsis

```
juicefs dump [command options] META-URL [FILE]
```

When the FILE is not provided, STDOUT will be used instead.

#### Options

`--subdir value`\
only dump a sub-directory.

### juicefs load

#### Description

load metadata from a previously dumped JSON file

#### Synopsis

```
juicefs load [command options] META-URL [FILE]
```

When the FILE is not provided, STDIN will be used instead.
