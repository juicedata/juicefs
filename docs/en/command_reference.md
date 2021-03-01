# Command Reference

There are many commands to help you manage your file system. This page provides a detailed reference for these commands.

## Overview

If you run `juicefs` by itself, it will print all available subcommands. In addition, you can add `-h/--help` flag after each subcommand to get more information of that subcommand.

```
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   0.10.0-62 (2021-03-01 64b83a8)

COMMANDS:
   format     format a volume
   mount      mount a volume
   umount     unmount a volume
   gateway    S3-compatible gateway
   sync       sync between two storage
   rmr        remove all files in a directory
   benchmark  run benchmark, including read/write/stat big/small files
   help, h    Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --debug, -v    enable debug log (default: false)
   --quiet, -q    only warning and errors (default: false)
   --trace        enable trace log (default: false)
   --help, -h     show help (default: false)
   --version, -V  print only the version (default: false)

COPYRIGHT:
   AGPLv3
```

Usage: `juicefs [global options] command [command options] [arguments...]`

Add `-h` or `--help` after all commands, getting arguments list and help information.

***Note:*** If `juicefs` is not placed in your `$PATH`, you should run the script with the path to the script. For example, if `juicefs` is placed in current directory, you should use `./juicefs`. It is recommended to place `juicefs` in your `$PATH` for the convenience.

The documentation below gives you detailed information about each subcommand.

## juicefs format

### Description

Format a volume. It's the first step for initializing a new file system volume.

### Synopsis

```
juicefs format [command options] REDIS-URL NAME
```

### Options

`--block-size value`\
size of block in KiB (default: 4096)

`--compress value`\
compression algorithm (lz4, zstd, none) (default: "lz4")

`--storage value`\
Object storage type (e.g. s3, gcs, oss, cos) (default: "file")

`--bucket value`\
A bucket URL to store data (default: `"$HOME/.juicefs/local"`)

`--access-key value`\
Access key for object storage (env `ACCESS_KEY`)

`--secret-key value`\
Secret key for object storage (env `SECRET_KEY`)

`--encrypt-rsa-key value`\
A path to RSA private key (PEM)

`--force`\
overwrite existing format (default: false)

## juicefs mount

### Description

Mount a volume. The volume shoud be formatted first.

### Synopsis

```
juicefs mount [command options] REDIS-URL MOUNTPOINT
```

### Options

`-d, --background`\
run in background (default: false)

`--no-syslog`\
disable syslog (default: false)

`-o value`\
other FUSE options

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

`--prefetch value`\
prefetch N blocks in parallel (default: 3)

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

`--no-usage-report`\
do not send usage report (default: false)

## juicefs umount

### Description

Unmount a volume.

### Synopsis

```
juicefs umount [options] MOUNTPOINT
```

### Options

`-f, --force`\
unmount a busy mount point by force (default: false)

## juicefs gateway

### Description

S3-compatible gateway.

### Synopsis

```
juicefs gateway [command options] REDIS-URL ADDRESS
```

### Options

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

`--prefetch value`\
prefetch N blocks in parallel (default: 3)

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

`--access-log value`\
path for JuiceFS access log

`--no-usage-report`\
do not send usage report (default: false)

`--no-banner`\
disable MinIO startup information (default: false)

## juicefs sync

### Description

Sync between two storage.

### Synopsis

```
juicefs sync [command options] SRC DST
```

### Options

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

## juicefs rmr

### Description

Remove all files in directories recursively.

### Synopsis

```
juicefs rmr PATH ...
```

## juicefs benchmark

### Description

Run benchmark, include read/write/stat big and small files.

### Synopsis

```
juicefs benchmark [options] DIR
```

### Options

`--dest value`\
path to run benchmark (default: `"/jfs/benchmark"`)

`--block-size value`\
block size in MiB (default: 1)

`--bigfile-file-size value`\
size of big file in MiB (default: 1024)

`--smallfile-file-size value`\
size of small file in MiB (default: 0.1)

`--smallfile-count value`\
number of small files (default: 100)
