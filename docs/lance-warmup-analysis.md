# JuiceFS 源码分析与 Lance Manifest 预热兼容方案

## 一、JuiceFS 源码架构分析

### 1.1 项目整体结构

```
juicefs/
├── main.go              # 入口
├── cmd/                 # CLI 命令实现 (warmup, mount, gc, sync, etc.)
├── pkg/                 # 核心逻辑
│   ├── meta/            # 元数据引擎 (Redis / SQL / TKV)
│   ├── chunk/           # 数据块存储层 (对象存储读写 + 本地缓存)
│   ├── vfs/             # 虚拟文件系统 (FUSE 接口)
│   ├── fs/              # 文件系统高层封装
│   ├── object/          # 对象存储抽象 (S3/Azure/GCS/BOS 等)
│   ├── fuse/            # FUSE 挂载层
│   ├── gateway/         # S3 网关
│   ├── acl/             # ACL 权限
│   ├── sync/            # 数据同步
│   ├── usage/           # 用量统计
│   ├── utils/           # 工具函数
│   └── metric/         # Prometheus 指标
├── sdk/                 # Java/Python SDK
├── docs/                # 文档
├── deploy/              # 部署配置
├── hack/                # 工具脚本
└── test/                # 集成测试
```

### 1.2 核心架构

JuiceFS 采用**元数据与数据分离**的架构：

```
┌──────────────────────────────────────────────────┐
│              Application / FUSE                  │
├──────────────────────────────────────────────────┤
│              VFS Layer (pkg/vfs)                 │
│   ┌──────────┐  ┌──────────┐  ┌─────────────┐ │
│   │  Reader   │  │  Writer   │  │ CacheFiller  │ │
│   └──────────┘  └──────────┘  └─────────────┘ │
├──────────────────────────────────────────────────┤
│         Meta Engine (pkg/meta)                   │
│   ┌────────┐  ┌────────┐  ┌────────┐           │
│   │ Redis  │  │  SQL   │  │  TKV   │           │
│   └────────┘  └────────┘  └────────┘           │
├──────────────────────────────────────────────────┤
│      Chunk Store (pkg/chunk)                     │
│   ┌──────────────────────────────────────┐       │
│   │         cachedStore                   │       │
│   │  ┌───────────┐  ┌──────────────────┐ │       │
│   │  │  bcache    │  │  ObjectStorage   │ │       │
│   │  │ (本地缓存) │  │  (S3/GCS/...)    │ │       │
│   │  └───────────┘  └──────────────────┘ │       │
│   └──────────────────────────────────────┘       │
└──────────────────────────────────────────────────┘
```

### 1.3 数据模型

**File → Chunk → Slice → Block**

| 概念 | 说明 |
|------|------|
| **File** | 用户可见的文件，有 inode |
| **Chunk** | 文件被按 64MiB (ChunkSize) 切分成多个 chunk，每个 chunk 有索引 indx |
| **Slice** | chunk 中的数据切片，每个 slice 有 `Id`/`Size`/`Off`/`Len` |
| **Block** | slice 在对象存储中的实际存储单元，按 BlockSize (默认 4MiB) 切分 |

对象存储 key 格式：`chunks/{id/1000/1000}/{id/1000}/{id}_{indx}_{blockSize}`

### 1.4 元数据存储

三种引擎实现 `Meta` 接口：Redis / SQL / TKV

核心 `Read` 调用链：
```
baseMeta.Read(inode, indx, &slices)
  → of.ReadChunk(inode, indx)     // open file 缓存
  → en.doRead(inode, indx)        // 从元数据引擎读取
  → buildSlice(ss)                // 构建有序 slice 列表
  → of.CacheChunk(inode, indx, slices)  // 缓存
```

---

## 二、Warmup 预热功能深度分析

### 2.1 整体流程

```
CLI (cmd/warmup.go)
  → 写入 FillCache 消息到 .jfs.control 控制文件
VFS (pkg/vfs/internal.go)
  → handleMsg 解析 FillCache 消息
CacheFiller (pkg/vfs/fill.go)
  → Cache() 遍历路径，resolve() 解析 inode
  → walkDir() 递归遍历目录
  → cacheFile() 处理每个文件
sliceIterator
  → meta.Read(inode, chunkIndex) 获取 slices
  → 对每个 slice 调用 handler
cachedStore.FillCache(id, size)
  → 计算 block keys → 从对象存储下载 → 写入 bcache 本地缓存
```

### 2.2 核心组件

- **CLI 层** (`cmd/warmup.go`): 收集路径，分批发送 FillCache 命令
- **VFS 消息处理** (`pkg/vfs/internal.go`): 解析 FillCache 消息，调用 CacheFiller
- **CacheFiller** (`pkg/vfs/fill.go`): 遍历目录/文件，创建 sliceIterator
- **sliceIterator**: 遍历文件的 chunks 和 slices
- **cachedStore.FillCache** (`pkg/chunk/cached_store.go`): 下载数据块到本地缓存

---

## 三、Lance 源码与 Manifest 格式深度分析

### 3.1 Lance 项目结构

```
lance/
├── protos/                      # Protobuf 定义
│   ├── table.proto              # Manifest / DataFragment / DataFile 消息
│   ├── file.proto               # Lance 文件格式 (Field / Metadata)
│   └── transaction.proto        # 事务定义
├── rust/
│   ├── lance-table/src/         # 表格式核心
│   │   ├── format/manifest.rs   # Manifest 结构体
│   │   ├── format/fragment.rs   # Fragment / DataFile 结构体
│   │   └── io/
│   │       ├── commit.rs        # Manifest 命名、路径、版本管理
│   │       └── manifest.rs      # Manifest 读写逻辑
│   ├── lance-file/src/format.rs # MAGIC="LANC", 版本号
│   └── lance/src/dataset/       # Dataset 层
```

### 3.2 Lance 数据集目录结构

```
/path/to/dataset.lance/          ← 数据集根目录
├── _versions/                    ← Manifest 版本目录
│   ├── 0.manifest               ← V1 命名: {version}.manifest
│   ├── 1.manifest
│   ├── 99999999999999999999.manifest  ← V2 命名: {u64::MAX - version}.manifest
│   ├── latest_version_hint.json  ← 版本提示: {"version": N}
│   └── d{version}.manifest      ← Detached 版本
├── data/                         ← 数据文件目录
│   ├── 0000-uuid.lance
│   ├── 0001-uuid.lance
│   └── ...
├── _deletions/                   ← 删除标记目录
├── _indices/                     ← 二级索引目录
├── _transactions/                ← 事务文件目录
└── _overlays/                    ← Overlay 文件目录
```

### 3.3 Manifest 命名方案

两种命名方案 (`rust/lance-table/src/io/commit.rs`)：

| 方案 | 格式 | 说明 |
|------|------|------|
| V1 | `_versions/{version}.manifest` | 简单递增 |
| V2 | `_versions/{u64::MAX - version:020}.manifest` | 反转+零填充20位，最新版本字典序排第一 |
| Detached | `_versions/d{version}.manifest` | 临时版本，不参与正常版本序列 |

版本提示文件 `latest_version_hint.json`：`{"version": 42}`，用于 O(1) 查找最新版本。

### 3.4 Manifest 文件物理格式

Manifest 是 **Protobuf 二进制文件**，尾部有固定格式：

