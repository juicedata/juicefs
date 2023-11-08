---
title: Command Reference
sidebar_position: 1
slug: /command_reference
description: Descriptions, usage and examples of all commands and options included in JuiceFS Client.
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Running `juicefs` by itself and it will print all available commands. In addition, you can add `-h/--help` flag after each command to get more information, e.g., `juicefs format -h`.

```
NAME:
   juicefs - A POSIX file system built on Redis and object storage.

USAGE:
   juicefs [global options] command [command options] [arguments...]

VERSION:
   1.1.0

COMMANDS:
   ADMIN:
     format   Format a volume
     config   Change configuration of a volume
     quota    Manage directory quotas
     destroy  Destroy an existing volume
     gc       Garbage collector of objects in data storage
     fsck     Check consistency of a volume
     restore  restore files from trash
     dump     Dump metadata into a JSON file
     load     Load metadata from a previously dumped JSON file
     version  Show version
   INSPECTOR:
     status   Show status of a volume
     stats    Show real time performance statistics of JuiceFS
     profile  Show profiling of operations completed in JuiceFS
     info     Show internal information of a path or inode
     debug    Collect and display system static and runtime information
     summary  Show tree summary of a directory
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
     clone     clone a file or directory without copying the underlying data

GLOBAL OPTIONS:
   --verbose, --debug, -v  enable debug log (default: false)
   --quiet, -q             show warning and errors only (default: false)
   --trace                 enable trace log (default: false)
   --log-id value          append the given log id in log, use "random" to use random uuid
   --no-agent              disable pprof (:6060) agent (default: false)
   --pyroscope value       pyroscope address
   --no-color              disable colors (default: false)
   --help, -h              show help (default: false)
   --version, -V           print version only (default: false)

COPYRIGHT:
   Apache License 2.0
```

## Auto completion {#auto-completion}

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

```shell
cp hack/autocomplete/bash_autocomplete /etc/bash_completion.d/juicefs
source /etc/bash_completion.d/juicefs
```

## Admin {#admin}

### `juicefs format` {#format}

