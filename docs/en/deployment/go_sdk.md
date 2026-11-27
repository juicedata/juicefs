---
title: Go SDK
sidebar_position: 6
description: Learn how to use the JuiceFS Go SDK to access files directly from Go applications without FUSE, configure the client, run benchmarks, and understand API behavior and limitations.
---

The Go SDK lets Go programs access a JuiceFS volume directly through the metadata engine and object storage, without a FUSE mount. It suits containerized environments where FUSE is not available, and Go services that want to read and write JuiceFS in process.

The API follows the Go standard library: a `Client` is a handle to one volume modeled on [`os.Root`](https://pkg.go.dev/os#Root), a `File` mirrors [`os.File`](https://pkg.go.dev/os#File), and `Client.FS()` returns an [`io/fs`](https://pkg.go.dev/io/fs) file system. The supported operations and differences from the standard library are described below.

## Quick start

Use Go 1.25.10 or later, with cgo enabled and a C compiler installed. The SDK's import path is `github.com/juicedata/juicefs/sdk/go`, and its package name is `juicefs`.

If you are starting a new project, run `go mod init example.com/myapp`. Set `JUICEFS_VERSION` to a tag or commit that contains the SDK. The following command prints the path to that version's `go.mod`.

```shell
JUICEFS_VERSION='your-tag-or-commit'
go list -m -f '{{.GoMod}}' "github.com/juicedata/juicefs@${JUICEFS_VERSION}"
```

A standalone Go build applies the main module's `replace` directives. Copy the `replace` directives from that file into your project's `go.mod` to use the same dependency forks as JuiceFS. If your project already replaces any of those modules, resolve the conflicts before building.

Save the example below as `main.go`, and then use the same `JUICEFS_VERSION` to add the SDK dependency and build the example from your project directory.

```shell
go get "github.com/juicedata/juicefs/sdk/go@${JUICEFS_VERSION}"
go build -tags nogspt ./...
```

The `nogspt` build tag prevents a JuiceFS dependency from modifying the process command line. Create a volume with [`juicefs format`](../reference/command_reference.mdx#format) before connecting.

```go
package main

import (
    "fmt"
    "log"

    juicefs "github.com/juicedata/juicefs/sdk/go"
)

func main() {
    c, err := juicefs.New("myjfs", "redis://localhost:6379/1", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()

    if err := c.MkdirAll("/data", 0755); err != nil {
        log.Fatal(err)
    }
    if err := c.WriteFile("/data/hello.txt", []byte("hello\n"), 0644); err != nil {
        log.Fatal(err)
    }
    data, err := c.ReadFile("/data/hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%s", data)

    entries, err := c.ReadDir("/data")
    if err != nil {
        log.Fatal(err)
    }
    for _, e := range entries {
        fmt.Println(e.Name(), e.IsDir())
    }
}
```

All methods of a `Client` are safe for concurrent use; create one `Client` per volume and share it. Paths are relative to the root of the volume, with or without a leading `/`. `Close` closes files that remain open, flushes buffered writes, and ends the session. Finish ongoing operations before closing the client, and check the returned error.

More runnable programs, including an HTTP file server built on `Client.FS()`, are in [`sdk/go/examples`](https://github.com/juicedata/juicefs/tree/main/sdk/go/examples).

## Configuration

```go
func New(name, metaURL string, conf *Config) (*Client, error)
func DefaultConfig() *Config
```

- `name` is used only in metric labels and logs. The SDK compares it with the actual volume name. If they do not match, it logs a warning but still connects.
- `metaURL` specifies the metadata engine URL, using the same format as [`juicefs mount`](../reference/command_reference.mdx#mount).
- If `conf` is `nil`, `DefaultConfig()` is used. To change options, start with `DefaultConfig()`: zero-valued fields in `Config` are treated as zero, not as their default values.

```go
conf := juicefs.DefaultConfig()
conf.CacheDir = "/var/jfsCache"
conf.CacheSize = 10 << 30
conf.MaxReadahead = 32 << 20 // enable readahead for sequential reads
c, err := juicefs.New("myjfs", metaURL, conf)
```

The table lists the defaults returned by `DefaultConfig()`. Sizes are in bytes and durations are `time.Duration`.

| Field | Default | Description |
| --- | --- | --- |
| `Bucket` | `""` | Endpoint of the object storage, overriding the endpoint configured for the volume |
| `StorageClass` | `""` | Storage class of the data written by this client |
| `ReadOnly` | `false` | Allow only read operations |
| `NoSession` | `false` | Do not register a session in the metadata engine; implies `NoBGJob` and disables the metadata backup and the usage report |
| `NoBGJob` | `true` | Disable background jobs such as cleanup and metadata backup |
| `OpenCache` | `0` | How long to reuse cached attributes when a file is reopened; `0` disables this cache |
| `BackupMeta` | `1h` | Interval between automatic metadata backups to object storage; `0` disables backups. Requires `NoBGJob` to be false |
| `BackupSkipTrash` | `false` | Skip the files in the trash when backing up the metadata |
| `Heartbeat` | `12s` | Heartbeat interval for the metadata engine |
| `CacheDir` | `"memory"` | Local cache directories separated by colons; `"memory"` caches in memory |
| `CacheSize` | 100 MiB | Size of the read cache |
| `FreeSpaceRatio` | `0.1` | Minimum free-space ratio for the cache disk |
| `CachePartialOnly` | `false` | Cache data only for random and small reads |
| `VerifyCacheChecksum` | `"extend"` | Checksum verification level for cached data: `none`, `full`, `shrink`, or `extend` |
| `CacheEviction` | `"2-random"` | Eviction policy of the cache: `none`, `2-random`, or `lru` |
| `CacheScanInterval` | `1h` | Interval to scan the cache directories and rebuild the index |
| `CacheExpire` | `0` | Evict cached blocks that have not been accessed within this period; `0` means they never expire |
| `Writeback` | `false` | Upload the blocks in the background after writing them to the cache disk; no effect with `CacheDir="memory"` |
| `BufferSize` | 300 MiB | Total size of the read and write buffers |
| `Prefetch` | `1` | Number of blocks prefetched concurrently |
| `MaxReadahead` | `0` | Maximum amount of data to read ahead per read session; `0` disables readahead. See below |
| `UploadLimit` | `0` | Upload bandwidth limit in Mbps; `0` uses the limit of the volume |
| `DownloadLimit` | `0` | Download bandwidth limit in Mbps; `0` uses the limit of the volume |
| `MaxUploads` | `20` | Number of concurrent uploads |
| `MaxDeletes` | `10` | Number of concurrent deletions of objects, also used by `RemoveAll` |
| `SkipDirNlink` | `20` | Number of retries for conflicting transactions before directory nlink updates are skipped (TKV engines only); `0` means updates are never skipped |
| `SkipDirMtime` | `100ms` | Do not update the mtime of a directory if it changes less than this |
| `IORetries` | `10` | Number of retries after a network failure |
| `GetTimeout` | `5s` | Timeout for downloading an object |
| `PutTimeout` | `60s` | Timeout to upload an object |
| `FastResolve` | `false` | Resolve a whole path in one request (Redis only) |
| `AttrCache` | `1s` | How long the attributes of files are cached |
| `EntryCache` | `0` | How long the entries of files are cached |
| `DirEntryCache` | `1s` | How long the entries of directories are cached |
| `Debug` | `false` | Enable debug logs |
| `NoUsageReport` | `false` | Do not report anonymous usage data such as the version |
| `AccessLog` | `""` | Path of the access log; an empty value disables logging |
| `PushGateway` | `""` | Address of a Prometheus Pushgateway, `<host>:<port>` |
| `PushInterval` | `10s` | Interval between metric pushes |
| `PushAuth` | `""` | Pushgateway basic authentication credentials, in the `<username>:<password>` format |
| `PushLabels` | `""` | Extra labels of the pushed metrics, `key1:value1;key2:value2` |

### Readahead

Readahead is disabled by default (`MaxReadahead` is `0`). For sequential reads of large files, set `MaxReadahead` to an appropriate value, such as eight times the volume's block size (32 MiB for the default 4 MiB block size). Readahead also requires space in `BufferSize` and uses at most 80% of it.

### Identity and umask

Operations run under the effective user and group identities captured when `New` is called, and permissions are checked according to POSIX rules. The root user (euid 0) is the superuser. `New` reads the umask of the process once and applies it to new files and directories.

### Logs

Logs of JuiceFS are global to the process and shared by all clients. The log level is `debug` if `Config.Debug` is set or the environment variable `JUICEFS_DEBUG` is not empty. Otherwise, it is determined by `JUICEFS_LOGLEVEL`, which defaults to `warning`.

## API

### Files and directories

`Client` provides the following operations, modeled on `os.Root` and the `os` package. `Rmdir` is an additional method that only removes directories. See the known limitations below for differences in behavior:

| Group | Methods |
| --- | --- |
| Files | `Open`, `Create`, `OpenFile`, `ReadFile`, `WriteFile`, `Truncate` |
| Directories | `Mkdir`, `MkdirAll`, `ReadDir`, `Remove`, `RemoveAll`, `Rmdir` |
| Names | `Rename`, `Link`, `Symlink`, `Readlink` |
| Attributes | `Stat`, `Lstat`, `Chmod`, `Chown`, `Lchown`, `Chtimes` |

`*File` provides common `*os.File` operations, including `Read`, `ReadAt`, `Write`, `WriteAt`, `Seek`, `Stat`, `Sync`, `Truncate`, `Chmod`, `Chown`, `ReadDir`, `Readdir`, `Readdirnames` and `Close`, and implements `io.Reader`, `io.Writer`, `io.Seeker`, `io.ReaderAt`, `io.WriterAt` and `fs.ReadDirFile`. `OpenFile` supports the flags `O_RDONLY`, `O_WRONLY`, `O_RDWR`, `O_CREATE`, `O_EXCL`, `O_TRUNC` and `O_APPEND`; other flags return `EINVAL`.

The `Sys` method of the `fs.FileInfo` returned by `Stat`, `Lstat` and `File.Stat` returns a `*juicefs.Stat` containing the inode, the owner, the link count, and the timestamps.

File system errors are generally `*fs.PathError` or `*os.LinkError` wrapping a `syscall.Errno`. Methods such as `ListQuota`, `Warmup`, `Status`, and `SessionInfo`, which do not operate on a single path, return `*os.SyscallError`. Use `errors.Is` to check these errors, for example, `errors.Is(err, fs.ErrNotExist)` or `errors.Is(err, syscall.ENOTDIR)`. After `Client.Close`, file system operations generally return errors wrapping `fs.ErrClosed`. `Name`, `FS` and `Gatherer` remain callable; repeated `Client.Close` and `RemoveAll("")` return nil.

### io/fs

`Client.FS()` returns an `fs.FS` view of the volume. It opens files in read-only mode, but this does not prevent all modification operations through the underlying `*File`. Set `Config.ReadOnly` to create a read-only client. It also implements `fs.StatFS`, `fs.ReadDirFS`, `fs.ReadFileFS` and `fs.ReadLinkFS`. Paths passed to this file system must satisfy `fs.ValidPath`: they must not start with `/`, and `"."` represents the root of the volume.

```go
fsys := c.FS()
err := fs.WalkDir(fsys, "data", func(p string, d fs.DirEntry, err error) error {
    if err != nil {
        return err
    }
    fmt.Println(p)
    return nil
})

http.Handle("/", http.FileServerFS(fsys))
```

### Interfaces specific to JuiceFS

| Methods | Description |
| --- | --- |
| `Getxattr`, `Listxattr`, `Setxattr`, `Removexattr` | Extended attributes that follow symbolic links. `Setxattr` takes `0`, `juicefs.XattrCreate` or `juicefs.XattrReplace`. If an attribute does not exist, the operation returns `syscall.ENODATA` on Linux and `syscall.ENOATTR` on macOS. |
| `Clone` | Clone a file or a directory by copying its metadata, the same as [`juicefs clone`](../reference/command_reference.mdx#clone). |
| `Info`, `Summary` | Report usage for a file or directory tree, the same as [`juicefs info`](../reference/command_reference.mdx#info) and [`juicefs summary`](../reference/command_reference.mdx#summary). |
| `SetQuota`, `GetQuota`, `DelQuota`, `ListQuota`, `CheckQuota` | Manage [quotas](../guide/quota.md) of directories (specified by path), users, and groups (`uid:<uid>` and `gid:<gid>`). They run as root, without permission checks. |
| `Warmup` | Warm up, check, or evict the cache of files, the same as [`juicefs warmup`](../reference/command_reference.mdx#warmup). |
| `Status`, `SessionInfo` | Volume configuration, statistics, and session information, the same as [`juicefs status`](../reference/command_reference.mdx#status). The returned configuration excludes credentials. |

### Metrics

Each `Client` has its own Prometheus registry. `Client.Gatherer()` returns it, allowing the application to expose JuiceFS metrics alongside its own:

```go
http.Handle("/metrics", promhttp.HandlerFor(
    prometheus.Gatherers{prometheus.DefaultGatherer, c.Gatherer()}, promhttp.HandlerOpts{}))
```

Metrics can also be pushed to a Pushgateway with `Config.PushGateway`.

## Benchmarks

Run from the repository root, without `-race` when measuring performance:

```shell
go test -tags nogspt -run '^$' -bench '^BenchmarkClient' -benchmem -benchtime=1s ./sdk/go
```

The benchmarks use a temporary SQLite metadata store and local file storage. No FUSE mount or external services are required. Setup, fixture validation, and cleanup are excluded from the measurements. All workloads are single-threaded (queue depth 1).

| Benchmark | Workload and timing | Metrics |
| --- | --- | --- |
| `ClientMetadata/CreateEmpty` | Create and close a new, uniquely named empty file per operation; deletion is excluded. | files/s, average µs/op |
| `ClientMetadata/Mkdir` | Create a different empty directory for each operation. Directory names are prepared before timing, and deletion is excluded. | op/s, average µs/op |
| `ClientMetadata/RemoveDir` | Create one empty directory for each operation before timing. Only delete directories during timing. | op/s, average µs/op |
| `ClientMetadata/RemoveFile/Empty` | Create and close a batch of empty files before timing. Remove one file per operation during timing. | files/s, average µs/op |
| `ClientMetadata/RemoveFile/4KiB` | Create and close/flush a batch of 4 KiB files before timing. Remove one file per operation during timing. | files/s, average µs/op |
| `ClientMetadata/OpenClose` | Open and close each of 1,024 pre-created empty files in sequence, one file per operation. Repeat from the beginning after each full pass. | files/s, average µs/op |
| `ClientMetadata/Stat` | Query the attributes of each of 1,024 pre-created empty files in sequence, one file per operation. Repeat from the beginning after each full pass. | ops/s, average µs/op |
| `ClientSmallFiles/Create4KiB` | Create a new 4 KiB file per operation, including closing/flushing the file. | files/s, average µs/op |
| `ClientSmallFiles/Read4KiBWarm` | Rotate through 256 pre-created, warmed 4 KiB files. Each operation opens, reads, and closes one file. | files/s, average µs/op |
| `ClientSequential/Write256MiB` | Sequentially write a 256 MiB file using 1 MiB requests. Each iteration includes a `Sync`, and subsequent iterations overwrite the file from the beginning. | MB/s |
| `ClientSequential/Read256MiB` | Sequentially read a pre-created 256 MiB file using 1 MiB requests. Each iteration starts reading from the beginning of the file. | MB/s |
| `ClientRandom/Read4KiB` | Read aligned 4 KiB blocks in a fixed random permutation across a 256 MiB file. | IOPS, average µs/op |
| `ClientRandom/Write4KiBSync` | Write aligned 4 KiB blocks in the same random order. Each write includes a `Sync`. | IOPS, average µs/op |

Benchmark names have the `Benchmark` prefix. All cases report allocations; `avg-us/op` is the arithmetic mean latency, not a latency percentile. MB/s uses decimal megabytes. File creation includes generating a unique filename but excludes deletion. `RemoveFile` excludes filename generation, file preparation and post-deletion verification; it measures time until `Remove` returns without an additional wait for background data reclamation. For `OpenClose` and `Stat`, batch creation and filename generation are excluded from timing; the default metadata cache settings are retained.

Large-file cases keep one handle open during timing and disable the SDK chunk cache; internal read buffers and the OS page cache remain enabled. Read fixtures are fully read and verified before timing, so reads can hit warm caches. The random offset sequence is prepared outside timing. `Sync` waits for upload and metadata commit, but the local file storage backend does not call `fsync`, so these results are not physical-disk durability or cold-storage measurements. For comparisons, keep the workload, cache state and flush policy consistent. Run individual cases in separate processes for repeated samples, for example:

```shell
for sample in 1 2 3; do
    go test -tags nogspt -run '^$' -bench '^BenchmarkClientRandom$/^Write4KiBSync$' -benchmem -benchtime=1s ./sdk/go
done
```

## Known limitations

- **Concurrent use of a `File`**: Concurrent `Read`, `Write`, and `Seek` calls share the offset without ordering. Synchronize them in the application, or use `ReadAt` and `WriteAt` for explicit offsets. Reads and writes on the same handle are serialized inside JuiceFS. `Close` does not wait for operations in progress.
- **Length of an open file.** `Read` and `ReadAt` use the length recorded when the handle was opened and updated by writes and truncation through that handle. A path-based `Client.Truncate` can leave existing handles with a stale read limit, even on the same client. For example, after shrinking a file from 6 bytes to 2 bytes, `ReadAt` at offset 3 on an existing handle may return bytes past the new EOF, even if `File.Stat` and `Seek(0, io.SeekEnd)` already report 2. Close and reopen the file to read using the new length or to see data appended by other handles or clients.
- **Append and truncation**: `O_APPEND` uses the end of the file known by the handle; appends from multiple handles are not atomic. `O_TRUNC` truncates after opening, in a separate operation.
- **Attribute cache**: With the default `AttrCache` and `DirEntryCache`, `Stat` of a directory may return the old modification time and link count for up to one second after its entries change. Set them to `0` if this matters.
- **Large directories**: `ReadDir` and `File.ReadDir` load the whole directory into memory.
- **Symbolic links**: `Symlink` with an absolute target stores it as a path relative to the link, and `Readlink` returns the stored path.
- **Creating a missing symbolic link target.** If the final path component is a symbolic link whose target file is missing, `Client.OpenFile` with `O_CREATE` and without `O_EXCL` returns `ENOENT` even when the target's parent directory exists and permits creation. In the same case, `os.OpenFile` on a Linux JuiceFS mount follows the link and creates the target file.
- **Lifecycle**: `Close` does not stop all background goroutines, so avoid creating and closing clients repeatedly in a long-running process. Initialization may exit the process when the metadata engine is not available, as `juicefs mount` does.
- **Terminal output**: `Status(true)` draws a progress bar when the standard output is a terminal.