```
┌─────────────────────────────────────────────┐
│           Manifest 文件布局                  │
├─────────────────────────────────────────────┤
│  [protobuf message length: 4 bytes LE]     │
├─────────────────────────────────────────────┤
│                                             │
│       pb::Manifest protobuf message          │
│                                             │
├─────────────────────────────────────────────┤
│  manifest_position: 8 bytes LE              │
├─────────────────────────────────────────────┤
│  MAJOR_VERSION: 2 bytes (i16, =0)           │
├─────────────────────────────────────────────┤
│  MINOR_VERSION: 2 bytes (i16, =2)          │
├─────────────────────────────────────────────┤
│  MAGIC: 4 bytes = "LANC"                    │
└─────────────────────────────────────────────┘
```

**读取逻辑** (`rust/lance-table/src/io/manifest.rs:read_manifest`)：
1. 先读文件末尾 64KiB (PREFETCH_SIZE)
2. 检查末尾 4 字节是否为 `LANC` 魔数
3. 从末尾 16 字节处读取 `manifest_position` (8 bytes LE)
4. 从 `manifest_position` 开始读取：先 4 字节长度，再读对应长度的 protobuf 数据
5. `prost::Message::decode()` 反序列化为 `pb::Manifest`

### 3.5 Protobuf 消息定义

#### Manifest (`protos/table.proto`)

```protobuf
message Manifest {
  repeated lance.file.Field fields = 1;          // Schema 字段
  map<string, bytes> schema_metadata = 14;       // Schema 元数据
  uint64 version = 2;                            // 数据集版本号
  optional string branch = 23;                   // 分支名
  optional string tag = 15;                       // 版本标签
  optional .google.protobuf.Timestamp timestamp = 3;
  repeated DataFragment fragments = 4;            // ★ 核心：Fragment 列表
  optional uint64 index_section = 9;             // 索引区位置
  optional string transaction_file = 12;          // 事务文件路径
  uint64 reader_feature_flags = 10;
  uint64 writer_feature_flags = 11;
  optional uint32 max_fragment_id = 13;
  uint64 next_row_id = 16;
  optional DataStorageFormat data_format = 21;
  map<string, string> config = 17;
  map<string, string> table_metadata = 24;
  repeated BasePath base_paths = 27;
  // ...
}
```

#### DataFragment (`protos/table.proto`)

```protobuf
message DataFragment {
  uint64 id = 1;                        // Fragment ID
  repeated DataFile files = 2;          // ★ 数据文件列表
  optional DeletionFile deletion_file = 3;  // 删除文件
  optional DataFile row_id_file = 4;    // 行 ID 文件
  repeated DataOverlayFile overlays = 5; // Overlay 文件
}
```

#### DataFile (`protos/table.proto`)

```protobuf
message DataFile {
  string path = 1;               // ★ 相对于 data/ 的路径
  repeated int32 fields = 2;     // 字段 ID 列表
  repeated int32 column_indices = 3;
  uint32 file_major_version = 4;
  uint32 file_minor_version = 5;
  optional uint64 file_size = 6;
  optional uint32 base_id = 7;   // 外部路径 ID
}
```

#### DeletionFile (`protos/table.proto`)

```protobuf
message DeletionFile {
  string path = 1;               // 相对于 _deletions/ 的路径
  uint64 num_deleted_rows = 2;
  optional uint64 file_size = 3;
}
```

### 3.6 Rust 结构体

```rust
// rust/lance-table/src/format/manifest.rs
pub struct Manifest {
    pub schema: Schema,
    pub version: u64,
    pub fragments: Arc<Vec<Fragment>>,  // ★ 核心
    pub transaction_file: Option<String>,
    pub config: HashMap<String, String>,
    pub table_metadata: HashMap<String, String>,
    pub base_paths: HashMap<u32, BasePath>,
    // ... 其他字段
}

// rust/lance-table/src/format/fragment.rs
pub struct Fragment {
    pub id: u64,
    pub files: Vec<DataFile>,           // ★ 数据文件
    pub deletion_file: Option<DeletionFile>,
    pub overlays: Vec<DataOverlayFile>,
    // ...
}

pub struct DataFile {
    pub path: String,                   // ★ 相对于 data/ 的路径
    pub fields: Arc<[i32]>,
    pub file_size_bytes: CachedFileSize,
    pub base_id: Option<u32>,
    // ...
}
```

### 3.7 关键发现

| 问题 | 答案 |
|------|------|
| Manifest 格式 | **Protobuf 二进制**，尾部 LANC 魔数 |
| 文件尾部结构 | `[manifest_pos:8B][major:2B][minor:2B][MAGIC:4B]` |
| 数据文件路径 | `DataFile.path` 相对于 `data/` 目录 |
| Deletion 文件路径 | `DeletionFile.path` 相对于 `_deletions/` 目录 |
| 版本查找 | 优先读 `latest_version_hint.json`，回退到目录列表 |
| 命名方案 | V1: `{version}.manifest`，V2: `{u64::MAX-version:020}.manifest` |
| Go SDK | **不存在**，需用 protoc 生成 Go 代码 |
| Protobuf 定义 | `protos/table.proto` + `protos/file.proto`，可直接复用 |

---

## 四、Lance Manifest 兼容预热方案

### 4.1 方案：文件格式感知的预热扩展

#### 4.1.1 整体架构

```
                            ┌─────────────────────┐
                            │   cmd/warmup.go      │
                            │   新增 --lance 选项   │
                            └──────────┬──────────┘
                                       │
                            ┌──────────▼──────────┐
                            │  vfs/internal.go     │
                            │  (消息传递，扩展协议) │
                            └──────────┬──────────┘
                                       │
                            ┌──────────▼──────────┐
                            │  vfs/lance_warmup.go │  ← 新文件
                            │  (Lance manifest 解析)│
                            └──────────┬──────────┘
                                       │
                            ┌──────────▼──────────┐
                            │  vfs/fill.go         │
                            │  CacheFiller         │
                            │  (Lance 感知分支)     │
                            └──────────┬──────────┘
                                       │
                    ┌──────────────────┼──────────────────┐
                    │                   │                  │
           ┌────────▼───────┐  ┌───────▼───────┐  ┌──────▼──────┐
           │ 原有流程         │  │ Manifest 解析  │  │ 数据文件     │
           │ (普通文件预热)   │  │ → fragment 收集│  │ 路径展开     │
           └────────────────┘  └───────────────┘  └─────────────┘
```

#### 4.1.2 实现步骤

##### Step 1: 生成 Go Protobuf 代码

```bash
# 在 juicefs 项目中
mkdir -p pkg/vfs/proto/lance
# 从 lance 项目复制 proto 定义
cp /home/dji/Desktop/code/lance/lance/protos/table.proto pkg/vfs/proto/lance/
cp /home/dji/Desktop/code/lance/lance/protos/file.proto pkg/vfs/proto/lance/

# 生成 Go 代码
protoc --go_out=. --go_opt=Mtable.proto=github.com/juicedata/juicefs/pkg/vfs/proto/lance \
    --go_opt=Mfile.proto=github.com/juicedata/juicefs/pkg/vfs/proto/lance \
    pkg/vfs/proto/lance/table.proto \
    pkg/vfs/proto/lance/file.proto
```

##### Step 2: 创建 Lance Manifest 解析器 (`pkg/vfs/lance_warmup.go`)

