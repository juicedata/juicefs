---
title: Go SDK
sidebar_position: 6
---

Go SDK 让 Go 程序直接通过元数据引擎和对象存储访问 JuiceFS 文件系统，不依赖 FUSE 挂载。适合无法使用 FUSE 的容器化环境，以及需要在进程内读写 JuiceFS 的 Go 服务。

API 遵循 Go 标准库：`Client` 是绑定到一个文件系统的句柄，以 [`os.Root`](https://pkg.go.dev/os#Root) 为模型；`File` 对应 [`os.File`](https://pkg.go.dev/os#File)；`Client.FS()` 返回 [`io/fs`](https://pkg.go.dev/io/fs) 文件系统。基于 `os` 和 `io/fs` 编写的代码只需少量修改即可使用。

## 环境要求

- Go 1.25 及以上版本。SDK 是 JuiceFS 模块中的一个包 `github.com/juicedata/juicefs/sdk/go`（包名为 `juicefs`），因此会引入 JuiceFS 的全部依赖。
- 需要启用 cgo 并安装 C 编译器：部分依赖（如 SQLite 驱动、压缩算法）使用 cgo 实现。
- 支持 Linux 和 macOS，不支持 Windows。
- 构建时需要加上 `nogspt` 标签。否则程序启动后，JuiceFS 的一个依赖会清空 `ps` 中显示的进程命令行。

```shell
go get github.com/juicedata/juicefs@<version>
go build -tags nogspt ./...
```

使用前需要先用 [`juicefs format`](../reference/command_reference.mdx#format) 创建文件系统。

## 快速上手

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

`Client` 的所有方法都可以并发调用，每个文件系统创建一个 `Client` 并共享使用即可。路径相对于文件系统的根目录，开头有没有 `/` 都可以。`Close` 会关闭尚未关闭的文件、上传缓冲中的数据并结束会话，程序退出前务必调用。

更多可运行的示例（包括基于 `Client.FS()` 的 HTTP 文件服务）见 [`sdk/go/examples`](https://github.com/juicedata/juicefs/tree/main/sdk/go/examples)。

## 配置

```go
func New(name, metaURL string, conf *Config) (*Client, error)
func DefaultConfig() *Config
```

- `name` 只用于指标标签和日志，不会与文件系统的实际名称比对，不一致时只打印警告日志。
- `metaURL` 是元数据引擎的地址，与 [`juicefs mount`](../reference/command_reference.mdx#mount) 相同。
- `conf` 为 `nil` 时使用 `DefaultConfig()`。修改配置时请从 `DefaultConfig()` 开始：`Config` 中字段的零值表示 0，而不是默认值。

```go
conf := juicefs.DefaultConfig()
conf.CacheDir = "/var/jfsCache"
conf.CacheSize = 10 << 30
conf.MaxReadahead = 32 << 20 // 为顺序读开启预读
c, err := juicefs.New("myjfs", metaURL, conf)
```

各配置项及其默认值与 [Python SDK](./python_sdk.md) 相同。容量的单位为字节，时长为 `time.Duration`。

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Bucket` | `""` | 对象存储的访问地址，覆盖文件系统中记录的地址 |
| `StorageClass` | `""` | 当前客户端写入数据使用的存储类型 |
| `ReadOnly` | `false` | 只读模式，只允许读类操作 |
| `NoSession` | `false` | 不在元数据引擎中注册会话；同时隐含 `NoBGJob`，并且不进行元数据备份和用量上报 |
| `NoBGJob` | `true` | 禁用清理、元数据备份等后台任务 |
| `OpenCache` | `0` | 再次打开文件时复用已缓存属性的时间，0 表示禁用 |
| `BackupMeta` | `1h` | 自动将元数据备份到对象存储的间隔，0 表示不备份；另外需要 `NoBGJob` 为 `false` |
| `BackupSkipTrash` | `false` | 备份元数据时跳过回收站中的文件 |
| `Heartbeat` | `12s` | 向元数据引擎发送心跳的间隔 |
| `CacheDir` | `"memory"` | 本地缓存目录，多个目录用冒号分隔；`"memory"` 表示使用内存缓存 |
| `CacheSize` | 100 MiB | 读缓存的容量 |
| `FreeSpaceRatio` | `0.1` | 缓存盘的最小剩余空间比例 |
| `CachePartialOnly` | `false` | 只缓存随机读和小块读的数据 |
| `VerifyCacheChecksum` | `"extend"` | 缓存数据的校验级别：`none`、`full`、`shrink` 或 `extend` |
| `CacheEviction` | `"2-random"` | 缓存淘汰策略：`none`、`2-random` 或 `lru` |
| `CacheScanInterval` | `1h` | 扫描缓存目录、重建索引的间隔 |
| `CacheExpire` | `0` | 超过该时间未被访问的缓存块会被淘汰，0 表示永不过期 |
| `Writeback` | `false` | 数据块先写入缓存盘，再在后台上传；`CacheDir="memory"` 时无效 |
| `BufferSize` | 300 MiB | 读写缓冲区的总大小 |
| `Prefetch` | `1` | 并发预取数据块的数量 |
| `MaxReadahead` | `0` | 每个读会话的最大预读量；0 表示关闭预读，见下文 |
| `UploadLimit` | `0` | 上传带宽限制（Mbps），0 表示使用文件系统的设置 |
| `DownloadLimit` | `0` | 下载带宽限制（Mbps），0 表示使用文件系统的设置 |
| `MaxUploads` | `20` | 上传的并发数 |
| `MaxDeletes` | `10` | 删除对象的并发数，`RemoveAll` 也使用该值 |
| `SkipDirNlink` | `20` | 事务冲突重试超过该次数后跳过更新目录的 nlink（仅 TKV 引擎），0 表示从不跳过 |
| `SkipDirMtime` | `100ms` | 目录 mtime 的变化小于该值时不更新 |
| `IORetries` | `10` | 网络失败后的重试次数 |
| `GetTimeout` | `5s` | 下载单个对象的超时时间 |
| `PutTimeout` | `60s` | 上传单个对象的超时时间 |
| `FastResolve` | `false` | 一次请求解析整条路径（仅 Redis） |
| `AttrCache` | `1s` | 文件属性的缓存时间 |
| `EntryCache` | `0` | 文件目录项的缓存时间 |
| `DirEntryCache` | `1s` | 目录的目录项缓存时间 |
| `Debug` | `false` | 开启 debug 日志 |
| `NoUsageReport` | `false` | 不上报版本号等匿名使用数据 |
| `AccessLog` | `""` | 访问日志的路径，为空表示不记录 |
| `PushGateway` | `""` | Prometheus Pushgateway 的地址，格式为 `<host>:<port>` |
| `PushInterval` | `10s` | 推送指标的间隔 |
| `PushAuth` | `""` | Pushgateway 的 basic auth，格式为 `<username>:<password>` |
| `PushLabels` | `""` | 推送指标时附加的标签，格式为 `key1:value1;key2:value2` |

### 预读

与 Python SDK 一样，预读默认关闭（`MaxReadahead` 为 0）。顺序读取大文件时可以设置 `MaxReadahead`，例如设为文件系统块大小的 8 倍（默认块大小 4 MiB 时为 32 MiB）。预读还会占用 `BufferSize`，最多使用其中的 80%。

### 身份与 umask

所有操作以进程的有效用户和组的身份执行，按 POSIX 规则检查权限；root（euid 为 0）为超级用户。`New` 读取一次进程的 umask，并应用于之后新建的文件和目录。

### 日志

JuiceFS 的日志是进程全局的，所有 `Client` 共用。设置了 `Config.Debug` 或环境变量 `JUICEFS_DEBUG` 不为空时为 debug 级别，否则使用 `JUICEFS_LOGLEVEL` 的值，默认为 warning 级别。

## API

### 文件与目录

`Client` 的方法与 `os.Root` 或 `os` 包中的同名方法名称、签名和语义相同：

| 类别 | 方法 |
| --- | --- |
| 文件 | `Open`、`Create`、`OpenFile`、`ReadFile`、`WriteFile`、`Truncate` |
| 目录 | `Mkdir`、`MkdirAll`、`ReadDir`、`Remove`、`RemoveAll`、`Rmdir` |
| 名称 | `Rename`、`Link`、`Symlink`、`Readlink` |
| 属性 | `Stat`、`Lstat`、`Chmod`、`Chown`、`Lchown`、`Chtimes` |

`*File` 提供 `*os.File` 的方法，包括 `Read`、`ReadAt`、`Write`、`WriteAt`、`Seek`、`Stat`、`Sync`、`Truncate`、`Chmod`、`Chown`、`ReadDir`、`Readdir`、`Readdirnames` 和 `Close`，并实现了 `io.Reader`、`io.Writer`、`io.Seeker`、`io.ReaderAt`、`io.WriterAt` 和 `fs.ReadDirFile`。`OpenFile` 支持 `O_RDONLY`、`O_WRONLY`、`O_RDWR`、`O_CREATE`、`O_EXCL`、`O_TRUNC` 和 `O_APPEND`，其他 flag 返回 `EINVAL`。

`Stat`、`Lstat` 和 `File.Stat` 返回的 `fs.FileInfo` 的 `Sys` 方法返回 `*juicefs.Stat`，其中包含 inode、属主、链接数和各时间戳。

与 `os` 一样，错误为包装了 `syscall.Errno` 的 `*fs.PathError` 或 `*os.LinkError`；不针对单个路径的 `ListQuota`、`Warmup`、`Status` 和 `SessionInfo` 返回 `*os.SyscallError`。请用 `errors.Is` 判断，例如 `errors.Is(err, fs.ErrNotExist)`、`errors.Is(err, syscall.ENOTDIR)`。`Close` 之后，各方法返回包装了 `fs.ErrClosed` 的错误。

### io/fs

`Client.FS()` 返回文件系统的只读 `fs.FS`，与 `os.Root.FS()` 相同，同时实现了 `fs.StatFS`、`fs.ReadDirFS`、`fs.ReadFileFS` 和 `fs.ReadLinkFS`。其中的路径须符合 `fs.ValidPath`：不以 `/` 开头，`"."` 表示根目录。

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

### JuiceFS 特有接口

| 方法 | 说明 |
| --- | --- |
| `Getxattr`、`Listxattr`、`Setxattr`、`Removexattr` | 扩展属性，跟随符号链接。`Setxattr` 的 flags 为 `0`、`juicefs.XattrCreate` 或 `juicefs.XattrReplace`。属性不存在时，Linux 上返回 `syscall.ENODATA`，macOS 上返回 `syscall.ENOATTR` |
| `Clone` | 通过复制元数据克隆文件或目录，同 [`juicefs clone`](../reference/command_reference.mdx#clone) |
| `Info`、`Summary` | 文件或目录树的用量，同 [`juicefs info`](../reference/command_reference.mdx#info) 和 [`juicefs summary`](../reference/command_reference.mdx#summary) |
| `SetQuota`、`GetQuota`、`DelQuota`、`ListQuota`、`CheckQuota` | 目录（以路径表示）以及用户和组（`uid:<uid>`、`gid:<gid>`）的[配额](../guide/quota.md)。这些操作以 root 身份执行，不检查权限 |
| `Warmup` | 预热、检查或清除文件的缓存，同 [`juicefs warmup`](../reference/command_reference.mdx#warmup) |
| `Status`、`SessionInfo` | 文件系统的配置、统计信息和会话，同 [`juicefs status`](../reference/command_reference.mdx#status)；返回的配置不含凭证 |

### 监控指标

每个 `Client` 有独立的 Prometheus registry。`Client.Gatherer()` 返回该 registry，程序可以将其与自身的指标一起暴露：

```go
http.Handle("/metrics", promhttp.HandlerFor(
    prometheus.Gatherers{prometheus.DefaultGatherer, c.Gatherer()}, promhttp.HandlerOpts{}))
```

也可以通过 `Config.PushGateway` 将指标推送到 Pushgateway。

## 基准测试

在仓库根目录运行，测量性能时不要加 `-race`：

```shell
go test -tags nogspt -run '^$' -bench '^BenchmarkClient' -benchmem -benchtime=1s ./sdk/go
```

测试使用临时 SQLite 元数据和本地文件存储，无需 FUSE 挂载或外部服务。初始化、测试数据准备与校验、清理均不计时。所有场景均为单线程，队列深度为 1。

| Benchmark | 工作负载和计时范围 | 指标 |
| --- | --- | --- |
| `ClientMetadata/CreateEmpty` | 每次创建并关闭一个名称唯一的空文件，不计删除 | files/s、平均 µs/op |
| `ClientMetadata/Mkdir` | 每次创建一个不同的空目录，目录名在计时前准备，不计删除 | op/s、平均 µs/op |
| `ClientMetadata/RemoveDir` | 计时前为每次操作准备一个空目录，计时内只删除目录 | op/s、平均 µs/op |
| `ClientMetadata/RemoveFile/Empty` | 计时前创建并关闭一批空文件，计时内逐个删除 | files/s、平均 µs/op |
| `ClientMetadata/RemoveFile/4KiB` | 计时前创建并关闭/刷新一批 4 KiB 文件，计时内逐个删除 | files/s、平均 µs/op |
| `ClientMetadata/OpenClose` | 顺序遍历预先创建的 1,024 个空文件，每次打开并关闭一个文件，遍历完后重新开始 | files/s、平均 µs/op |
| `ClientMetadata/Stat` | 顺序遍历预先创建的 1,024 个空文件，每次查询一个文件的属性，遍历完后重新开始 | ops/s、平均 µs/op |
| `ClientSmallFiles/Create4KiB` | 每次创建一个新的 4 KiB 文件，包含关闭/刷新 | files/s、平均 µs/op |
| `ClientSmallFiles/Read4KiBWarm` | 轮换读取预先创建并预热的 256 个 4 KiB 文件，每次包含打开、读取和关闭 | files/s、平均 µs/op |
| `ClientSequential/Write256MiB` | 以 1 MiB 请求连续写入 256 MiB 文件，每轮包含 `Sync`，后续轮次回到文件开头覆盖 | MB/s |
| `ClientSequential/Read256MiB` | 以 1 MiB 请求连续读取预先创建的 256 MiB 文件，每轮回到文件开头 | MB/s |
| `ClientRandom/Read4KiB` | 按固定随机排列读取 256 MiB 文件中对齐的 4 KiB 块 | IOPS、平均 µs/op |
| `ClientRandom/Write4KiBSync` | 按相同随机顺序写入对齐的 4 KiB 块，每次写入均包含 `Sync` | IOPS、平均 µs/op |

Benchmark 名称均带有 `Benchmark` 前缀。所有场景均输出内存分配；`avg-us/op` 为算术平均延迟，不是延迟分位数。MB/s 使用十进制 MB。文件创建包含生成唯一文件名的开销，不包含删除。`RemoveFile` 的文件名生成、文件准备和删除后校验均不计时；测量到 `Remove` 返回为止，不额外等待后台数据回收。`OpenClose` 和 `Stat` 的批量文件创建及文件名生成不计时，保留默认元数据缓存设置。

大文件测试在计时期间保持同一个文件句柄打开，关闭 SDK chunk 缓存；内部读缓冲和操作系统页缓存仍启用。读取数据在计时前已完整读取并校验，因此可能命中热缓存。随机偏移序列在计时前生成。`Sync` 等待数据上传和元数据提交，但本地 file 存储后端不调用 `fsync`，结果不代表物理磁盘持久化或冷读性能。比较结果时应保持工作负载、缓存状态和刷新策略一致。重复采样建议每次使用独立进程运行单个 case，例如：

```shell
for sample in 1 2 3; do
    go test -tags nogspt -run '^$' -bench '^BenchmarkClientRandom$/^Write4KiBSync$' -benchmem -benchtime=1s ./sdk/go
done
```

## 已知限制

- **并发使用同一个 `File`**：SDK 不对同一个 `*File` 上的调用做同步。并发的 `Read` 或 `Write` 共享偏移量且没有顺序保证，请在应用中自行同步，或改用 `ReadAt` 和 `WriteAt`。同一个句柄上的读写在 JuiceFS 内部仍然是串行执行的。
- **已打开文件的长度**：`Read` 和 `Seek(0, io.SeekEnd)` 所用的文件末尾是打开时的长度加上通过该句柄写入的数据。其他句柄或客户端追加的数据要重新打开文件后才能看到。
- **属性缓存**：使用默认的 `AttrCache` 和 `DirEntryCache` 时，目录中的条目变化后，最多 1 秒内对该目录调用 `Stat` 可能返回旧的修改时间和链接数。如有需要可将它们设为 0。
- **大目录**：`ReadDir` 和 `File.ReadDir` 会把整个目录一次性读入内存。
- **符号链接**：与 Python SDK 一样，`Symlink` 会把绝对路径的目标保存为相对于链接的路径，`Readlink` 返回保存的路径。
- **空路径**：空路径会被当作根目录，而不是返回 `ENOENT`。请不要传入空路径。
- **生命周期**：`Close` 不会停止所有后台 goroutine，长期运行的进程应避免反复创建和关闭 `Client`。与 `juicefs mount` 一样，元数据引擎不可用时初始化可能导致进程退出。
- **终端输出**：标准输出为终端时，`Status(true)` 会绘制进度条。
