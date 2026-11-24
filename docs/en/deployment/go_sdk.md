---
title: Go SDK
sidebar_position: 6
---

The Go SDK lets a Go program access a JuiceFS volume directly through the metadata engine and the object storage, without a FUSE mount. It suits containerized environments where FUSE is not available, and Go services that want to read and write JuiceFS in process.

The API follows the Go standard library: a `Client` is a handle to one volume modeled on [`os.Root`](https://pkg.go.dev/os#Root), a `File` mirrors [`os.File`](https://pkg.go.dev/os#File), and `Client.FS()` returns an [`io/fs`](https://pkg.go.dev/io/fs) file system. The supported operations and differences from the standard library are described below.

## Quick start

Use Go 1.25.10 or later, with cgo enabled and a C compiler installed. The SDK package is `github.com/juicedata/juicefs/sdk/go`, named `juicefs`.

```shell
go get github.com/juicedata/juicefs@<version>
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

All methods of a `Client` are safe for concurrent use; create one `Client` per volume and share it. Paths are relative to the root of the volume, with or without a leading `/`. `Close` closes the files left open, flushes buffered writes and ends the session. Finish ongoing operations before closing the client, and check the returned error.

More runnable programs, including an HTTP file server built on `Client.FS()`, are in [`sdk/go/examples`](https://github.com/juicedata/juicefs/tree/main/sdk/go/examples).

## Configuration

```go
func New(name, metaURL string, conf *Config) (*Client, error)
func DefaultConfig() *Config
```

- `name` is only used in metric labels and logs. It is compared with the volume name; a mismatch logs a warning but does not prevent connecting.
- `metaURL` is the URL of the metadata engine, the same as for [`juicefs mount`](../reference/command_reference.mdx#mount).
- A `nil` `conf` means `DefaultConfig()`. To change options, start from `DefaultConfig()`: a zero field of `Config` means zero, not the default.

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
| `Bucket` | `""` | Endpoint of the object storage, overriding the one of the volume |
| `StorageClass` | `""` | Storage class of the data written by this client |
| `ReadOnly` | `false` | Allow only read operations |
| `NoSession` | `false` | Do not register a session in the metadata engine; implies `NoBGJob`, and disables the metadata backup and the usage report |
| `NoBGJob` | `true` | Disable background jobs such as cleanup and metadata backup |
| `OpenCache` | `0` | How long the attributes of an opened file are reused when it is opened again; 0 disables it |
| `BackupMeta` | `1h` | Interval of the automatic metadata backup to the object storage; 0 disables it. It also needs `NoBGJob` to be `false` |
| `BackupSkipTrash` | `false` | Skip the files in the trash when backing up the metadata |
| `Heartbeat` | `12s` | Interval of the heartbeat to the metadata engine |
| `CacheDir` | `"memory"` | Local cache directories separated by colons; `"memory"` caches in memory |
| `CacheSize` | 100 MiB | Size of the read cache |
| `FreeSpaceRatio` | `0.1` | Minimum ratio of free space on the cache disk |
| `CachePartialOnly` | `false` | Only cache the data of random and small reads |
| `VerifyCacheChecksum` | `"extend"` | Checksum level of cached data: `none`, `full`, `shrink` or `extend` |
| `CacheEviction` | `"2-random"` | Eviction policy of the cache: `none`, `2-random` or `lru` |
| `CacheScanInterval` | `1h` | Interval to scan the cache directories and rebuild the index |
| `CacheExpire` | `0` | Evict the cached blocks not accessed for this long; 0 never expires them |
| `Writeback` | `false` | Upload the blocks in the background after writing them to the cache disk; no effect with `CacheDir="memory"` |
| `BufferSize` | 300 MiB | Total size of the read and write buffers |
| `Prefetch` | `1` | Number of blocks prefetched concurrently |
| `MaxReadahead` | `0` | Maximum readahead of each reading session; 0 disables readahead, see below |
| `UploadLimit` | `0` | Upload bandwidth limit in Mbps; 0 uses the limit of the volume |
| `DownloadLimit` | `0` | Download bandwidth limit in Mbps; 0 uses the limit of the volume |
| `MaxUploads` | `20` | Number of concurrent uploads |
| `MaxDeletes` | `10` | Number of concurrent deletions of objects, also used by `RemoveAll` |
| `SkipDirNlink` | `20` | Skip updating the nlink of directories after this many retries of conflicting transactions (TKV engines only); 0 never skips |
| `SkipDirMtime` | `100ms` | Do not update the mtime of a directory if it changes less than this |
| `IORetries` | `10` | Number of retries after a network failure |
| `GetTimeout` | `5s` | Timeout to download an object |
| `PutTimeout` | `60s` | Timeout to upload an object |
| `FastResolve` | `false` | Resolve a whole path in one request (Redis only) |
| `AttrCache` | `1s` | How long the attributes of files are cached |
| `EntryCache` | `0` | How long the entries of files are cached |
| `DirEntryCache` | `1s` | How long the entries of directories are cached |
| `Debug` | `false` | Enable debug logs |
| `NoUsageReport` | `false` | Do not report anonymous usage data such as the version |
| `AccessLog` | `""` | Path of the access log; empty disables it |
| `PushGateway` | `""` | Address of a Prometheus Pushgateway, `<host>:<port>` |
| `PushInterval` | `10s` | Interval to push metrics |
| `PushAuth` | `""` | Basic auth of the Pushgateway, `<username>:<password>` |
| `PushLabels` | `""` | Extra labels of the pushed metrics, `key1:value1;key2:value2` |

### Readahead

Readahead is disabled by default (`MaxReadahead` is 0). For sequential reads of large files, set `MaxReadahead`, for example to 8 times the block size of the volume (32 MiB for the default 4 MiB block). The readahead also needs room in `BufferSize`, of which it uses at most 80%.

### Identity and umask

Operations use the effective user and groups captured when `New` is called, and permissions are checked as in POSIX; root (euid 0) is the superuser. `New` reads the umask of the process once and applies it to new files and directories.

### Logs

Logs of JuiceFS are global to the process and shared by all clients. The level is debug if `Config.Debug` is set or the environment variable `JUICEFS_DEBUG` is not empty, otherwise the value of `JUICEFS_LOGLEVEL`, and warning by default.

## API

### Files and directories

`Client` provides the following operations, modeled on `os.Root` and the `os` package. `Rmdir` is an additional method that only removes directories. See the known limitations below for differences in behavior:

| Group | Methods |
| --- | --- |
| Files | `Open`, `Create`, `OpenFile`, `ReadFile`, `WriteFile`, `Truncate` |
| Directories | `Mkdir`, `MkdirAll`, `ReadDir`, `Remove`, `RemoveAll`, `Rmdir` |
| Names | `Rename`, `Link`, `Symlink`, `Readlink` |
| Attributes | `Stat`, `Lstat`, `Chmod`, `Chown`, `Lchown`, `Chtimes` |

`*File` provides common `*os.File` operations, including `Read`, `ReadAt`, `Write`, `WriteAt`, `Seek`, `Stat`, `Sync`, `Truncate`, `Chmod`, `Chown`, `ReadDir`, `Readdir`, `Readdirnames` and `Close`, and implements `io.Reader`, `io.Writer`, `io.Seeker`, `io.ReaderAt`, `io.WriterAt` and `fs.ReadDirFile`. `OpenFile` supports the flags `O_RDONLY`, `O_WRONLY`, `O_RDWR`, `O_CREATE`, `O_EXCL`, `O_TRUNC` and `O_APPEND`; other flags give `EINVAL`.

The `Sys` method of the `fs.FileInfo` returned by `Stat`, `Lstat` and `File.Stat` returns a `*juicefs.Stat` with the inode, the owner, the link count and the timestamps.

File system errors are generally `*fs.PathError` or `*os.LinkError` wrapping a `syscall.Errno`; `ListQuota`, `Warmup`, `Status` and `SessionInfo`, which take no single name, return `*os.SyscallError`. Check them with `errors.Is`, for example `errors.Is(err, fs.ErrNotExist)` or `errors.Is(err, syscall.ENOTDIR)`. After `Client.Close`, file system operations generally return errors wrapping `fs.ErrClosed`. `Name`, `FS` and `Gatherer` remain callable; repeated `Client.Close` and `RemoveAll("")` return nil.

### io/fs

`Client.FS()` returns an `fs.FS` view of the volume. It opens files for reading, but does not enforce read-only access through all methods of the underlying `*File`; use `Config.ReadOnly` for a read-only client. It also implements `fs.StatFS`, `fs.ReadDirFS`, `fs.ReadFileFS` and `fs.ReadLinkFS`. Its names follow `fs.ValidPath`: no leading `/`, and `"."` is the root of the volume.

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
| `Getxattr`, `Listxattr`, `Setxattr`, `Removexattr` | Extended attributes, following symbolic links. `Setxattr` takes `0`, `juicefs.XattrCreate` or `juicefs.XattrReplace`. A missing attribute gives `syscall.ENODATA` on Linux and `syscall.ENOATTR` on macOS |
| `Clone` | Clone a file or a directory by copying its metadata, as [`juicefs clone`](../reference/command_reference.mdx#clone) |
| `Info`, `Summary` | Usage of a file or a directory tree, as [`juicefs info`](../reference/command_reference.mdx#info) and [`juicefs summary`](../reference/command_reference.mdx#summary) |
| `SetQuota`, `GetQuota`, `DelQuota`, `ListQuota`, `CheckQuota` | [Quotas](../guide/quota.md) of directories (by path) and of users and groups (`uid:<uid>` and `gid:<gid>`). They run as root, without permission checks |
| `Warmup` | Warm up, check or evict the cache of files, as [`juicefs warmup`](../reference/command_reference.mdx#warmup) |
| `Status`, `SessionInfo` | Setting, statistics and sessions of the volume, as [`juicefs status`](../reference/command_reference.mdx#status); the setting leaves out credentials |

### Metrics

Each `Client` has its own Prometheus registry. `Client.Gatherer()` returns it, so that it can be exposed by the program together with its own metrics:

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

The benchmarks use temporary SQLite metadata and local file storage. No FUSE mount or external services are needed. Setup, fixture validation and cleanup are excluded from timing. All workloads are single-threaded (queue depth 1).

| Benchmark | Workload and timing | Metrics |
| --- | --- | --- |
| `ClientMetadata/CreateEmpty` | Create and close a new, uniquely named empty file per operation; deletion is excluded | files/s, average µs/op |
| `ClientMetadata/Mkdir` | Create a different empty directory per operation; prepare names before timing, exclude deletion | op/s, average µs/op |
| `ClientMetadata/RemoveDir` | Pre-create one empty directory per operation; only delete directories during timing | op/s, average µs/op |
| `ClientMetadata/RemoveFile/Empty` | Pre-create and close a batch of empty files; remove one file per operation during timing | files/s, average µs/op |
| `ClientMetadata/RemoveFile/4KiB` | Pre-create and close/flush a batch of 4 KiB files; remove one file per operation during timing | files/s, average µs/op |
| `ClientMetadata/OpenClose` | Sequentially traverse 1,024 pre-created empty files, opening and closing one file per operation; restart after each traversal | files/s, average µs/op |
| `ClientMetadata/Stat` | Sequentially traverse 1,024 pre-created empty files, querying one file's attributes per operation; restart after each traversal | ops/s, average µs/op |
| `ClientSmallFiles/Create4KiB` | Create a new 4 KiB file per operation, including close/flush | files/s, average µs/op |
| `ClientSmallFiles/Read4KiBWarm` | Rotate through 256 pre-created, warmed 4 KiB files; each operation includes open/read/close | files/s, average µs/op |
| `ClientSequential/Write256MiB` | Write a 256 MiB file sequentially in 1 MiB requests, then `Sync`; rewind and overwrite on subsequent passes | MB/s |
| `ClientSequential/Read256MiB` | Read a pre-created 256 MiB file sequentially in 1 MiB requests; rewind on each pass | MB/s |
| `ClientRandom/Read4KiB` | Read aligned 4 KiB blocks in a fixed random permutation across a 256 MiB file | IOPS, average µs/op |
| `ClientRandom/Write4KiBSync` | Write aligned 4 KiB blocks in the same random order, including `Sync` after every write | IOPS, average µs/op |

Benchmark names have the `Benchmark` prefix. All cases report allocations; `avg-us/op` is the arithmetic mean, not a latency percentile. MB/s uses decimal megabytes. File creation includes generating a unique filename but excludes deletion. `RemoveFile` excludes filename generation, file preparation and post-deletion verification; it measures time until `Remove` returns without an additional wait for background data reclamation. For `OpenClose` and `Stat`, batch creation and filename generation are excluded from timing; the default metadata cache settings are retained.

Large-file cases keep one handle open during timing and disable the SDK chunk cache; internal read buffers and the OS page cache remain enabled. Read fixtures are fully read and verified before timing, so reads can hit warm caches. The random offset sequence is prepared outside timing. `Sync` waits for upload and metadata commit, but the local file storage backend does not call `fsync`, so these results are not physical-disk durability or cold-storage measurements. For comparisons, keep the workload, cache state and flush policy consistent. Run individual cases in separate processes for repeated samples, for example:

```shell
for sample in 1 2 3; do
    go test -tags nogspt -run '^$' -bench '^BenchmarkClientRandom$/^Write4KiBSync$' -benchmem -benchtime=1s ./sdk/go
done
```

## Known limitations

- **Concurrent use of a `File`**: concurrent `Read`, `Write` and `Seek` calls share the offset without ordering. Synchronize them in the application, or use `ReadAt` and `WriteAt` for explicit offsets. Reads and writes on the same handle are serialized inside JuiceFS. `Close` does not wait for operations in progress.
- **Length of an open file**: the end of a file for `Read` and `Seek(0, io.SeekEnd)` is the length at open, updated by writes and truncation through the same handle. Data appended by other handles or clients is not visible until the file is opened again.
- **Append and truncation**: `O_APPEND` uses the end of the file known by the handle; appends from multiple handles are not atomic. `O_TRUNC` truncates after opening, in a separate operation.
- **Attribute cache**: with the default `AttrCache` and `DirEntryCache`, `Stat` of a directory may return the old modification time and link count for up to one second after its entries change. Set them to 0 if this matters.
- **Large directories**: `ReadDir` and `File.ReadDir` load the whole directory into memory.
- **Symbolic links**: `Symlink` with an absolute target stores it as a path relative to the link, and `Readlink` returns the stored path.
- **Lifecycle**: `Close` does not stop all background goroutines, so avoid creating and closing clients repeatedly in a long-running process. Initialization may exit the process when the metadata engine is not available, as `juicefs mount` does.
- **Terminal output**: `Status(true)` draws a progress bar when the standard output is a terminal.