```go
package vfs

import (
    "bytes"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "io"
    "path"
    "strconv"
    "strings"

    "github.com/juicedata/juicefs/pkg/meta"
    lancepb "github.com/juicedata/juicefs/pkg/vfs/proto/lance"
    "google.golang.org/protobuf/proto"
)

const (
    lanceMagic           = "LANC"
    lanceManifestDir     = "_versions"
    lanceDataDir          = "data"
    lanceDeletionsDir    = "_deletions"
    lanceIndicesDir      = "_indices"
    lanceTransactionsDir = "_transactions"
    lanceVersionHintFile = "latest_version_hint.json"
    lanceManifestExt     = ".manifest"
)

// LanceWarmupConfig Lance 预热配置
type LanceWarmupConfig struct {
    Version        string // 指定版本，空则为最新
    ManifestOnly   bool   // 仅预热 manifest 文件
    IncludeIndices bool   // 是否预热索引文件
}

// LanceVersionHint 版本提示文件
type LanceVersionHint struct {
    Version uint64 `json:"version"`
}

// resolveLanceWarmupPaths 解析 Lance 数据集，返回需要预热的文件路径列表
//
// 完整流程:
//   1. 找到最新/指定版本的 manifest 路径
//   2. 读取 manifest 文件内容（通过 JuiceFS VFS 读取）
//   3. 解析 protobuf 获取 fragment 信息
//   4. 收集所有数据文件、删除文件、overlay 文件路径
func (c *CacheFiller) resolveLanceWarmupPaths(
    ctx meta.Context,
    datasetPath string,
    config *LanceWarmupConfig,
) ([]string, error) {
    var paths []string

    // 1. 找到 manifest 文件路径
    manifestPath, err := c.findLanceManifestPath(ctx, datasetPath, config.Version)
    if err != nil {
        return nil, fmt.Errorf("find manifest: %w", err)
    }
    paths = append(paths, manifestPath)

    if config != nil && config.ManifestOnly {
        return paths, nil
    }

    // 2. 读取并解析 manifest
    manifest, err := c.readAndParseLanceManifest(ctx, manifestPath)
    if err != nil {
        return nil, fmt.Errorf("parse manifest %s: %w", manifestPath, err)
    }

    // 3. 收集所有 fragment 的数据文件
    for _, frag := range manifest.Fragments {
        // 数据文件: path 相对于 data/ 目录
        for _, dataFile := range frag.Files {
            fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)
            paths = append(paths, fullPath)
        }

        // 删除文件: path 相对于 _deletions/ 目录
        if frag.DeletionFile != nil && frag.DeletionFile.Path != "" {
            delPath := path.Join(datasetPath, lanceDeletionsDir, frag.DeletionFile.Path)
            paths = append(paths, delPath)
        }

        // Overlay 文件
        for _, overlay := range frag.Overlays {
            if overlay.DataFile != nil && overlay.DataFile.Path != "" {
                overlayPath := path.Join(datasetPath, lanceDataDir, overlay.DataFile.Path)
                paths = append(paths, overlayPath)
            }
        }
    }

    // 4. 可选：预热索引文件
    if config != nil && config.IncludeIndices {
        // 递归遍历 _indices/ 目录，通过 meta.Readdir
        indexPath := path.Join(datasetPath, lanceIndicesDir)
        paths = append(paths, indexPath) // walkDir 会处理目录递归
    }

    // 5. 事务文件
    if manifest.TransactionFile != "" {
        paths = append(paths, path.Join(datasetPath, lanceTransactionsDir, manifest.TransactionFile))
    }

    return paths, nil
}

// findLanceManifestPath 找到指定版本的 manifest 文件路径
func (c *CacheFiller) findLanceManifestPath(
    ctx meta.Context,
    datasetPath string,
    version string,
) (string, error) {
    versionsDir := path.Join(datasetPath, lanceManifestDir)

    if version != "" {
        // 指定版本：尝试 V1 和 V2 命名
        v1Path := path.Join(versionsDir, version+lanceManifestExt)
        if inode, attr, st := c.resolvePath(ctx, v1Path); st == 0 && attr.Typ == meta.TypeFile {
            return v1Path, nil
        }
        // 尝试 V2 命名
        if v, err := strconv.ParseUint(version, 10, 64); err == nil {
            inverted := ^uint64(0) - v
            v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
            if _, _, st := c.resolvePath(ctx, v2Path); st == 0 {
                return v2Path, nil
            }
        }
        return "", fmt.Errorf("manifest for version %s not found in %s", version, versionsDir)
    }

    // 未指定版本：优先读 latest_version_hint.json
    hintPath := path.Join(versionsDir, lanceVersionHintFile)
    if content, err := c.readFileContent(ctx, hintPath); err == nil {
        var hint LanceVersionHint
        if err := json.Unmarshal(content, &hint); err == nil && hint.Version > 0 {
            // 尝试 V2 命名
            inverted := ^uint64(0) - hint.Version
            v2Path := path.Join(versionsDir, fmt.Sprintf("%020d%s", inverted, lanceManifestExt))
            if _, _, st := c.resolvePath(ctx, v2Path); st == 0 {
                return v2Path, nil
            }
            // 回退 V1
            v1Path := path.Join(versionsDir, fmt.Sprintf("%d%s", hint.Version, lanceManifestExt))
            if _, _, st := c.resolvePath(ctx, v1Path); st == 0 {
                return v1Path, nil
            }
        }
    }

    // Fallback: 列出 _versions/ 目录找最新版本
    return c.findLatestManifestByListing(ctx, versionsDir)
}

// findLatestManifestByListing 列出 _versions/ 目录找最新 manifest
func (c *CacheFiller) findLatestManifestByListing(
    ctx meta.Context,
    versionsDir string,
) (string, error) {
    // 通过 meta.Resolve + meta.Readdir 遍历目录
    // V1 命名: 文件名是版本号，取最大值
    // V2 命名: 文件名是反转版本号，取最小值
    // ...
    return "", fmt.Errorf("not implemented: list %s", versionsDir)
}

// readAndParseLanceManifest 读取 manifest 文件并解析 protobuf
func (c *CacheFiller) readAndParseLanceManifest(
    ctx meta.Context,
    manifestPath string,
) (*lancepb.Manifest, error) {
    content, err := c.readFileContent(ctx, manifestPath)
    if err != nil {
        return nil, fmt.Errorf("read manifest: %w", err)
    }
    return parseLanceManifestBytes(content)
}

// parseLanceManifestBytes 解析 manifest 二进制数据
//
// 文件尾部布局 (16 bytes):
//   [manifest_position: 8 bytes LE] [major_version: 2 bytes] [minor_version: 2 bytes] [MAGIC: 4 bytes]
//
// manifest_position 处:
//   [protobuf_length: 4 bytes LE] [protobuf data]
func parseLanceManifestBytes(data []byte) (*lancepb.Manifest, error) {
    if len(data) < 16 {
        return nil, fmt.Errorf("manifest file too small: %d bytes", len(data))
    }

    // 检查魔数 (末尾4字节)
    magic := data[len(data)-4:]
    if !bytes.Equal(magic, []byte(lanceMagic)) {
        return nil, fmt.Errorf("invalid magic: %x (expected %x)", magic, []byte(lanceMagic))
    }

    // 读取 manifest position (末尾16字节的前8字节)
    manifestPos := binary.LittleEndian.Uint64(data[len(data)-16 : len(data)-8])
    if manifestPos >= uint64(len(data)) {
        return nil, fmt.Errorf("invalid manifest position: %d (file size: %d)", manifestPos, len(data))
    }

    // 读取 protobuf 消息长度 (manifestPos 处的 4 字节)
    msgStart := int(manifestPos)
    if msgStart+4 > len(data)-16 {
        return nil, fmt.Errorf("manifest message start exceeds file boundary")
    }
    msgLen := binary.LittleEndian.Uint32(data[msgStart : msgStart+4])
    msgEnd := msgStart + 4 + int(msgLen)

    if msgEnd > len(data)-16 {
        return nil, fmt.Errorf("manifest message length mismatch: expected %d, available %d",
            msgLen, len(data)-16-msgStart-4)
    }

    // 解析 protobuf
    pbManifest := &lancepb.Manifest{}
    if err := proto.Unmarshal(data[msgStart+4:msgEnd], pbManifest); err != nil {
        return nil, fmt.Errorf("decode manifest protobuf: %w", err)
    }

    return pbManifest, nil
}

// isLanceDataset 检查路径是否是 Lance 数据集
func isLanceDataset(p string) bool {
    return strings.HasSuffix(p, ".lance") || strings.HasSuffix(p, ".lance/")
}
```

