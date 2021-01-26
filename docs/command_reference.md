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
   0.9.3-27 (2021-01-26 c516f3c)

COMMANDS:
   format     format a volume
   mount      mount a volume
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

Usage: `juicefs [command] [command options] [arguments ...]`

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
Upload objects in background (default: false)

`--cache-dir value`\
directory to cache object (default: `"$HOME/.juicefs/cache"`)

`--cache-size value`\
size of cached objects in MiB (default: 1024)

`--free-space-ratio value`\
min free space (ratio) (default: 0.1)

`--cache-partial-only`\
cache only random/small read (default: false)

`--no-usage-report`\
do not send usage report (default: false)

## juicefs benchmark

### Description

Run benchmark, include read/write/stat big and small files.

### Synopsis

```
juicefs benchmark [command options] [arguments...]
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
