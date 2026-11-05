---
title: Go SDK
sidebar_position: 6
---

The Go SDK lets a Go program access a JuiceFS volume directly through the metadata engine and the object storage, without a FUSE mount. It suits containerized environments where FUSE is not available, and Go services that want to read and write JuiceFS in process.

The API follows the Go standard library: a `Client` is a handle to one volume modeled on [`os.Root`](https://pkg.go.dev/os#Root), a `File` mirrors [`os.File`](https://pkg.go.dev/os#File), and `Client.FS()` returns an [`io/fs`](https://pkg.go.dev/io/fs) file system. Code written against `os` and `io/fs` works with few changes.

## Requirements

- Go 1.25 or later. The SDK is a package of the JuiceFS module, `github.com/juicedata/juicefs/sdk/go` (package name `juicefs`), so it brings in the dependencies of JuiceFS.
- cgo and a C compiler: some dependencies, such as the SQLite driver and the compressors, are implemented with cgo.
- Linux or macOS. Windows is not supported.
- Build with the `nogspt` tag. Without it, a dependency of JuiceFS clears the command line of the process shown by `ps` as soon as the program starts.

```shell
go get github.com/juicedata/juicefs@<version>
go build -tags nogspt ./...
```

The volume must have been created with [`juicefs format`](../reference/command_reference.mdx#format).

## Quick start

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

All methods of a `Client` are safe for concurrent use; create one `Client` per volume and share it. Paths are relative to the root of the volume, with or without a leading `/`. `Close` closes the files left open, uploads the buffered data and ends the session, so always call it before the program exits.

More runnable programs, including an HTTP file server built on `Client.FS()`, are in [`sdk/go/examples`](https://github.com/juicedata/juicefs/tree/main/sdk/go/examples).

## Configuration

```go
func New(name, metaURL string, conf *Config) (*Client, error)
func DefaultConfig() *Config
```

- `name` is only used in metric labels and logs. It is not checked against the name of the volume; a mismatch only logs a warning.
- `metaURL` is the URL of the metadata engine, the same as for [`juicefs mount`](../reference/command_reference.mdx#mount).
- A `nil` `conf` means `DefaultConfig()`. To change options, start from `DefaultConfig()`: a zero field of `Config` means zero, not the default.

```go
conf := juicefs.DefaultConfig()
conf.CacheDir = "/var/jfsCache"
conf.CacheSize = 10 << 30
conf.MaxReadahead = 32 << 20 // enable readahead for sequential reads
c, err := juicefs.New("myjfs", metaURL, conf)
```

The options and their defaults are the same as those of the [Python SDK](./python_sdk.md). Sizes are in bytes and durations are `time.Duration`.

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

Readahead is disabled by default (`MaxReadahead` is 0), as in the Python SDK. For sequential reads of large files, set `MaxReadahead`, for example to 8 times the block size of the volume (32 MiB for the default 4 MiB block). The readahead also needs room in `BufferSize`, of which it uses at most 80%.

### Identity and umask

Operations run as the effective user and groups of the process, and permissions are checked as in POSIX; root (euid 0) is the superuser. `New` reads the umask of the process once and applies it to new files and directories.

### Logs

Logs of JuiceFS are global to the process and shared by all clients. The level is debug if `Config.Debug` is set or the environment variable `JUICEFS_DEBUG` is not empty, otherwise the value of `JUICEFS_LOGLEVEL`, and warning by default.

## API

### Files and directories

The methods of `Client` have the same names, signatures and semantics as those of `os.Root` or the `os` package:

| Group | Methods |
| --- | --- |
| Files | `Open`, `Create`, `OpenFile`, `ReadFile`, `WriteFile`, `Truncate` |
| Directories | `Mkdir`, `MkdirAll`, `ReadDir`, `Remove`, `RemoveAll`, `Rmdir` |
| Names | `Rename`, `Link`, `Symlink`, `Readlink` |
| Attributes | `Stat`, `Lstat`, `Chmod`, `Chown`, `Lchown`, `Chtimes` |

`*File` provides the methods of `*os.File`, including `Read`, `ReadAt`, `Write`, `WriteAt`, `Seek`, `Stat`, `Sync`, `Truncate`, `Chmod`, `Chown`, `ReadDir`, `Readdir`, `Readdirnames` and `Close`, and implements `io.Reader`, `io.Writer`, `io.Seeker`, `io.ReaderAt`, `io.WriterAt` and `fs.ReadDirFile`. `OpenFile` supports the flags `O_RDONLY`, `O_WRONLY`, `O_RDWR`, `O_CREATE`, `O_EXCL`, `O_TRUNC` and `O_APPEND`; other flags give `EINVAL`.

The `Sys` method of the `fs.FileInfo` returned by `Stat`, `Lstat` and `File.Stat` returns a `*juicefs.Stat` with the inode, the owner, the link count and the timestamps.

Errors are `*fs.PathError` or `*os.LinkError` wrapping a `syscall.Errno`, as in `os`; `ListQuota`, `Warmup`, `Status` and `SessionInfo`, which take no single name, return `*os.SyscallError`. Check them with `errors.Is`, for example `errors.Is(err, fs.ErrNotExist)` or `errors.Is(err, syscall.ENOTDIR)`. After `Close`, methods return errors wrapping `fs.ErrClosed`.

### io/fs

`Client.FS()` returns a read-only `fs.FS` of the volume, like `os.Root.FS()`. It also implements `fs.StatFS`, `fs.ReadDirFS`, `fs.ReadFileFS` and `fs.ReadLinkFS`. Its names follow `fs.ValidPath`: no leading `/`, and `"."` is the root of the volume.

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

## Known limitations

- **Concurrent use of a `File`**: the SDK does not synchronize calls on the same `*File`. Concurrent `Read` or `Write` calls share the offset without ordering, so synchronize them in the application, or use `ReadAt` and `WriteAt`. Reads and writes on the same handle are still serialized inside JuiceFS.
- **Length of an open file**: the end of a file for `Read` and `Seek(0, io.SeekEnd)` is its length at open plus the writes through the same handle. Data appended by other handles or clients is not visible until the file is opened again.
- **Attribute cache**: with the default `AttrCache` and `DirEntryCache`, `Stat` of a directory may return the old modification time and link count for up to one second after its entries change. Set them to 0 if this matters.
- **Large directories**: `ReadDir` and `File.ReadDir` load the whole directory into memory.
- **Symbolic links**: `Symlink` with an absolute target stores it as a path relative to the link, as the Python SDK does, and `Readlink` returns the stored path.
- **Empty names**: an empty name refers to the root of the volume instead of giving `ENOENT`. Do not pass empty names.
- **Lifecycle**: `Close` does not stop all background goroutines, so avoid creating and closing clients repeatedly in a long-running process. Initialization may exit the process when the metadata engine is not available, as `juicefs mount` does.
- **Terminal output**: `Status(true)` draws a progress bar when the standard output is a terminal.