##### Step 3: 修改 CLI 层 (`cmd/warmup.go`)

```go
func cmdWarmup() *cli.Command {
    return &cli.Command{
        // ...
        Flags: []cli.Flag{
            // ... 原有 flags
            &cli.BoolFlag{
                Name:  "lance",
                Usage: "treat paths as Lance dataset directories, warmup manifest and related data files",
            },
            &cli.StringFlag{
                Name:  "lance-version",
                Usage: "specific Lance dataset version to warmup (default: latest)",
            },
            &cli.BoolFlag{
                Name:  "lance-manifest-only",
                Usage: "only warmup Lance manifest files, not data files",
            },
            &cli.BoolFlag{
                Name:  "lance-include-indices",
                Usage: "also warmup Lance index files",
            },
        },
    }
}

// 在 warmup() 函数中:
func warmup(ctx *cli.Context) error {
    // ... 原有逻辑

    lanceMode := ctx.Bool("lance")
    if lanceMode {
        // 不直接发送路径到 FillCache，而是先解析 manifest
        // 然后将展开后的路径列表发送给 FillCache
        // 或者通过扩展的 FillCache 消息传递 lance 配置
    }

    // ... 发送命令
}
```

##### Step 4: 修改 CacheFiller.Cache() 添加 Lance 分支

```go
// pkg/vfs/fill.go
func (c *CacheFiller) Cache(ctx meta.Context, action CacheAction, paths []string, threads int, resp *CacheResponse) {
    // ... 原有初始化

    for _, p := range paths {
        // 检查是否是 Lance 数据集
        if isLanceDataset(p) {
            lanceConfig := &LanceWarmupConfig{
                Version:        lanceVersion,       // 从消息中获取
                ManifestOnly:   lanceManifestOnly,  // 从消息中获取
                IncludeIndices: lanceIncludeIndices,
            }
            
            lancePaths, err := c.resolveLanceWarmupPaths(ctx, p, lanceConfig)
            if err != nil {
                logger.Warnf("resolve lance paths for %s: %s", p, err)
                continue
            }

            // 将解析出的路径加入预热队列
            for _, lp := range lancePaths {
                if st := c.resolve(ctx, lp, &inode, attr); st != 0 {
                    logger.Warnf("resolve lance path %s: %s", lp, st)
                    continue
                }
                if attr.Typ == meta.TypeDirectory {
                    c.walkDir(ctx, inode, todo)
                } else if attr.Typ == meta.TypeFile {
                    _ = sendFile(ctx, todo, _file{inode, attr.Length})
                }
            }
            continue
        }

        // 原有逻辑
        if st := c.resolve(ctx, p, &inode, attr); st != 0 {
            // ...
        }
    }
    // ...
}
```

##### Step 5: 扩展 FillCache 消息协议

在 `pkg/vfs/internal.go` 的 `handleMsg` 中扩展 `meta.FillCache` 消息：

```go
case meta.FillCache:
    // ... 原有解析 paths/concurrent/background/action

    // 新增：Lance 配置 (可选字段，向后兼容)
    var lanceConfig *LanceWarmupConfig
    if r.HasMore() {
        lanceFlag := r.Get8()
        if lanceFlag == 1 {
            lanceConfig = &LanceWarmupConfig{}
            if r.HasMore() {
                lanceConfig.ManifestOnly = r.Get8() == 1
            }
            if r.HasMore() {
                lanceConfig.IncludeIndices = r.Get8() == 1
            }
            if r.HasMore() {
                lanceConfig.Version = string(r.GetBytes())
            }
        }
    }

    v.cacheFiller.CacheWithLance(ctx, action, paths, int(concurrent), stat, lanceConfig)
```

### 4.2 需要辅助实现的方法

CacheFiller 需要一些辅助方法来读取文件内容和检查路径：

```go
// resolvePath 解析路径，返回 inode 和 attr
func (c *CacheFiller) resolvePath(ctx meta.Context, p string) (Ino, *meta.Attr, int) {
    var inode Ino
    var attr meta.Attr
    st := c.meta.Resolve(ctx, 1, p, &inode, &attr)
    return inode, &attr, int(st)
}

// readFileContent 通过 VFS 读取文件全部内容
// 用于读取 manifest 文件和 version hint 文件
func (c *CacheFiller) readFileContent(ctx meta.Context, p string) ([]byte, error) {
    var inode Ino
    var attr meta.Attr
    st := c.meta.Resolve(ctx, 1, p, &inode, &attr)
    if st != 0 {
        return nil, fmt.Errorf("resolve %s: %s", p, st)
    }

    // 通过 chunk store 读取文件内容
    // 需要遍历文件的 chunks/slices 并组装
    // 或者通过 VFS Read 接口
    // ...
    return nil, nil // TODO: 实现
}
```

### 4.3 预热策略优化

基于 Lance 的特性，可以优化预热策略：

| 文件类型 | 大小 | 预热策略 |
|---------|------|---------|
| Manifest 文件 | 小 (KB-MB) | 高优先级，先预热 |
| Version hint | 极小 (bytes) | 随 manifest 一起 |
| 数据文件 | 大 (MB-GB) | 按顺序预热，支持并发 |
| Deletion 文件 | 小 | 随数据文件一起 |
| 索引文件 | 中 | 可选，按需预热 |
| 事务文件 | 小 | 随 manifest 一起 |

### 4.4 增量预热支持

通过对比不同版本的 manifest，可以实现增量预热：

```go
// 只预热新增/变更的 fragment
func (c *CacheFiller) incrementalLanceWarmup(
    ctx meta.Context,
    datasetPath string,
    fromVersion, toVersion string,
) ([]string, error) {
    // 1. 读取两个版本的 manifest
    oldManifest, _ := c.readAndParseLanceManifest(ctx, /* old version path */)
    newManifest, _ := c.readAndParseLanceManifest(ctx, /* new version path */)

    // 2. 对比 fragment 列表
    oldFrags := make(map[uint64]*lancepb.DataFragment)
    for _, f := range oldManifest.Fragments {
        oldFrags[f.Id] = f
    }

    var paths []string
    for _, newFrag := range newManifest.Fragments {
        oldFrag, exists := oldFrags[newFrag.Id]
        if !exists {
            // 新 fragment，预热所有文件
            for _, df := range newFrag.Files {
                paths = append(paths, path.Join(datasetPath, lanceDataDir, df.Path))
            }
        } else {
            // 已有 fragment，检查文件变更
            oldFiles := make(map[string]bool)
            for _, df := range oldFrag.Files {
                oldFiles[df.Path] = true
            }
            for _, df := range newFrag.Files {
                if !oldFiles[df.Path] {
                    paths = append(paths, path.Join(datasetPath, lanceDataDir, df.Path))
                }
            }
        }
    }

    return paths, nil
}
```

### 4.5 实现优先级