Create and format a file system, if a volume already exists with the same `META-URL`, this command will skip the format step. To adjust configurations for existing volumes, use [`juicefs config`](#config).

#### Synopsis

```shell
juicefs format [command options] META-URL NAME

# Create a simple test volume (data will be stored in a local directory)
juicefs format sqlite3://myjfs.db myjfs

# Create a volume with Redis and S3
juicefs format redis://localhost myjfs --storage=s3 --bucket=https://mybucket.s3.us-east-2.amazonaws.com

# Create a volume with password protected MySQL
juicefs format mysql://jfs:mypassword@(127.0.0.1:3306)/juicefs myjfs
# A safer alternative
META_PASSWORD=mypassword juicefs format mysql://jfs:@(127.0.0.1:3306)/juicefs myjfs

# Create a volume with quota enabled
juicefs format sqlite3://myjfs.db myjfs --inodes=1000000 --capacity=102400

# Create a volume with trash disabled
juicefs format sqlite3://myjfs.db myjfs --trash-days=0
```

#### Options

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`NAME`|Name of the file system|
|`--force`|overwrite existing format (default: false)|
|`--no-update`|don't update existing volume (default: false)|

#### Data storage options {#format-data-storage-options}

|Items|Description|
|-|-|
|`--storage=file`|Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `file`, refer to [documentation](../reference/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)|
|`--bucket=/var/jfs`|A bucket URL to store data (default: `$HOME/.juicefs/local` or `/var/jfs`)|
|`--access-key=value`|Access Key for object storage (can also be set via the environment variable `ACCESS_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--secret-key value`|Secret Key for object storage (can also be set via the environment variable `SECRET_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--session-token=value`|session token for object storage, see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#session-token) for more.|
|`--storage-class value` <VersionAdd>1.1</VersionAdd> |the default storage class|

#### Data format options {#format-data-format-options}

|Items|Description|
|-|-|
|`--block-size=4096`|size of block in KiB (default: 4096). 4M is usually a better default value because many object storage services use 4M as their internal block size, thus using the same block size in JuiceFS usually yields better performance.|
|`--compress=none`|compression algorithm, choose from `lz4`, `zstd`, `none` (default). Enabling compression will inevitably affect performance. Among the two supported algorithms, `lz4` offers a better performance, while `zstd` comes with a higher compression ratio, Google for their detailed comparison.|
|`--encrypt-rsa-key=value`|A path to RSA private key (PEM)|
|`--encrypt-algo=aes256gcm-rsa`|encrypt algorithm (aes256gcm-rsa, chacha20-rsa) (default: "aes256gcm-rsa")|
|`--hash-prefix`|For most object storages, if object storage blocks are sequentially named, they will also be closely stored in the underlying physical regions. When loaded with intensive concurrent consecutive reads, this can cause hotspots and hinder object storage performance.<br/><br/>Enabling `--hash-prefix` will add a hash prefix to name of the blocks (slice ID mod 256, see [internal implementation](../development/internals.md#object-storage-naming-format)), this distributes data blocks evenly across actual object storage regions, offering more consistent performance. Obviously, this option dictates object naming pattern and **should be specified when a file system is created, and cannot be changed on-the-fly.**<br/><br/>Currently, [AWS S3](https://aws.amazon.com/about-aws/whats-new/2018/07/amazon-s3-announces-increased-request-rate-performance) had already made improvements and no longer require application side optimization, but for other types of object storages, this option still recommended for large scale scenarios.|
|`--shards=0`|If your object storage limit speed in a bucket level (or you're using a self-hosted object storage with limited performance), you can store the blocks into N buckets by hash of key (default: 0), when N is greater than 0, `bucket` should to be in the form of `%d`, e.g. `--bucket "juicefs-%d"`. `--shards` cannot be changed afterwards and must be planned carefully ahead.|

#### Management options {#format-management-options}

|Items|Description|
|-|-|
|`--capacity=0`|storage space limit in GiB, default to 0 which means no limit. Capacity will include trash files, if [trash](../security/trash.md) is enabled.|
|`--inodes=0`|Limit the number of inodes, default to 0 which means no limit.|
|`--trash-days=1`|By default, delete files are put into [trash](../security/trash.md), this option controls the number of days before trash files are expired, default to 1, set to 0 to disable trash.|

### `juicefs config` {#config}

Change config of a volume. Note that after updating some settings, the client may not take effect immediately, and it needs to wait for a certain period of time. The specific waiting time can be controlled by the [`--heartbeat`](#mount-metadata-options) option.

#### Synopsis

```shell
juicefs config [command options] META-URL

# Show the current configurations
juicefs config redis://localhost

# Change volume "quota"
juicefs config redis://localhost --inodes 10000000 --capacity 1048576

# Change maximum days before files in trash are deleted
juicefs config redis://localhost --trash-days 7

# Limit client version that is allowed to connect
juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0
```

#### Options

|Items|Description|
|-|-|
|`--yes, -y`|automatically answer 'yes' to all prompts and run non-interactively (default: false)|
|`--force`|skip sanity check and force update the configurations (default: false)|

#### Data storage options {#config-data-storage-options}

|Items|Description|
|-|-|
|`--storage=file` <VersionAdd>1.1</VersionAdd> |Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, refer to [documentation](../reference/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types).|
|`--bucket=/var/jfs`|A bucket URL to store data (default: `$HOME/.juicefs/local` or `/var/jfs`)|
|`--access-key=value`|Access Key for object storage (can also be set via the environment variable `ACCESS_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--secret-key value`|Secret Key for object storage (can also be set via the environment variable `SECRET_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--session-token=value`|session token for object storage, see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#session-token) for more.|
|`--storage-class value` <VersionAdd>1.1</VersionAdd> |the default storage class|
|`--upload-limit=0`|bandwidth limit for upload in Mbps (default: 0)|
|`--download-limit=0`|bandwidth limit for download in Mbps (default: 0)|

#### Management options {#config-management-options}

|Items|Description|
|-|-|
|`--capacity value`|limit for space in GiB|
|`--inodes value`|limit for number of inodes|
|`--trash-days value`|number of days after which removed files will be permanently deleted|
|`--encrypt-secret`|encrypt the secret key if it was previously stored in plain format (default: false)|
|`--min-client-version value` <VersionAdd>1.1</VersionAdd> |minimum client version allowed to connect|
|`--max-client-version value` <VersionAdd>1.1</VersionAdd> |maximum client version allowed to connect|
|`--dir-stats` <VersionAdd>1.1</VersionAdd> |enable dir stats, which is necessary for fast summary and dir quota (default: false)|

### `juicefs quota` <VersionAdd>1.1</VersionAdd> {#quota}

Manage directory quotas

#### Synopsis

```shell
juicefs quota command [command options] META-URL

# Set quota to a directory
juicefs quota set redis://localhost --path /dir1 --capacity 1 --inodes 100

# Get quota of a directory
juicefs quota get redis://localhost --path /dir1

# List all directory quotas
juicefs quota list redis://localhost

# Delete quota of a directory
juicefs quota delete redis://localhost --path /dir1

# Check quota consistency of a directory
juicefs quota check redis://localhost
```

#### Options

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see "[JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md)" for details.|
|`--path value`|full path of the directory within the volume|
|`--capacity value`|hard quota of the directory limiting its usage of space in GiB (default: 0)|
|`--inodes value`|hard quota of the directory limiting its number of inodes (default: 0)|
|`--repair`|repair inconsistent quota (default: false)|
|`--strict`|calculate total usage of directory in strict mode (NOTE: may be slow for huge directory) (default: false)|

### `juicefs destroy` {#destroy}

Destroy an existing volume, will delete relevant data in metadata engine and object storage. See [How to destroy a file system](../administration/destroy.md).

#### Synopsis

```shell
juicefs destroy [command options] META-URL UUID

juicefs destroy redis://localhost e94d66a8-2339-4abd-b8d8-6812df737892
```

#### Options

|Items|Description|
|-|-|
|`--yes, -y` <VersionAdd>1.1</VersionAdd> |automatically answer 'yes' to all prompts and run non-interactively (default: false)|
|`--force`|skip sanity check and force destroy the volume (default: false)|

### `juicefs gc` {#gc}

If for some reason, a object storage block escape JuiceFS management completely, i.e. the metadata is gone, but the block still persists in the object storage, and cannot be released, this is called an "object leak". If this happens without any special file system manipulation, it could well indicate a bug within JuiceFS, file a [GitHub Issue](https://github.com/juicedata/juicefs/issues/new/choose) to let us know.

Meanwhile, you can run this command to deal with leaked objects. It also deletes stale slices produced by file overwrites. See [Status Check & Maintenance](../administration/status_check_and_maintenance.md#gc).

#### Synopsis

```shell
juicefs gc [command options] META-URL

# Check only, no writable change
juicefs gc redis://localhost

# Trigger compaction of all slices
juicefs gc redis://localhost --compact

# Delete leaked objects
juicefs gc redis://localhost --delete
```

#### Options

|Items|Description|
|-|-|
|`--delete`|delete leaked objects (default: false)|
|`--compact`|compact all chunks with more than 1 slices (default: false).|
|`--threads=10`|number of threads to delete leaked objects (default: 10)|

### `juicefs fsck` {#fsck}

Check consistency of file system.

#### Synopsis

```shell
juicefs fsck [command options] META-URL

juicefs fsck redis://localhost
```

#### Options

|Items|Description|
|-|-|
|`--path value` <VersionAdd>1.1</VersionAdd> |absolute path within JuiceFS to check|
|`--repair` <VersionAdd>1.1</VersionAdd> |repair specified path if it's broken (default: false)|
|`--recursive, -r` <VersionAdd>1.1</VersionAdd> |recursively check or repair (default: false)|
|`--sync-dir-stat` <VersionAdd>1.1</VersionAdd> |sync stat of all directories, even if they are existed and not broken (NOTE: it may take a long time for huge trees) (default: false)|

### `juicefs restore` <VersionAdd>1.1</VersionAdd> {#restore}

Rebuild the tree structure for trash files, and put them back to original directories.

#### Synopsis

```shell
juicefs restore [command options] META HOUR ...

juicefs restore redis://localhost/1 2023-05-10-01
```

#### Options

|Items|Description|
|-|-|
|`--put-back value`|move the recovered files into original directory (default: false)|
|`--threads value`|number of threads (default: 10)|

### `juicefs dump` {#dump}

Dump metadata into a JSON file. Refer to ["Metadata backup"](../administration/metadata_dump_load.md#backup) for more information.

#### Synopsis

```shell
juicefs dump [command options] META-URL [FILE]

# Export metadata to meta-dump.json
juicefs dump redis://localhost meta-dump.json

# Export metadata for only one subdirectory of the file system
juicefs dump redis://localhost sub-meta-dump.json --subdir /dir/in/jfs
```

#### Options

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`FILE`|Export file path, if not specified, it will be exported to standard output. If the filename ends with `.gz`, it will be automatically compressed.|
|`--subdir=path`|Only export metadata for the specified subdirectory.|
|`--keep-secret-key` <VersionAdd>1.1</VersionAdd> |Export object storage authentication information, the default is `false`. Since it is exported in plain text, pay attention to data security when using it. If the export file does not contain object storage authentication information, you need to use [`juicefs config`](#config) to reconfigure object storage authentication information after the subsequent import is completed.|

### `juicefs load` {#load}

Load metadata from a previously dumped JSON file. Read ["Metadata recovery and migration"](../administration/metadata_dump_load.md#recovery-and-migration) to learn more.

#### Synopsis

```shell
juicefs load [command options] META-URL [FILE]

# Import the metadata backup file meta-dump.json to the database
juicefs load redis://127.0.0.1:6379/1 meta-dump.json
```

#### Options

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`FILE`|Import file path, if not specified, it will be imported from standard input. If the filename ends with `.gz`, it will be automatically decompressed.|
|`--encrypt-rsa-key=path` <VersionAdd>1.0.4</VersionAdd> |The path to the RSA private key file used for encryption.|
|`--encrypt-alg=aes256gcm-rsa` <VersionAdd>1.0.4</VersionAdd> |Encryption algorithm, the default is `aes256gcm-rsa`.|

## Inspector {#inspector}

### `juicefs status` {#status}

Show status of JuiceFS.

#### Synopsis

```shell
juicefs status [command options] META-URL

juicefs status redis://localhost
```

#### Options

|Items|Description|
|-|-|
|`--session=0, -s 0`|show detailed information (sustained inodes, locks) of the specified session (SID) (default: 0)|
|`--more, -m` <VersionAdd>1.1</VersionAdd> |show more statistic information, may take a long time (default: false)|

### `juicefs stats` {#stats}

Show runtime statistics, read [Real-time performance monitoring](../administration/fault_diagnosis_and_analysis.md#performance-monitor) for more.

#### Synopsis

```shell
juicefs stats [command options] MOUNTPOINT

juicefs stats /mnt/jfs

# More metrics
juicefs stats /mnt/jfs -l 1
```

#### Options

|Items|Description|
|-|-|
|`--schema=ufmco`|schema string that controls the output sections (`u`: usage, `f`: FUSE, `m`: metadata, `c`: block cache, `o`: object storage, `g`: Go) (default: `ufmco`)|
|`--interval=1`|interval in seconds between each update (default: 1)|
|`--verbosity=0`|verbosity level, 0 or 1 is enough for most cases (default: 0)|

### `juicefs profile` {#profile}

Show profiling of operations completed in JuiceFS, based on [access log](../administration/fault_diagnosis_and_analysis.md#access-log). read [Real-time performance monitoring](../administration/fault_diagnosis_and_analysis.md#performance-monitor) for more.

#### Synopsis

```shell
juicefs profile [command options] MOUNTPOINT/LOGFILE

# Monitor real time operations
juicefs profile /mnt/jfs

# Replay an access log
cat /mnt/jfs/.accesslog > /tmp/jfs.alog
# Press Ctrl-C to stop the "cat" command after some time
juicefs profile /tmp/jfs.alog

# Analyze an access log and print the total statistics immediately
juicefs profile /tmp/jfs.alog --interval 0
```

#### Options

|Items|Description|
|-|-|
|`--uid=value, -u value`|only track specified UIDs (separated by comma)|
|`--gid=value, -g value`|only track specified GIDs (separated by comma)|
|`--pid=value, -p value`|only track specified PIDs (separated by comma)|
|`--interval=2`|flush interval in seconds; set it to 0 when replaying a log file to get an immediate result (default: 2)|

### `juicefs info` {#info}

Show internal information for given paths or inodes.

#### Synopsis

```shell
juicefs info [command options] PATH or INODE

# Check a path
juicefs info /mnt/jfs/foo

# Check an inode
cd /mnt/jfs
juicefs info -i 100
```

#### Options

|Items|Description|
|-|-|
|`--inode, -i`|use inode instead of path (current dir should be inside JuiceFS) (default: false)|
|`--recursive, -r`|get summary of directories recursively (NOTE: it may take a long time for huge trees) (default: false)|
|`--strict` <VersionAdd>1.1</VersionAdd> |get accurate summary of directories (NOTE: it may take a long time for huge trees) (default: false)|
|`--raw`|show internal raw information (default: false)|

### `juicefs debug` <VersionAdd>1.1</VersionAdd> {#debug}

It collects and displays information from multiple dimensions such as the operating environment and system logs to help better locate errors

#### Synopsis

```shell
juicefs debug [command options] MOUNTPOINT

# Collect and display information about the mount point /mnt/jfs
juicefs debug /mnt/jfs

# Specify the output directory as /var/log
juicefs debug --out-dir=/var/log /mnt/jfs

# Get the last up to 1000 log entries
juicefs debug --out-dir=/var/log --limit=1000 /mnt/jfs
```

#### Options

|Items|Description|
|-|-|
|`--out-dir=./debug/`|The output directory of the results, automatically created if the directory does not exist (default: `./debug/`)|
|`--stats-sec=5`|The number of seconds to sample .stats file (default: 5)|
|`--limit=value`|The number of log entries collected, from newest to oldest, if not specified, all entries will be collected|
|`--trace-sec=5`|The number of seconds to sample trace metrics (default: 5)|
|`--profile-sec=30`|The number of seconds to sample profile metrics (default: 30)|

### `juicefs summary` <VersionAdd>1.1</VersionAdd> {#summary}

It is used to show tree summary of target directory.

#### Synopsis

```shell
juicefs summary [command options] PATH

# Show with path
juicefs summary /mnt/jfs/foo

# Show max depth of 5
juicefs summary --depth 5 /mnt/jfs/foo

# Show top 20 entries
juicefs summary --entries 20 /mnt/jfs/foo

# Show accurate result
juicefs summary --strict /mnt/jfs/foo
```

#### Options

|Items|Description|
|-|-|
|`--depth value, -d value`|depth of tree to show (zero means only show root) (default: 2)|
|`--entries value, -e value`|show top N entries (sort by size) (default: 10)|
|`--strict`|show accurate summary, including directories and files (may be slow) (default: false)|
|`--csv`|print summary in csv format (default: false)|

## Service {#service}

### `juicefs mount` {#mount}

Mount a volume. The volume must be formatted in advance.

JuiceFS can be mounted by root or normal user, but due to their privilege differences, cache directory and log path will vary, read below descriptions for more.

#### Synopsis

```shell
juicefs mount [command options] META-URL MOUNTPOINT

# Mount in foreground
juicefs mount redis://localhost /mnt/jfs

# Mount in background with password protected Redis
juicefs mount redis://:mypassword@localhost /mnt/jfs -d
# A safer alternative
META_PASSWORD=mypassword juicefs mount redis://localhost /mnt/jfs -d

# Mount with a sub-directory as root
juicefs mount redis://localhost /mnt/jfs --subdir /dir/in/jfs

# Enable "writeback" mode, which improves performance at the risk of losing objects
juicefs mount redis://localhost /mnt/jfs -d --writeback

# Enable "read-only" mode
juicefs mount redis://localhost /mnt/jfs -d --read-only

# Disable metadata backup
juicefs mount redis://localhost /mnt/jfs --backup-meta 0
```

#### Options

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`MOUNTPOINT`|file system mount point, e.g. `/mnt/jfs`, `Z:`.|
|`-d, --background`|run in background (default: false)|
|`--no-syslog`|disable syslog (default: false)|
|`--log=path`|path of log file when running in background (default: `$HOME/.juicefs/juicefs.log` or `/var/log/juicefs.log`)|
|`--update-fstab` <VersionAdd>1.1</VersionAdd> |add / update entry in `/etc/fstab`, will create a symlink from `/sbin/mount.juicefs` to JuiceFS executable if not existing (default: false)|

#### FUSE related options {#mount-fuse-options}

|Items|Description|
|-|-|
|`--enable-xattr`|enable extended attributes (xattr) (default: false)|
|`--enable-ioctl` <VersionAdd>1.1</VersionAdd> |enable ioctl (support GETFLAGS/SETFLAGS only) (default: false)|
|`--root-squash value` <VersionAdd>1.1</VersionAdd> |mapping local root user (UID = 0) to another one specified as UID:GID|
|`--prefix-internal` <VersionAdd>1.1</VersionAdd> |add '.jfs' prefix to all internal files (default: false)|
|`-o value`|other FUSE options, see [FUSE Mount Options](../reference/fuse_mount_options.md)|

#### Metadata related options {#mount-metadata-options}

|Items|Description|
|-|-|
|`--subdir=value`|mount a sub-directory as root (default: "")|
|`--backup-meta=3600`|interval (in seconds) to automatically backup metadata in the object storage (0 means disable backup) (default: "3600")|
|`--heartbeat=12`|interval (in seconds) to send heartbeat; it's recommended that all clients use the same heartbeat value (default: "12")|
|`--read-only`|allow lookup/read operations only (default: false)|
|`--no-bgjob`|Disable background jobs, default to false, which means clients by default carry out background jobs, including:<br/><ul><li>Clean up expired files in Trash (look for `cleanupDeletedFiles`, `cleanupTrash` in [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go))</li><li>Delete slices that's not referenced (look for `cleanupSlices` in [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go))</li><li>Clean up stale client sessions (look for `CleanStaleSessions` in [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/base.go))</li></ul>Note that compaction isn't affected by this option, it happens automatically with file reads and writes, client will check if compaction is in need, and run in background (take Redis for example, look for `compactChunk` in [`pkg/meta/base.go`](https://github.com/juicedata/juicefs/blob/main/pkg/meta/redis.go)).|
|`--atime-mode=noatime` <VersionAdd>1.1</VersionAdd> |Control atime (last time the file was accessed) behavior, support the following modes:<br/><ul><li>`noatime` (default): set when the file is created or when `SetAttr` is explicitly called. Accessing and modifying the file will not affect atime, tracking atime comes at a performance cost, so this is the default behavior</li><li>`relatime`: update inode access times relative to mtime (last time when the file data was modified) or ctime (last time when file metadata was changed). Only update atime if atime was earlier than the current mtime or ctime, or the file's atime is more than 1 day old</li><li>`strictatime`: always update atime on access</li></ul>|
|`--skip-dir-nlink value` <VersionAdd>1.1</VersionAdd> |number of retries after which the update of directory nlink will be skipped (used for tkv only, 0 means never) (default: 20)|

#### Metadata cache related options {#mount-metadata-cache-options}

For metadata cache description and usage, refer to [Kernel metadata cache](../guide/cache.md#kernel-metadata-cache) and [Client memory metadata cache](../guide/cache.md#client-memory-metadata-cache).

|Items|Description|
|-|-|
|`--attr-cache=1`|attributes cache timeout in seconds (default: 1), read [Kernel metadata cache](../guide/cache.md#kernel-metadata-cache)|
|`--entry-cache=1`|file entry cache timeout in seconds (default: 1), read [Kernel metadata cache](../guide/cache.md#kernel-metadata-cache)|
|`--dir-entry-cache=1`|dir entry cache timeout in seconds (default: 1), read [Kernel metadata cache](../guide/cache.md#kernel-metadata-cache)|
|`--open-cache=0`|open file cache timeout in seconds (0 means disable this feature) (default: 0)|
|`--open-cache-limit value` <VersionAdd>1.1</VersionAdd> |max number of open files to cache (soft limit, 0 means unlimited) (default: 10000)|

#### Data storage related options {#mount-data-storage-options}

|Items|Description|
|-|-|
|`--storage=file`|Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `"file"`, refer to [documentation](../reference/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types).|
|`--storage-class value` <VersionAdd>1.1</VersionAdd> |the storage class for data written by current client|
|`--bucket=value`|customized endpoint to access object storage|
|`--get-timeout=60`|the max number of seconds to download an object (default: 60)|
|`--put-timeout=60`|the max number of seconds to upload an object (default: 60)|
|`--io-retries=10`|number of retries after network failure (default: 10)|
|`--max-uploads=20`|Upload concurrency, defaults to 20. This is already a reasonably high value for 4M writes, with such write pattern, increasing upload concurrency usually demands higher `--buffer-size`, learn more at [Read/Write Buffer](../guide/cache.md#buffer-size). But for random writes around 100K, 20 might not be enough and can cause congestion at high load, consider using a larger upload concurrency, or try to consolidate small writes in the application end. |
|`--max-deletes=10`|number of threads to delete objects (default: 10)|
|`--upload-limit=0`|bandwidth limit for upload in Mbps (default: 0)|
|`--download-limit=0`|bandwidth limit for download in Mbps (default: 0)|

#### Data cache related options {#mount-data-cache-options}

|Items|Description|
|-|-|
|`--buffer-size=300`|total read/write buffering in MiB (default: 300), see [Read/Write buffer](../guide/cache.md#buffer-size)|
|`--prefetch=1`|prefetch N blocks in parallel (default: 1), see [Client read data cache](../guide/cache.md#client-read-cache)|
|`--writeback`|upload objects in background (default: false), see [Client write data cache](../guide/cache.md#client-write-cache)|
|`--upload-delay=0`|When `--writeback` is enabled, you can use this option to add a delay to object storage upload, default to 0, meaning that upload will begin immediately after write. Different units are supported, including `s` (second), `m` (minute), `h` (hour). If files are deleted during this delay, upload will be skipped entirely, when using JuiceFS for temporary storage, use this option to reduce resource usage. Refer to [Client write data cache](../guide/cache.md#client-write-cache).|
|`--cache-dir=value`|directory paths of local cache, use `:` (Linux, macOS) or `;` (Windows) to separate multiple paths (default: `$HOME/.juicefs/cache` or `/var/jfsCache`), see [Client read data cache](../guide/cache.md#client-read-cache)|
|`--cache-mode value` <VersionAdd>1.1</VersionAdd> |file permissions for cached blocks (default: "0600")|
|`--cache-size=102400`|size of cached object for read in MiB (default: 102400), see [Client read data cache](../guide/cache.md#client-read-cache)|
|`--free-space-ratio=0.1`|min free space ratio (default: 0.1), if [Client write data cache](../guide/cache.md#client-write-cache) is enabled, this option also controls write cache size, see [Client read data cache](../guide/cache.md#client-read-cache)|
|`--cache-partial-only`|cache random/small read only (default: false), see [Client read data cache](../guide/cache.md#client-read-cache)|
|`--verify-cache-checksum value` <VersionAdd>1.1</VersionAdd> |Checksum level for cache data. After enabled, checksum will be calculated on divided parts of the cache blocks and stored on disks, which are used for verification during reads. The following strategies are supported:<br/><ul><li>`none`: Disable checksum verification, if local cache data is tampered, bad data will be read;</li><li>`full` (default): Perform verification when reading the full block, use this for sequential read scenarios;</li><li>`shrink`: Perform verification on parts that's fully included within the read range, use this for random read scenarios;</li><li>`extend`: Perform verification on parts that fully include the read range, this causes read amplifications and is only used for random read scenarios demanding absolute data integrity.</li></ul>|
|`--cache-eviction value` <VersionAdd>1.1</VersionAdd> |cache eviction policy (none or 2-random) (default: "2-random")|
|`--cache-scan-interval value` <VersionAdd>1.1</VersionAdd> |interval (in seconds) to scan cache-dir to rebuild in-memory index (default: "3600")|

#### Metrics related options {#mount-metrics-options}

||Items|Description|
|-|-|
|`--metrics=127.0.0.1:9567`|address to export metrics (default: `127.0.0.1:9567`)|
|`--consul=127.0.0.1:8500`|Consul address to register (default: `127.0.0.1:8500`)|
|`--no-usage-report`|do not send usage report (default: false)|

### `juicefs umount` {#umount}

Unmount a volume.

#### Synopsis

```shell
juicefs umount [command options] MOUNTPOINT

juicefs umount /mnt/jfs
```

#### Options

|Items|Description|
|-|-|
|`-f, --force`|force unmount a busy mount point (default: false)|
|`--flush` <VersionAdd>1.1</VersionAdd> |wait for all staging chunks to be flushed (default: false)|

### `juicefs gateway` {#gateway}

Start an S3-compatible gateway, read [Deploy JuiceFS S3 Gateway](../deployment/s3_gateway.md) for more.

#### Synopsis

```shell
juicefs gateway [command options] META-URL ADDRESS

export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=12345678
juicefs gateway redis://localhost localhost:9000
```

#### Options

Apart from options listed below, this command shares options with `juicefs mount`, be sure to refer to [`mount`](#mount) as well.

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`ADDRESS`|S3 gateway address and listening port, for example: `localhost:9000`|
|`--access-log=path`|path for JuiceFS access log.|
|`--no-banner`|disable MinIO startup information (default: false)|
|`--multi-buckets`|use top level of directories as buckets (default: false)|
|`--keep-etag`|save the ETag for uploaded objects (default: false)|
|`--umask=022`|umask for new file and directory in octal (default: 022)|

### `juicefs webdav` {#webdav}

Start a WebDAV server, refer to [Deploy WebDAV Server](../deployment/webdav.md) for more.

#### Synopsis

```shell
juicefs webdav [command options] META-URL ADDRESS

juicefs webdav redis://localhost localhost:9007
```

#### Options

Apart from options listed below, this command shares options with `juicefs mount`, be sure to refer to [`mount`](#mount) as well.

|Items|Description|
|-|-|
|`META-URL`|Database URL for metadata storage, see [JuiceFS supported metadata engines](../reference/how_to_set_up_metadata_engine.md) for details.|
|`ADDRESS`|WebDAV address and listening port, for example: `localhost:9007`.|
|`--cert-file` <VersionAdd>1.1</VersionAdd> |certificate file for HTTPS|
|`--key-file` <VersionAdd>1.1</VersionAdd> |key file for HTTPS|
|`--gzip`|compress served files via gzip (default: false)|
|`--disallowList`|disallow list a directory (default: false)|
|`--access-log=path`|path for JuiceFS access log.|

## Tool {#tool}

### `juicefs bench` {#bench}

Run benchmark, including read/write/stat for big and small files.
For a detailed introduction to the `bench` subcommand, refer to the [documentation](../benchmark/performance_evaluation_guide.md#juicefs-bench).

#### Synopsis

```shell
juicefs bench [command options] PATH

# Run benchmarks with 4 threads
juicefs bench /mnt/jfs -p 4

# Run benchmarks of only small files
juicefs bench /mnt/jfs --big-file-size 0
```

#### Options

|Items|Description|
|-|-|
|`--block-size=1`|block size in MiB (default: 1)|
|`--big-file-size=1024`|size of big file in MiB (default: 1024)|
|`--small-file-size=0.1`|size of small file in MiB (default: 0.1)|
|`--small-file-count=100`|number of small files (default: 100)|
|`--threads=1, -p 1`|number of concurrent threads (default: 1)|

### `juicefs objbench` {#objbench}

Run basic benchmarks on the target object storage to test if it works as expected. Read [documentation](../benchmark/performance_evaluation_guide.md#juicefs-objbench) for more.

#### Synopsis

```shell
juicefs objbench [command options] BUCKET

# Run benchmarks on S3
ACCESS_KEY=myAccessKey SECRET_KEY=mySecretKey juicefs objbench --storage=s3 https://mybucket.s3.us-east-2.amazonaws.com -p 6
```

#### Options

|Items|Description|
|-|-|
|`--storage=file`|Object storage type (e.g. `s3`, `gcs`, `oss`, `cos`) (default: `file`, refer to [documentation](../reference/how_to_set_up_object_storage.md#supported-object-storage) for all supported object storage types)|
|`--access-key=value`|Access Key for object storage (can also be set via the environment variable `ACCESS_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--secret-key value`|Secret Key for object storage (can also be set via the environment variable `SECRET_KEY`), see [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md#aksk) for more.|
|`--block-size=4096`|size of each IO block in KiB (default: 4096)|
|`--big-object-size=1024`|size of each big object in MiB (default: 1024)|
|`--small-object-size=128`|size of each small object in KiB (default: 128)|
|`--small-objects=100`|number of small objects (default: 100)|
|`--skip-functional-tests`|skip functional tests (default: false)|
|`--threads=4, -p 4`|number of concurrent threads (default: 4)|

### `juicefs warmup` {#warmup}

Download data to local cache in advance, to achieve better performance on application's first read. You can specify a mount point path to recursively warm-up all files under this path. You can also specify a file through the `--file` option to only warm-up the files contained in it.

If the files needing warming up resides in many different directories, you should specify their names in a text file, and pass to the `warmup` command using the `--file` option, allowing `juicefs warmup` to download concurrently, which is significantly faster than calling `juicefs warmup` multiple times, each with a single file.

#### Synopsis

```shell
juicefs warmup [command options] [PATH ...]

# Warm up all files in datadir
juicefs warmup /mnt/jfs/datadir

# Warm up selected files
echo '/jfs/f1
/jfs/f2
/jfs/f3' > /tmp/filelist.txt
juicefs warmup -f /tmp/filelist.txt
```

#### Options

|Items|Description|
|-|-|
|`--file=path, -f path`|file containing a list of paths (each line is a file path)|
|`--threads=50, -p 50`|number of concurrent workers, default to 50. Reduce this number in low bandwidth environment to avoid download timeouts|
|`--background, -b`|run in background (default: false)|

### `juicefs rmr` {#rmr}

Remove all the files and subdirectories, similar to `rm -rf`, except this command deals with metadata directly (bypassing kernel), thus is much faster.

If trash is enabled, deleted files are moved into trash. Read more at [Trash](../security/trash.md).

#### Synopsis

```shell
juicefs rmr PATH ...

juicefs rmr /mnt/jfs/foo
```

### `juicefs sync` {#sync}

Sync between two storage, read [Data migration](../guide/sync.md) for more.

#### Synopsis

```shell
juicefs sync [command options] SRC DST

# Sync object from OSS to S3
juicefs sync oss://mybucket.oss-cn-shanghai.aliyuncs.com s3://mybucket.s3.us-east-2.amazonaws.com

# Sync objects from S3 to JuiceFS
juicefs sync s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# SRC: a1/b1,a2/b2,aaa/b1   DST: empty   sync result: aaa/b1
juicefs sync --exclude='a?/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# SRC: a1/b1,a2/b2,aaa/b1   DST: empty   sync result: a1/b1,aaa/b1
juicefs sync --include='a1/b1' --exclude='a[1-9]/b*' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/

# SRC: a1/b1,a2/b2,aaa/b1,b1,b2  DST: empty   sync result: a1/b1,b2
juicefs sync --include='a1/b1' --exclude='a*' --include='b2' --exclude='b?' s3://mybucket.s3.us-east-2.amazonaws.com/ jfs://META-URL/
```

As shown in the examples, the format of both source (`SRC`) and destination (`DST`) paths is:

```
[NAME://][ACCESS_KEY:SECRET_KEY[:TOKEN]@]BUCKET[.ENDPOINT][/PREFIX]
```

In which:

- `NAME`: JuiceFS supported data storage types like `s3`, `oss`, refer to [this document](../reference/how_to_set_up_object_storage.md#supported-object-storage) for a full list.
- `ACCESS_KEY` and `SECRET_KEY`: The credential required to access the data storage, refer to [this document](../reference/how_to_set_up_object_storage.md#aksk).
- `TOKEN` token used to access the object storage, as some object storage supports the use of temporary token to obtain permission for a limited time
- `BUCKET[.ENDPOINT]`: The access address of the data storage service. The format may be different for different storage types, and refer to [the document](../reference/how_to_set_up_object_storage.md#supported-object-storage).
- `[/PREFIX]`: Optional, a prefix for the source and destination paths that can be used to limit synchronization of data only in certain paths.

#### Selection related options {#sync-selection-related-options}

|Items|Description|
|-|-|
|`--start=KEY, -s KEY, --end=KEY, -e KEY`|Provide object storage key range for syncing.|
|`--exclude=PATTERN`|Exclude keys matching PATTERN.|
|`--include=PATTERN`|Include keys matching PATTERN, need to be used with `--exclude`.|
|`--limit=-1`|Limit the number of objects that will be processed, default to -1 which means unlimited.|
|`--update, -u`|Update existing files if the source files' `mtime` is newer, default to false.|
|`--force-update, -f`|Always update existing file, default to false.|
|`--existing, --ignore-non-existing` <VersionAdd>1.1</VersionAdd> |Skip creating new files on destination, default to false.|
|`--ignore-existing` <VersionAdd>1.1</VersionAdd> |Skip updating files that already exist on destination, default to false.|

#### Action related options {#sync-action-related-options}

|Items|Description|
|-|-|
|`--dirs`|Sync empty directories as well.|
|`--perms`|Preserve permissions, default to false.|
|`--links, -l`|Copy symlinks as symlinks default to false.|
|`--delete-src, --deleteSrc`|Delete objects that already exist in destination. Different from rsync, files won't be deleted at the first run, instead they will be deleted at the next run, after files are successfully copied to the destination.|
|`--delete-dst, --deleteDst`|Delete extraneous objects from destination.|
|`--check-all`|Verify the integrity of all files in source and destination, default to false. Comparison is done on byte streams, which comes at a performance cost.|
|`--check-new`|Verify the integrity of newly copied files, default to false. Comparison is done on byte streams, which comes at a performance cost.|
|`--dry`|Don't actually copy any file.|

#### Storage related options {#sync-storage-related-options}

|Items|Description|
|-|-|
|`--threads=10, -p 10`|Number of concurrent threads, default to 10.|
|`--list-threads=1` <VersionAdd>1.1</VersionAdd> |Number of `list` threads, default to 1. Read [concurrent `list`](../guide/sync.md#concurrent-list) to learn its usage.|
|`--list-depth=1` <VersionAdd>1.1</VersionAdd> |Depth of concurrent `list` operation, default to 1. Read [concurrent `list`](../guide/sync.md#concurrent-list) to learn its usage.|
|`--no-https`|Do not use HTTPS, default to false.|
|`--storage-class value` <VersionAdd>1.1</VersionAdd> |the storage class for destination|
|`--bwlimit=0`|Limit bandwidth in Mbps default to 0 which means unlimited.|

#### Cluster related options {#sync-cluster-related-options}

|Items| Description                                                                                                                                                                                                                       |
|-|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|`--manager-addr=ADDR`| The listening address of the Manager node in distributed synchronization mode in the format: `<IP>:[port]`. If not specified, it listens on a random port. If this option is omitted, it listens on a random local IPv4 address and a random port. |
|`--worker=ADDR,ADDR`| Worker node addresses used in distributed syncing, comma separated.                                                                                                                                                               |

### `juicefs clone` <VersionAdd>1.1</VersionAdd> {#clone}

Quickly clone directories or files within a single JuiceFS mount point. The cloning process involves copying only the metadata without copying the data blocks, making it extremely fast. Read [Clone Files or Directories](../guide/clone.md) for more.

#### Synopsis

```shell
juicefs clone [command options] SRC DST

# Clone a file
juicefs clone /mnt/jfs/file1 /mnt/jfs/file2

# Clone a directory
juicefs clone /mnt/jfs/dir1 /mnt/jfs/dir2

# Preserve the UID, GID, and mode of the file
juicefs clone -p /mnt/jfs/file1 /mnt/jfs/file2
```

#### Options

|Items|Description|
|-|-|
|`--preserve, -p`|By default, the executor's UID and GID are used for the clone result, and the mode is recalculated based on the user's umask. Use this option to preserve the UID, GID, and mode of the file.|