| 阶段 | 内容 | 难度 | 依赖 |
|------|------|------|------|
| P0 | 生成 Go protobuf 代码 | 低 | protoc |
| P1 | 实现 manifest 解析器（parseLanceManifestBytes） | 中 | P0 |
| P2 | 实现版本查找（findLanceManifestPath） | 中 | P1 |
| P3 | 实现 resolveLanceWarmupPaths | 中 | P2 |
| P4 | 集成到 CacheFiller.Cache() | 中 | P3 |
| P5 | CLI 层 --lance 选项 | 低 | P4 |
| P6 | 增量预热（版本对比） | 高 | P3 |
| P7 | 索引文件预热 | 中 | P4 |

### 4.6 风险与注意事项

1. **Protobuf 依赖**：引入 `google.golang.org/protobuf` 依赖，增加二进制体积
2. **Manifest 大文件处理**：大数据集的 manifest 可能很大，需要考虑流式解析或内存限制
3. **版本兼容**：Lance 文件格式有 V1/V2 两种命名方案，需要都支持
4. **并发控制**：大数据集可能有数千个数据文件，需要合理控制并发预热数
5. **错误处理**：manifest 解析失败时应有 graceful degradation，不影响普通文件预热
6. **readFileContent 实现**：需要通过 JuiceFS 内部接口读取文件内容，可能需要 VFS 层暴露一个 Read 方法给 CacheFiller

---

## 五、Lance 列级元数据预热设计

### 5.1 背景

当前实现预热的是**文件级**的——对 Lance 数据集而言，预热粒度是 manifest 文件和 data 文件（整个 `.lance` 文件）。但实际场景中，用户可能只需要预热**某一列的元数据**，原因：

1. **列式存储特性**：Lance 是列式格式，分析查询通常只读取部分列，预热点不需要的列浪费带宽和缓存空间
2. **元数据 vs 数据**：列的元数据（page table / column metadata）比列的实际数据小得多，但读取时必须先加载，预热元数据能显著加速首次查询
3. **大宽表场景**：ML/AI 数据集可能有数百甚至数千列，只预热需要的列可以大幅减少预热时间

### 5.2 Lance 文件格式深入分析

#### 5.2.1 V1 文件格式 (Lance File v1, major=0, minor=1)

V1 文件布局：

```
┌───────────────────────────────────┐
│  Data Pages                       │  ← 实际列数据，按列×batch组织
│  (Page Table 指向这些数据)          │
├───────────────────────────────────┤
│  Page Table                       │  ← N×M×2 矩阵: [field_id][batch_id] → (position, length)
│  (位置由 Metadata 指定)            │
├───────────────────────────────────┤
│  File Metadata (protobuf)         │  ← 包含 batch_offsets, page_table_position, manifest_position
├───────────────────────────────────┤
│  Metadata Offset (8 bytes)        │  ← 指向 File Metadata 的位置
├───────────────────────────────────┤
│  MAGIC: "LANC" (4 bytes)          │
└───────────────────────────────────┘
```

**V1 Page Table 结构**：
```
PageTable: BTreeMap<field_id, BTreeMap<batch_id, PageInfo{position, length}>>

加载方式:
  PageTable::load(reader, position, min_field_id, max_field_id, num_batches)
  - 从 position 处读取 (max_field_id - min_field_id + 1) * num_batches * 2 个 int64
  - 每对 (position, length) 描述一个 page 的物理位置和大小
```

**V1 列与 Field 的关系**：
- Manifest 的 `fields` 列表定义了所有字段（含嵌套字段），每个有 `id` 和 `name`
- `DataFile.fields` 列表记录该数据文件包含哪些字段
- 读取某列时：field_id → page_table[field_id][batch_id] → (position, length) → 从 position 处读 length 字节

#### 5.2.2 V2 文件格式 (Lance File v2, major=0, minor=2+)

V2 文件布局（来自 `protos/file2.proto`）：

```
┌──────────────────────────────────────┐
│  Data Pages                          │  ← 数据缓冲区
│  (Data Buffer 0..N)                  │
├──────────────────────────────────────┤
│  Column Metadatas                     │  ← 每列一个 ColumnMetadata protobuf
│  |A| Column 0 Metadata               │  ← 包含 pages 列表、buffer_offsets/sizes
│  |  Column 1 Metadata                │
│  |  ...                               │
│  |  Column CN Metadata                │
├──────────────────────────────────────┤
│  Column Metadata Offset Table         │  ← |B| 每列元数据的位置和大小
│  (Column 0 Position, Size, ...)       │
├──────────────────────────────────────┤
│  Global Buffers Offset Table          │  ← |C| 全局缓冲区位置
│  (Global Buffer 0 Position, Size, .) │
├──────────────────────────────────────┤
│  Footer (40 bytes)                    │
│  A: u64 column_meta_start             │  ← Column Metadatas 的起始位置
│  B: u64 column_meta_offsets_start     │  ← Offset Table 的起始位置
│  C: u64 global_buff_offsets_start     │  ← Global Buffers 的起始位置
│  u32 num_global_buffers               │
│  u32 num_columns                      │
│  u16 major_version                    │
│  u16 minor_version                    │
│  "LANC" (4 bytes magic)               │
└──────────────────────────────────────┘
```

**V2 ColumnMetadata 结构** (`protos/file2.proto`)：
```protobuf
message ColumnMetadata {
  message Page {
    repeated uint64 buffer_offsets = 1;  // page 数据缓冲区在文件中的偏移
    repeated uint64 buffer_sizes = 2;     // 每个缓冲区的大小
    uint64 length = 3;                   // 逻辑行数
    Encoding encoding = 4;               // 页编码方式
    uint64 priority = 5;                 // 优先级（通常为首行行号）
  }
  Encoding encoding = 1;                 // 列级编码
  repeated Page pages = 2;               // ★ 该列的所有页
  repeated uint64 buffer_offsets = 3;   // 列级缓冲区偏移
  repeated uint64 buffer_sizes = 4;      // 列级缓冲区大小
}
```

**V2 列投影**：
- `ReaderProjection` 包含 `column_indices: Vec<u32>`，指定要读取哪些列
- `from_column_names(schema, &["col_a", "col_b"])` → 构建 projection
- 读取时只加载指定列的 ColumnMetadata 和对应的数据页

**V2 Footer 关键字段**：
- `column_meta_start`: Column Metadatas 区域的起始位置
- `column_meta_offsets_start`: Offset Table 的起始位置（每列 12 字节: position:u64 + size:u32）
- `num_columns`: 列数
- **注意**: Footer 中有 `column_meta_offsets_start` 字段但当前 Lance 代码注释 "We don't use this today because we always load metadata for every column"——即当前 Lance 读取所有列的元数据，尚未支持 metadata projection

#### 5.2.3 两种格式的列元数据对比

| 特性 | V1 (legacy) | V2 (current) |
|------|-------------|--------------|
| 列元数据位置 | Page Table (int64 数组) | ColumnMetadata (protobuf) |
| 列元数据内容 | (position, length) pairs | pages[], buffer_offsets[], encoding |
| 列定位方式 | field_id → page_table[field_id][batch] | column_index → column_metadata_offsets[index] |
| 支持列投影 | 是（按 field_id 查询） | 是（按 column_index 选择性读取） |
| 元数据大小 | 固定大小 (num_fields × num_batches × 16B) | 可变大小（protobuf 编码） |

### 5.3 列级元数据预热方案

#### 5.3.1 整体架构

```
                    CLI: juicefs warmup --lance --lance-columns "col_a,col_b" /path/ds.lance
                                    │
                    ┌───────────────▼───────────────┐
                    │  CacheFiller                  │
                    │  resolveLancePaths()           │
                    │  (新增列级预热分支)             │
                    └───────────────┬───────────────┘
                                    │
                    ┌───────────────▼───────────────┐
                    │  LanceFileMetadataReader       │  ← 新增
                    │  1. 读 .lance 文件 footer       │
                    │  2. 解析 ColumnMetadata         │
                    │  3. 提取指定列的 page offsets   │
                    │  4. 计算需要预热的字节范围       │
                    └───────────────┬───────────────┘
                                    │
                    ┌───────────────▼───────────────┐
                    │  JuiceFS Slice 预热            │
                    │  将字节范围映射到 JuiceFS        │
                    │  chunks/slices/blocks          │
                    │  → FillCache(id, size)         │
                    └───────────────────────────────┘
```

#### 5.3.2 核心挑战

预热列元数据比预热整个文件复杂得多，核心问题是：

**JuiceFS 的预热粒度是 Slice（对象存储中的对象），而 Lance 的列元数据是文件内的字节范围。**

JuiceFS 的 `FillCache(id, size)` 预热的是整个 slice（即一个对象存储对象），无法只预热对象的一部分。这意味着：

1. 如果目标列的元数据和**其他列的数据**在同一个 JuiceFS slice 中，预热整个 slice 会把不需要的数据也下载了
2. 需要将 Lance 文件内的字节范围映射到 JuiceFS 的 chunk/slice/block 结构

#### 5.3.3 字节范围 → JuiceFS Block 映射

Lance 数据文件在 JuiceFS 中是一个普通文件。文件的内容通过 JuiceFS 的 chunk/slice/block 机制存储在对象存储中。

```
Lance 文件: /data/0000-uuid.lance (假设大小 100MB, ChunkSize=64MB)
  
  JuiceFS 内部:
  Chunk 0: slices → blocks → 对象存储 objects
    block 0: chunks/0/0/0_0_4194304  (0-4MB)
    block 1: chunks/0/0/0_1_4194304  (4-8MB)
    block 2: chunks/0/0/0_2_4194304  (8-12MB)
    ...
    block 15: chunks/0/0/0_15_4194304 (60-64MB)
  Chunk 1: slices → blocks
    block 0: chunks/0/0/0_16_4194304 (64-68MB)
    ...
  
  如果目标列元数据在文件偏移 0x1000-0x2000 (4KB-8KB):
    → 落在 Chunk 0, block 0 (0-4MB 范围内)
    → 需要 FillCache(slice_id, slice_size) 预热包含这个范围的 block
```

**映射算法**：
```
输入: Lance 文件 inode, 字节范围 [start_offset, end_offset]
输出: 需要预热的 (chunkIndex, blockIndex) 列表

1. startChunk = start_offset / ChunkSize
   endChunk = end_offset / ChunkSize

2. 对每个 chunkIndex ∈ [startChunk, endChunk]:
     a. meta.Read(inode, chunkIndex, &slices)  // 从元数据引擎获取 slices
     b. 对每个 slice:
        - 计算 slice 在文件中的字节范围 [slice.Off, slice.Off + slice.Len]
        - 与 [start_offset, end_offset] 取交集
        - 如果有交集，对该 slice 调用 FillCache(slice.Id, slice.Size)
```

#### 5.3.4 实现设计

##### Step 1: 扩展 CLI 选项

```go
// cmd/warmup.go - 新增 flags
&cli.StringFlag{
    Name:  "lance-columns",
    Usage: "comma-separated list of column names to warmup (only metadata, not data pages)",
},
&cli.BoolFlag{
    Name:  "lance-column-data",
    Usage: "warmup column data pages in addition to metadata",
},
```

使用方式：
```bash
# 仅预热 col_a 和 col_b 的元数据 (ColumnMetadata + page table)
juicefs warmup --lance --lance-columns "col_a,col_b" /path/ds.lance

# 预热 col_a 的元数据 + 数据页
juicefs warmup --lance --lance-columns "col_a" --lance-column-data /path/ds.lance
```

##### Step 2: 新增 Lance 文件元数据读取器

创建 `pkg/vfs/lance_file_reader.go`：

```go
package vfs

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "io"

    "github.com/juicedata/juicefs/pkg/meta"
    lancepb "github.com/juicedata/juicefs/pkg/vfs/proto/lance"
    file2pb "github.com/juicedata/juicefs/pkg/vfs/proto/lance/file2"
    "google.golang.org/protobuf/proto"
)

// LanceFileFooter V2 文件 footer (40 bytes)
type LanceFileFooter struct {
    ColumnMetaStart         uint64 // Column Metadatas 区域起始位置
    ColumnMetaOffsetsStart  uint64 // Column Metadata Offset Table 起始位置
    GlobalBuffOffsetsStart  uint64 // Global Buffers Offset Table 起始位置
    NumGlobalBuffers        uint32
    NumColumns              uint32
    MajorVersion            uint16
    MinorVersion            uint16
}

const lanceFileFooterLen = 40

// readLanceFileFooter 读取 .lance 数据文件的 footer
// 需要先读文件末尾 40 字节
func (c *CacheFiller) readLanceFileFooter(
    ctx meta.Context, inode Ino, fileSize int64,
) (*LanceFileFooter, []byte, error) {
    // 读取文件末尾 footer (40 bytes)
    footerData, err := c.readFileRange(ctx, inode, fileSize-int64(lanceFileFooterLen), int64(lanceFileFooterLen))
    if err != nil {
        return nil, nil, err
    }

    // 检查魔数
    magic := footerData[lanceFileFooterLen-4:]
    if !bytes.Equal(magic, []byte(lanceMagic)) {
        return nil, nil, fmt.Errorf("invalid lance file magic: %x", magic)
    }

    footer := &LanceFileFooter{
        ColumnMetaStart:        binary.LittleEndian.Uint64(footerData[0:8]),
        ColumnMetaOffsetsStart: binary.LittleEndian.Uint64(footerData[8:16]),
        GlobalBuffOffsetsStart: binary.LittleEndian.Uint64(footerData[16:24]),
        NumGlobalBuffers:       binary.LittleEndian.Uint32(footerData[24:28]),
        NumColumns:             binary.LittleEndian.Uint32(footerData[28:32]),
        MajorVersion:           binary.LittleEndian.Uint16(footerData[32:34]),
        MinorVersion:           binary.LittleEndian.Uint16(footerData[34:36]),
    }
    return footer, footerData, nil
}

// readColumnMetadataOffset 读取指定列的 ColumnMetadata 位置和大小
// Offset Table 中每列: position(8 bytes LE) + size(4 bytes LE) = 12 bytes
func (c *CacheFiller) readColumnMetadataOffset(
    ctx meta.Context, inode Ino, footer *LanceFileFooter, columnIndex int,
) (position uint64, size uint32, err error) {
    // Offset Table 中每列占 12 字节
    offset := int64(footer.ColumnMetaOffsetsStart) + int64(columnIndex)*12
    data, err := c.readFileRange(ctx, inode, offset, 12)
    if err != nil {
        return 0, 0, err
    }
    position = binary.LittleEndian.Uint64(data[0:8])
    size = binary.LittleEndian.Uint32(data[8:12])
    return position, size, nil
}

// readColumnMetadata 读取并解析指定列的 ColumnMetadata
func (c *CacheFiller) readColumnMetadata(
    ctx meta.Context, inode Ino, footer *LanceFileFooter, columnIndex int,
) (*file2pb.ColumnMetadata, error) {
    pos, size, err := c.readColumnMetadataOffset(ctx, inode, footer, columnIndex)
    if err != nil {
        return nil, fmt.Errorf("read column %d offset: %w", columnIndex, err)
    }
    data, err := c.readFileRange(ctx, inode, int64(pos), int64(size))
    if err != nil {
        return nil, fmt.Errorf("read column %d metadata: %w", columnIndex, err)
    }
    cm := &file2pb.ColumnMetadata{}
    if err := proto.Unmarshal(data, cm); err != nil {
        return nil, fmt.Errorf("decode column %d metadata: %w", columnIndex, err)
    }
    return cm, nil
}

// resolveColumnByteRanges 从 ColumnMetadata 中提取所有需要预热的字节范围
//
// 如果 includeDataPages=true, 返回 page 数据的字节范围
// 如果 includeDataPages=false, 只返回 ColumnMetadata 自身的字节范围
// (ColumnMetadata 已经包含在 offset table 中读取过)
//
// 返回: [(start_offset, end_offset), ...]
type byteRange struct {
    start uint64
    end   uint64
}

func columnMetadataByteRanges(cm *file2pb.ColumnMetadata, includeDataPages bool) []byteRange {
    var ranges []byteRange

    if includeDataPages {
        // 收集所有 page 的 buffer 字节范围
        for _, page := range cm.Pages {
            for i, off := range page.BufferOffsets {
                size := page.BufferSizes[i]
                ranges = append(ranges, byteRange{start: off, end: off + size})
            }
        }
    }

    // 收集列级 buffer 的字节范围
    for i, off := range cm.BufferOffsets {
        size := cm.BufferSizes[i]
        ranges = append(ranges, byteRange{start: off, end: off + size})
    }

    return ranges
}

// warmupByteRanges 将文件内的字节范围映射到 JuiceFS slices 并预热
//
// 核心算法:
//   对每个字节范围 [start, end):
//   1. 计算 start_chunk = start / ChunkSize, end_chunk = end / ChunkSize
//   2. 对每个 chunk:
//      a. meta.Read(inode, chunk_index, &slices)
//      b. 对每个 slice, 检查其文件内范围 [slice.Off, slice.Off + slice.Len)
//         是否与 [start, end) 有交集
//      c. 有交集则 FillCache(slice.Id, slice.Size)
func (c *CacheFiller) warmupByteRanges(
    ctx meta.Context, inode Ino, fileSize int64, ranges []byteRange, action CacheAction,
) error {
    for _, r := range ranges {
        startChunk := uint32(r.start / meta.ChunkSize)
        endChunk := uint32(r.end / meta.ChunkSize)

        for chunkIdx := startChunk; chunkIdx <= endChunk; chunkIdx++ {
            if ctx.Canceled() {
                return nil
            }
            var slices []meta.Slice
            if st := c.meta.Read(ctx, inode, chunkIdx, &slices); st != 0 {
                logger.Warnf("read inode %d chunk %d: %s", inode, chunkIdx, st)
                continue
            }
            for _, s := range slices {
                sliceStart := uint64(chunkIdx)*uint64(meta.ChunkSize) + uint64(s.Off)
                sliceEnd := sliceStart + uint64(s.Len)

                // 检查交集
                if sliceEnd <= r.start || sliceStart >= r.end {
                    continue // 无交集
                }

                switch action {
                case WarmupCache:
                    if err := c.store.FillCache(s.Id, s.Size); err != nil {
                        logger.Warnf("fill cache slice %d: %s", s.Id, err)
                    }
                case EvictCache:
                    _ = c.store.EvictCache(s.Id, s.Size)
                case CheckCache:
                    _ = c.store.CheckCache(s.Id, s.Size, nil)
                }
            }
        }
    }
    return nil
}
```

##### Step 3: 扩展 `resolveLancePaths` 支持列级预热

```go
// 在 LanceWarmupConfig 中新增字段
type LanceWarmupConfig struct {
    Version          string
    ManifestOnly    bool
    IncludeIndices  bool
    // 新增：列级预热
    Columns          []string  // 要预热的列名
    IncludeDataPages bool      // 是否预热列数据页（true=元数据+数据, false=仅元数据）
}

// 在 resolveLancePaths() 中新增列级分支
func (c *CacheFiller) resolveLancePaths(ctx, datasetPath, config) ([]string, error) {
    // ... 原有逻辑: 找 manifest, 收集文件路径 ...

    // 新增: 如果指定了列名，收集需要预热的字节范围
    if len(config.Columns) > 0 {
        // 1. 从 manifest 的 schema 中找到列名 → field_id / column_index 的映射
        fieldToColumnIndex := c.buildFieldColumnMap(manifest)

        // 2. 对每个数据文件，预热指定列的元数据
        for _, frag := range manifest.Fragments {
            for _, dataFile := range frag.Files {
                fullPath := path.Join(datasetPath, lanceDataDir, dataFile.Path)

                var inode Ino
                var attr = &Attr{}
                if st := c.resolve(ctx, fullPath, &inode, attr); st != 0 {
                    continue
                }

                // 3. 读 .lance 文件 footer
                footer, _, err := c.readLanceFileFooter(ctx, inode, attr.Length)
                if err != nil {
                    logger.Warnf("read footer %s: %s", fullPath, err)
                    continue
                }

                // 4. 对每个指定的列，读取并预热其 ColumnMetadata
                for _, colName := range config.Columns {
                    colIdx, ok := fieldToColumnIndex[colName]
                    if !ok {
                        logger.Warnf("column %s not found in %s", colName, fullPath)
                        continue
                    }
                    if int(colIdx) >= int(footer.NumColumns) {
                        continue
                    }

                    // 读取 ColumnMetadata
                    cm, err := c.readColumnMetadata(ctx, inode, footer, int(colIdx))
                    if err != nil {
                        logger.Warnf("read column metadata %s in %s: %s", colName, fullPath, err)
                        continue
                    }

                    // 计算字节范围
                    ranges := columnMetadataByteRanges(cm, config.IncludeDataPages)

                    // 预热这些字节范围对应的 JuiceFS blocks
                    if err := c.warmupByteRanges(ctx, inode, attr.Length, ranges, WarmupCache); err != nil {
                        logger.Warnf("warmup column %s in %s: %s", colName, fullPath, err)
                    }
                }
            }
        }
    }

    return paths, nil
}

// buildFieldColumnMap 从 manifest 的 schema 构建 列名 → column_index 映射
//
// V2.0: 所有字段（含中间结构字段）都有 column index，按 DFS 序分配
// V2.1: 只有叶子字段有 column index
func (c *CacheFiller) buildFieldColumnMap(manifest *lancepb.Manifest) map[string]int {
    m := make(map[string]int)
    colIdx := 0
    // 遍历 fields，按 DFS 序分配 column index
    // 注意: 需要根据文件版本判断是 V2.0 还是 V2.1 规则
    for _, field := range manifest.Fields {
        // 简化处理: 对叶子字段和非叶子字段都分配 index
        // 精确处理需要检查 file_minor_version
        m[field.Name] = colIdx
        colIdx++
        // TODO: 递归处理嵌套字段
    }
    return m
}
```

##### Step 4: 新增 `readFileRange` 辅助方法

```go
// readFileRange 读取文件中指定字节范围的内容
// 通过 JuiceFS chunk store 读取，支持范围读取
func (c *CacheFiller) readFileRange(
    ctx meta.Context, inode Ino, offset, length int64,
) ([]byte, error) {
    // 计算涉及的 chunk
    startChunk := uint32(offset / int64(meta.ChunkSize))
    endChunk := uint32((offset + length - 1) / int64(meta.ChunkSize))

    var buf bytes.Buffer
    for chunkIdx := startChunk; chunkIdx <= endChunk; chunkIdx++ {
        var slices []meta.Slice
        if st := c.meta.Read(ctx, inode, chunkIdx, &slices); st != 0 {
            return nil, fmt.Errorf("read chunk %d: %s", chunkIdx, st)
        }
        for _, s := range slices {
            if s.Id == 0 {
                continue // hole
            }
            // 计算这个 slice 中我们需要的范围
            chunkStart := int64(chunkIdx) * int64(meta.ChunkSize)
            sliceFileStart := chunkStart + int64(s.Off)
            sliceFileEnd := sliceFileStart + int64(s.Len)

            // 与 [offset, offset+length) 取交集
            readStart := max(sliceFileStart, offset)
            readEnd := min(sliceFileEnd, offset+length)
            if readStart >= readEnd {
                continue
            }

            // 从 chunk store 读取
            intraSliceOff := readStart - sliceFileStart
            readLen := readEnd - readStart
            page := chunk.NewOffPage(int(readLen))
            reader := c.store.NewReader(s.Id, int(s.Size))
            n, err := reader.ReadAt(ctx, page, intraSliceOff)
            if err != nil && n == 0 {
                page.Release()
                return nil, fmt.Errorf("read slice %d at %d: %w", s.Id, intraSliceOff, err)
            }
            buf.Write(page.Data[:n])
            page.Release()
        }
    }
    return buf.Bytes(), nil
}
```

##### Step 5: 扩展 FillCache 消息协议

```go
// cmd/warmup.go - sendCommand 中新增列名编码
if lanceCfg != nil {
    // ... 原有 lance 配置编码 ...
    // 新增: 列名列表
    if len(lanceCfg.Columns) > 0 {
        colData := strings.Join(lanceCfg.Columns, ",")
        wb.Put16(uint16(len(colData)))
        wb.Put([]byte(colData))
        var includeData uint8
        if lanceCfg.IncludeDataPages {
            includeData = 1
        }
        wb.Put8(includeData)
    }
}

// pkg/vfs/internal.go - handleMsg 中新增列名解析
if lanceCfg != nil {
    // ... 原有解析 ...
    if r.HasMore() {
        colLen := int(r.Get16())
        if colLen > 0 && r.HasMore() {
            colData := string(r.Get(colLen))
            lanceCfg.Columns = strings.Split(colData, ",")
        }
        if r.HasMore() {
            lanceCfg.IncludeDataPages = r.Get8() == 1
        }
    }
}
```

##### Step 6: 生成 file2.proto 的 Go 代码

```bash
# 复制 V2 文件格式 proto
cp /home/dji/Desktop/code/lance/lance/protos/file2.proto pkg/vfs/proto/lance/
protoc --go_out=pkg/vfs/proto/lance/ \
    --go_opt=Mfile2.proto=github.com/juicedata/juicefs/pkg/vfs/proto/lance \
    pkg/vfs/proto/lance/file2.proto
```

### 5.4 V1 格式数据文件的列级预热

对于 V1 格式的 .lance 文件，列元数据是 Page Table：

```go
// V1 Page Table 读取
// 1. 读文件末尾找到 Metadata offset
// 2. 读 Metadata protobuf → 获取 page_table_position
// 3. 从 page_table_position 读取指定 field_id 的 (position, length) 对

func (c *CacheFiller) warmupV1Column(
    ctx meta.Context, inode Ino, fileSize int64,
    fieldID int32, numBatches int32,
) error {
    // 1. 读取文件 metadata
    metadata, err := c.readV1FileMetadata(ctx, inode, fileSize)
    if err != nil {
        return err
    }

    // 2. 从 page_table_position 读取指定 field_id 的 pages
    // PageTable 布局: [field_id][batch_id] → (position: int64, length: int64)
    // 每列占 numBatches * 2 * 8 字节
    fieldOffset := int64(metadata.PageTablePosition) +
        int64(fieldID) * int64(numBatches) * 2 * 8

    pageTableData, err := c.readFileRange(ctx, inode, fieldOffset,
        int64(numBatches)*16)
    if err != nil {
        return err
    }

    // 3. 解析每个 batch 的 (position, length) 并预热
    var ranges []byteRange
    for i := 0; i < int(numBatches); i++ {
        pos := binary.LittleEndian.Uint64(pageTableData[i*16 : i*16+8])
        length := binary.LittleEndian.Uint64(pageTableData[i*16+8 : i*16+16])
        if pos == 0 && length == 0 {
            continue // 不存在的 page
        }
        ranges = append(ranges, byteRange{start: pos, end: pos + length})
    }

    return c.warmupByteRanges(ctx, inode, fileSize, ranges, WarmupCache)
}
```

### 5.5 列名到 Field ID / Column Index 的映射

Manifest 的 `fields` 列表定义了所有字段。需要处理嵌套字段：

```go
// buildFieldToColumnIndexMap 构建 列名 → column_index 的映射
// 处理嵌套字段时使用全限定名 (如 "struct_col.field_a")
func buildFieldToColumnIndexMap(fields []*lancepb.Field) map[string]int {
    m := make(map[string]int)
    var walk func(field *lancepb.Field, prefix string, idx *int)
    walk = func(field *lancepb.Field, prefix string, idx *int) {
        name := field.Name
        if prefix != "" {
            name = prefix + "." + name
        }
        m[name] = *idx
        *idx++
        // 注: V1 格式中所有字段都有 column index
        // V2.1 中只有叶子字段有，需要根据版本调整
    }
    idx := 0
    for _, field := range fields {
        walk(field, "", &idx)
    }
    return m
}
```

### 5.6 实现优先级

| 阶段 | 内容 | 难度 | 依赖 |
|------|------|------|------|
| C0 | 生成 file2.proto Go 代码 | 低 | protoc |
| C1 | 实现 readFileRange (按范围读文件) | 中 | — |
| C2 | 实现 readLanceFileFooter (V2) | 中 | C1 |
| C3 | 实现 readColumnMetadata (V2) | 中 | C2 |
| C4 | 实现 warmupByteRanges (范围→slice 映射) | 高 | C1 |
| C5 | 实现 buildFieldColumnMap | 中 | — |
| C6 | 集成列级预热到 resolveLancePaths | 高 | C3,C4,C5 |
| C7 | V1 格式支持 (Page Table 读取) | 中 | C1,C4 |
| C8 | 嵌套字段名处理 | 中 | C5 |
| C9 | 消息协议扩展 + CLI 选项 | 低 | C6 |

### 5.7 风险与注意事项

1. **V1/V2 兼容**：Lance 数据文件可能是 V1 或 V2 格式，需要通过 footer 的 major/minor 版本号判断
2. **Column Index 精确性**：V2.0 和 V2.1 的列索引分配规则不同，需要检查文件版本
3. **嵌套字段**：struct/list 等嵌套类型的列名解析需要正确处理全限定名
4. **Page Table 加载**：V1 的 Page Table 需要知道 min_field_id, max_field_id, num_batches，这些信息来自 manifest 中的 schema 和 fragment
5. **大文件范围读取**：readFileRange 需要高效读取文件内小范围数据，避免读取整个文件
6. **Slice 覆盖**：一个 JuiceFS slice 可能包含多个列的数据，预热 slice 会带入其他列的数据。这是 JuiceFS 架构限制——预热的粒度受限于 slice 大小
7. **性能优化**：可以批量收集所有列的字节范围，合并重叠或相邻的范围，减少 meta.Read 调用次数
8. **DataFile.fields 字段**：manifest 中的 `DataFile.fields` 列表记录了文件包含哪些字段及其在文件中的列索引，这是精确映射的关键
