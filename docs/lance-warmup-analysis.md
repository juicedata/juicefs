# JuiceFS Lance Manifest 预热功能 — 实现文档

## 一、功能概述

本文档描述 JuiceFS warmup 命令对 Lance 数据集的预热支持，包括：

1. **数据集级预热**：解析 Lance manifest，自动发现并预热所有相关文件
2. **列级元数据预热**（V2 格式）：精确预热指定列的元数据，而非整个数据文件

---

## 二、JuiceFS 架构简介

### 2.1 整体架构

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

### 2.2 数据模型

**File → Chunk → Slice → Block**

| 概念 | 说明 |
|------|------|
| **File** | 用户可见的文件，有 inode |
| **Chunk** | 文件按 64MiB (ChunkSize) 切分 |
| **Slice** | chunk 中的数据切片，有 `Id`/`Size`/`Off`/`Len` |
| **Block** | slice 在对象存储中的存储单元，按 BlockSize(默认 4MiB) 切分 |

对象存储 key 格式：`chunks/{id/1000/1000}/{id/1000}/{id}_{indx}_{blockSize}`

### 2.3 Warmup 预热流程

```
CLI (cmd/warmup.go)
  → 写入 FillCache 消息到 .jfs.control 控制文件
VFS (pkg/vfs/internal.go)
  → handleMsg 解析 FillCache 消息
CacheFiller (pkg/vfs/fill.go)
  → Cache() / CacheWithConfig() 遍历路径，resolve() 解析 inode
  → walkDir() 递归遍历目录
  → cacheFile() 处理每个文件
sliceIterator
  → meta.Read(inode, chunkIndex) 获取 slices
  → 对每个 slice 调用 handler
cachedStore.FillCache(id, size)
  → 计算 block keys → 从对象存储下载 → 写入 bcache 本地缓存
```

---

## 三、Lance 数据格式分析

### 3.1 Lance 数据集目录结构

```
/path/to/dataset.lance/          ← 数据集根目录
├── _versions/                    ← Manifest 版本目录
│   ├── 0.manifest               ← V1 命名: {version}.manifest
│   ├── 1.manifest
│   ├── 99999999999999999999.manifest  ← V2 命名: {u64::MAX - version:020}.manifest
│   ├── latest_version_hint.json  ← 版本提示: {"version": N}
│   └── d{version}.manifest      ← Detached 版本
├── data/                         ← 数据文件目录 (DataFile.path 相对于此)
│   ├── 0000-uuid.lance
│   ├── 0001-uuid.lance
│   └── ...
├── _deletions/                   ← 删除标记目录
├── _indices/                     ← 二级索引目录
├── _transactions/                ← 事务文件目录
└── _overlays/                    ← Overlay 文件目录
```

### 3.2 Manifest 命名方案

| 方案 | 格式 | 说明 |
|------|------|------|
| V1 | `_versions/{version}.manifest` | 简单递增 |
| V2 | `_versions/{u64::MAX - version:020}.manifest` | 反转+零填充20位，最新版本字典序排第一 |
| Detached | `_versions/d{version}.manifest` | 临时版本 |

版本提示文件 `latest_version_hint.json`：`{"version": 42}`，用于 O(1) 查找最新版本。

### 3.3 Manifest 文件物理格式

Manifest 是 **Protobuf 二进制文件**，尾部有固定格式：

```
┌─────────────────────────────────────────────┐
│           Manifest 文件布局                  │
├─────────────────────────────────────────────┤
│  [protobuf message length: 4 bytes LE]     │
├─────────────────────────────────────────────┤
│       pb::Manifest protobuf message          │
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

### 3.4 Protobuf 消息定义

#### Manifest (`protos/table.proto`)

```protobuf
message Manifest {
  repeated lance.file.Field fields = 1;          // Schema 字段
  uint64 version = 2;                            // 数据集版本号
  repeated DataFragment fragments = 4;            // Fragment 列表
  optional string transaction_file = 12;          // 事务文件路径
  // ... 其他字段
}
```

#### DataFragment

```protobuf
message DataFragment {
  uint64 id = 1;                        // Fragment ID
  repeated DataFile files = 2;          // 数据文件列表
  optional DeletionFile deletion_file = 3;  // 删除文件
  repeated DataOverlayFile overlays = 5;     // Overlay 文件
}
```

#### DataFile

```protobuf
message DataFile {
  string path = 1;               // 相对于 data/ 的路径
  repeated int32 fields = 2;     // 字段 ID 列表
  repeated int32 column_indices = 3; // 字段在文件中的列索引
  // ...
}
```

### 3.5 Lance V2 数据文件格式

V2 文件布局（来自 `protos/file2.proto`）：

```
┌──────────────────────────────────────┐
│  Data Pages                          │  ← 数据缓冲区
│  (Data Buffer 0..N)                  │
├──────────────────────────────────────┤
│  Column Metadatas                     │  ← 每列一个 ColumnMetadata protobuf
│  Column 0 Metadata                   │  ← 包含 pages 列表、buffer_offsets/sizes
│  Column 1 Metadata                    │
│  ...                                  │
│  Column CN Metadata                   │
├──────────────────────────────────────┤
│  Column Metadata Offset (CMO) Table   │  ← 每列 16 字节: position(8B) + length(8B)
├──────────────────────────────────────┤
│  Global Buffers Offset Table          │
├──────────────────────────────────────┤
│  Footer (40 bytes)                    │
│  u64: column_meta_start               │  ← Column Metadatas 区域起始位置
│  u64: column_meta_offsets_start       │  ← CMO Table 起始位置
│  u64: global_buff_offsets_start       │
│  u32: num_global_buffers               │
│  u32: num_columns                      │
│  u16: major_version                    │
│  u16: minor_version                    │
│  "LANC" (4 bytes magic)               │
└──────────────────────────────────────┘
```

#### ColumnMetadata (`protos/file2.proto`)

```protobuf
message ColumnMetadata {
  message Page {
    repeated uint64 buffer_offsets = 1;  // page 数据缓冲区在文件中的偏移
    repeated uint64 buffer_sizes = 2;     // 每个缓冲区的大小
    uint64 length = 3;                   // 逻辑行数
    Encoding encoding = 4;               // 页编码方式
  }
  Encoding encoding = 1;                 // 列级编码
  repeated Page pages = 2;               // 该列的所有页
  repeated uint64 buffer_offsets = 3;   // 列级缓冲区偏移
  repeated uint64 buffer_sizes = 4;      // 列级缓冲区大小
}
```

---

## 四、已实现功能

### 4.1 数据集级预热

**功能：** 给定 Lance 数据集路径，自动解析 manifest，发现所有数据文件、删除文件、overlay 文件和事务文件，统一预热。

**实现文件：**

| 文件 | 说明 |
|------|------|
| `pkg/vfs/proto/lance/table.proto` | Lance manifest protobuf 定义 |
| `pkg/vfs/proto/lance/file.proto` | Lance 文件格式 proto 定义 |
| `pkg/vfs/proto/lance/table.pb.go` | 生成的 Go 代码 |
| `pkg/vfs/proto/lance/file.pb.go` | 生成的 Go 代码 |
| `pkg/vfs/lance_warmup.go` | 核心实现 |
| `pkg/vfs/lance_warmup_test.go` | 单元测试 |
| `cmd/warmup.go` | CLI 选项和消息编码 |
| `pkg/vfs/internal.go` | FillCache 消息协议扩展 |
| `pkg/vfs/fill.go` | CacheWithConfig() 方法 |

**核心方法：**

| 方法 | 说明 |
|------|------|
| `resolveLancePaths()` | 解析数据集路径，返回需要预热的文件路径列表 |
| `findLanceManifestPath()` | 定位 manifest 文件（读 hint 或列目录） |
| `readAndParseLanceManifest()` | 读取 manifest 文件并解析 protobuf |
| `parseLanceManifestBytes()` | 解析 manifest 二进制（LANC 魔数 → manifest_pos → protobuf） |
| `readFileContent()` | 通过 JuiceFS chunk store 读取文件全部内容 |
| `isLanceDataset()` | 检测路径是否是 Lance 数据集（.lance 后缀） |

**流程：**

```
CLI: juicefs warmup --lance /mnt/jfs/data/ds.lance
  → sendCommand 编码 Lance 配置到 FillCache 消息
VFS: handleMsg 解析消息
  → CacheWithConfig()
  → 检测 .lance 后缀 → isLanceDataset()
  → resolveLancePaths():
      1. findLanceManifestPath() — 读 latest_version_hint.json 或列目录
      2. readAndParseLanceManifest() — 读文件 → LANC 魔数 → protobuf 解析
      3. 遍历 fragments → 收集 data/、_deletions/、_overlays/ 文件路径
  → 展开后的路径走正常 warmup 流程
```

### 4.2 列级元数据预热（V2 格式）

**功能：** 精确预热 Lance V2 数据文件中指定列的元数据（ColumnMetadata + page buffers），而非整个文件。

**新增文件：**

| 文件 | 说明 |
|------|------|
| `pkg/vfs/proto/lance/file2/file2.proto` | Lance V2 文件格式 protobuf 定义 |
| `pkg/vfs/proto/lance/file2/file2.pb.go` | 生成的 Go 代码 (ColumnMetadata, Page 等) |

**新增方法 (`pkg/vfs/lance_warmup.go`)：**

| 方法 | 说明 |
|------|------|
| `readLanceFileFooter()` | 读 V2 文件末尾 40B footer（column_meta_start, num_columns 等） |
| `readColumnMetadataOffset()` | 读 CMO 表中指定列的 16B 条目 (position + length) |
| `readColumnMetadata()` | 读取并 protobuf 解析指定列的 ColumnMetadata |
| `columnByteRanges()` | 从 ColumnMetadata 提取页缓冲区和列缓冲区的字节范围 |
| `mergeByteRanges()` | 合并重叠/相邻的字节范围，减少 meta.Read 调用 |
| `warmupByteRanges()` | **核心**：将文件内字节范围映射到 JuiceFS slices → FillCache |
| `buildFieldColumnMap()` | 列名 → column index 映射（支持 DataFile.fields + column_indices） |
| `warmupLanceColumns()` | 列级预热入口：遍历数据文件 → footer → 列元数据 → 字节范围 → 预热 |
| `readFileRange()` | 按字节范围读取文件内容（通过 chunk store） |

**字节范围 → JuiceFS Slice 映射算法：**

```
输入: Lance 文件 inode, 字节范围 [start, end)
输出: 预热对应的 JuiceFS slices

1. startChunk = start / ChunkSize(64MiB)
   endChunk = (end-1) / ChunkSize

2. 对每个 chunkIndex ∈ [startChunk, endChunk]:
   a. meta.Read(inode, chunkIndex, &slices)  // 从元数据引擎获取 slices
   b. 对每个 slice:
      - 计算文件内范围 [chunkStart + slice.Off, chunkStart + slice.Off + slice.Len)
      - 与 [start, end) 取交集
      - 有交集 → FillCache(slice.Id, slice.Size)
```

**列名 → Column Index 映射：**

- 优先使用 manifest 中 DataFile 的 `fields` 和 `column_indices` 字段精确映射
- 回退到 manifest schema 的 DFS 序分配（V2.0 规则）

**流程：**

```
CLI: juicefs warmup --lance --lance-columns "col_a,col_b" /mnt/jfs/data/ds.lance
  → sendCommand 编码列名到 FillCache 消息
VFS: handleMsg 解析消息
  → CacheWithConfig(lanceCfg with Columns)
  → resolveLancePaths():
      1. 找到并解析 manifest
      2. warmupLanceColumns():
           for each data file:
             a. readLanceFileFooter() — 读 40B trailer
             b. buildFieldColumnMap() — 列名→column index
             c. for each requested column:
                - readColumnMetadata(inode, footer, colIdx) — 读 CMO 表 + protobuf
                - columnByteRanges(cm) — 提取 page/col buffer 字节范围
                - mergeByteRanges() — 合并重叠范围
                - warmupByteRanges():
                    for each range [start,end):
                      for chunk in [start/64M, end/64M]:
                        meta.Read(inode, chunk) → slices
                        slice overlaps range? → FillCache(slice.Id, slice.Size)
```

### 4.3 FillCache 消息协议扩展

FillCache 消息在原有基础上向后兼容地扩展了 Lance 配置字段：

```
原有:  [pathsLen:4B] [paths] [threads:2B] [background:1B] [action:1B]
新增:  [lance_flag:1B] [manifest_only:1B] [include_indices:1B]
       [version_len:2B] [version]
       [columns_len:2B] [columns_data] [include_data_pages:1B]
```

所有新增字段都是可选的——通过 `r.HasMore()` 检查，不影响旧版本客户端。

---

## 五、使用方式

### 5.1 数据集级预热

```bash
# 预热整个 Lance 数据集（自动解析 manifest，预热所有数据文件）
juicefs warmup --lance /mnt/jfs/data/mydataset.lance

# 预热指定版本的数据集
juicefs warmup --lance --lance-version 5 /mnt/jfs/data/mydataset.lance

# 仅预热 manifest 文件（元数据，不预热数据文件）
juicefs warmup --lance --lance-manifest-only /mnt/jfs/data/mydataset.lance

# 预热数据集包括索引文件
juicefs warmup --lance --lance-include-indices /mnt/jfs/data/mydataset.lance
```

### 5.2 列级元数据预热

```bash
# 预热 col_a 和 col_b 的列元数据（ColumnMetadata + column-level buffers）
juicefs warmup --lance --lance-columns "col_a,col_b" /mnt/jfs/data/mydataset.lance

# 预热 col_a 的列元数据 + 数据页缓冲区（page buffer offsets/sizes）
juicefs warmup --lance --lance-columns "col_a" --lance-column-data /mnt/jfs/data/mydataset.lance

# 组合使用：指定版本 + 列级预热
juicefs warmup --lance --lance-version 3 --lance-columns "col_a,col_b" /mnt/jfs/data/mydataset.lance
```

### 5.3 CLI 参数汇总

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--lance` | bool | false | 启用 Lance 数据集预热模式 |
| `--lance-version` | string | "" (最新) | 指定 Lance 数据集版本 |
| `--lance-manifest-only` | bool | false | 仅预热 manifest 文件 |
| `--lance-include-indices` | bool | false | 同时预热索引文件 |
| `--lance-columns` | string | "" | 逗号分隔的列名，预热列级元数据 |
| `--lance-column-data` | bool | false | 同时预热列数据页缓冲区 |

### 5.4 使用场景

**场景 1：首次加载 Lance 数据集**

```bash
# 预热整个数据集，加速首次查询
juicefs warmup --lance /mnt/jfs/data/training_data.lance
```

**场景 2：大宽表，只查询部分列**

```bash
# 500 列的表，只查询 3 列
juicefs warmup --lance --lance-columns "id,timestamp,feature_42" /mnt/jfs/data/wide_table.lance
```

**场景 3：版本更新后增量预热**

```bash
# 新版本发布，只预热新版本的 manifest 和指定列
juicefs warmup --lance --lance-version 10 --lance-columns "id,label" /mnt/jfs/data/ds.lance
```

**场景 4：仅需元数据加速（不占大量缓存空间）**

```bash
# 只预热 manifest 文件，适合元数据频繁访问但数据访问较少的场景
juicefs warmup --lance --lance-manifest-only /mnt/jfs/data/ds.lance
```

---

## 六、实现细节

### 6.1 项目结构

```
juicefs/
├── cmd/warmup.go                      # CLI: --lance 等选项, sendCommand 编码
├── pkg/vfs/
│   ├── fill.go                        # CacheWithConfig(): Lance 检测分支
│   ├── internal.go                    # handleMsg: FillCache 消息解码
│   ├── lance_warmup.go                # ★ 核心实现 (数据集级 + 列级预热)
│   ├── lance_warmup_test.go           # 单元测试
│   └── proto/lance/
│       ├── table.proto                # Lance manifest protobuf 定义
│       ├── file.proto                 # Lance V1 文件格式 proto
│       ├── table.pb.go               # 生成的 Go 代码
│       ├── file.pb.go                 # 生成的 Go 代码
│       └── file2/
│           ├── file2.proto            # Lance V2 文件格式 proto (ColumnMetadata)
│           └── file2.pb.go            # 生成的 Go 代码
```

### 6.2 FillCache 消息协议

```
┌────────────────────────────────────────────────────────────────┐
│ FillCache 消息体 (向后兼容扩展)                                │
├────────────────────────────────────────────────────────────────┤
│ pathsLen: 4 bytes LE                                           │
│ paths:    N bytes (pathsLen, \n 分隔的路径)                     │
│ threads:  2 bytes LE                                           │
│ background: 1 byte                                             │
│ action:   1 byte (WarmupCache=0, EvictCache=1, CheckCache=2)  │
├──────── ─ ─ ─ ─ 以下为可选字段 (通过 HasMore 检查) ─ ─ ─ ─ ─┤
│ lance_flag:       1 byte (1=lance mode)                      │
│ manifest_only:    1 byte                                       │
│ include_indices:  1 byte                                       │
│ version_len:      2 bytes LE                                   │
│ version:          N bytes                                      │
│ columns_len:      2 bytes LE (逗号分隔列名)                     │
│ columns_data:     N bytes                                      │
│ include_data_pages: 1 byte                                      │
└────────────────────────────────────────────────────────────────┘
```

### 6.3 测试

#### 单元测试

已实现 13 个测试（9 单元 + 4 E2E），覆盖以下场景：

| 测试 | 说明 |
|------|------|
| `TestParseLanceManifestBytes` | 解析包含 fragments/files/deletion 的完整 manifest |
| `TestParseLanceManifestBytes_TooSmall` | 过小文件返回错误 |
| `TestParseLanceManifestBytes_BadMagic` | 错误魔数返回错误 |
| `TestRelativeDeletionFilePath` | 删除文件路径构造 (bitmap/arrow/nil) |
| `TestIsLanceDataset` | .lance 后缀检测 |
| `TestMergeByteRanges` | 字节范围合并 (空/单个/不相交/重叠/相邻/包含) |
| `TestColumnByteRanges` | 从 ColumnMetadata 提取字节范围 (仅元数据/含数据页) |
| `TestBuildFieldColumnMap` | 列名→column index 映射 (回退/精确映射) |
| `TestE2E_ManifestParseAndPathResolution` | 构建 manifest 二进制 → 解析 → 验证 fragments/files/deletion/txn |
| `TestE2E_V2FooterAndColumnMetadata` | 构建 V2 数据文件 → 读 footer → 读 CMO → 解析 ColumnMetadata → 提取字节范围 |
| `TestE2E_FieldColumnMapResolution` | manifest schema → 字段映射 (回退 DFS / 精确映射 / 子集字段) |
| `TestE2E_FullPipeline` | 完整链路：manifest → schema → field map → V2 file → footer → column metadata → byte ranges → merge |

运行测试：

```bash
cd /home/dji/Desktop/code/juicefs
go test ./pkg/vfs/ -run "TestParseLance|TestRelativeDeletion|TestIsLance|TestMergeByte|TestColumnByte|TestBuildField|TestE2E" -v -count=1
```

---

## 七、限制与注意事项

1. **V2 格式仅**：列级元数据预热仅支持 Lance V2 文件格式（footer 中 `major_version` 和 `minor_version` 标识）。不支持 V1 (legacy) 格式的 Page Table。

2. **Slice 粒度限制**：JuiceFS 的预热粒度是 Slice（对象存储对象）。如果目标列的元数据和其他列的数据在同一个 Slice 中，预热整个 Slice 会带入不需要的数据。这是 JuiceFS 架构特性决定的。

3. **嵌套字段**：当前 `buildFieldColumnMap` 使用简单的 DFS 序分配列索引，对于复杂嵌套字段（struct/list）可能不够精确。建议优先使用 manifest 中 DataFile 的 `fields` + `column_indices` 精确映射。

4. **大文件范围读取**：`readFileRange` 通过 chunk store 按范围读取，但对于非常大的文件可能需要多次 meta.Read 调用。已实现字节范围合并优化以减少调用次数。

5. **Protobuf 依赖**：引入 `google.golang.org/protobuf` 依赖，增加少量二进制体积。

6. **并发控制**：列级预热的并发度由 `--threads` 参数控制（默认 50），与文件级预热共用并发池。

---

## 八、手工测试指南

本章描述从编译 JuiceFS、创建 Lance 数据集、挂载文件系统到验证预热功能的完整流程。

### 8.1 环境准备

#### 必要软件

```bash
# Go (编译 JuiceFS)
go version  # 需要 1.21+

# Python + pylance (创建 Lance 数据集)
python3 --version  # 3.8+
pip3 install pylance  # 已安装: pylance 9.1.0b4 / lance 1.2.1

# Redis (JuiceFS 元数据引擎，可选，也可用 SQLite 替代)
# 如果没有 redis-server，可以使用 SQLite 作为元数据引擎
```

#### 8.1.1 编译 JuiceFS

```bash
cd /home/dji/Desktop/code/juicefs

# 切换到 lance 分支
git checkout lance

# 编译
go build -o juicefs .

# 验证
./juicefs --version
# 预期输出: juicefs version 1.5.0-dev+unknown

# 查看 warmup 命令帮助
./juicefs warmup --help
# 确认可以看到 --lance, --lance-columns, --lance-column-data 等选项
```

#### 8.1.2 运行单元测试

```bash
cd /home/dji/Desktop/code/juicefs

# 运行全部 Lance 相关测试
go test ./pkg/vfs/ \
  -run "TestParseLance|TestRelativeDeletion|TestIsLance|TestMergeByte|TestColumnByte|TestBuildField|TestE2E" \
  -v -count=1

# 预期: 13 个测试全部 PASS
```

### 8.2 创建 Lance 数据集

使用 Python + pylance 在本地创建一个测试用 Lance 数据集：

```python
#!/usr/bin/env python3
"""create_lance_dataset.py — 创建测试用 Lance 数据集"""

import lance
import pyarrow as pa
import os

# 1. 定义 schema 和测试数据
schema = pa.schema([
    pa.field("id", pa.int64()),
    pa.field("name", pa.string()),
    pa.field("score", pa.float32()),
    pa.field("embedding", pa.list_(pa.float32(), 128)),
])

num_rows = 10000

table = pa.table({
    "id": pa.array(range(num_rows), type=pa.int64()),
    "name": pa.array([f"item_{i}" for i in range(num_rows)], type=pa.string()),
    "score": pa.array([float(i) / 100.0 for i in range(num_rows)], type=pa.float32()),
    "embedding": pa.array(
        [[float(i) / 1000.0] * 128 for i in range(num_rows)],
        type=pa.list_(pa.float32(), 128)
    ),
}, schema=schema)

# 2. 写入 Lance 数据集
dataset_path = "/tmp/test_lance_data.lance"
if os.path.exists(dataset_path):
    import shutil
    shutil.rmtree(dataset_path)

# 写入数据，分多个 fragment
lance.write_dataset(
    table,
    dataset_path,
    mode="overwrite",
    max_rows_per_file=2500,  # 4 个 fragment
)

print(f"Lance dataset created at: {dataset_path}")

# 3. 验证目录结构
for root, dirs, files in os.walk(dataset_path):
    level = root.replace(dataset_path, "").count(os.sep)
    indent = "  " * level
    print(f"{indent}{os.path.basename(root)}/")
    subindent = "  " * (level + 1)
    for file in sorted(files):
        filepath = os.path.join(root, file)
        size = os.path.getsize(filepath)
        print(f"{subindent}{file} ({size:,} bytes)")

# 4. 打印 manifest 信息
import lance
ds = lance.dataset(dataset_path)
print(f"\nDataset info:")
print(f"  Version: {ds.version}")
print(f"  Schema: {ds.schema}")
print(f"  Num fragments: {ds.num_fragments}")
for frag in ds.get_fragments():
    print(f"    Fragment {frag.fragment_id}: {len(frag.files)} files")
    for f in frag.files:
        print(f"      - {f.path}")
```

运行：

```bash
python3 create_lance_dataset.py
```

预期输出（目录结构类似）：

```
test_lance_data.lance/
  _versions/
    0.manifest (xxx bytes)
    latest_version_hint.json (xx bytes)
  data/
    0000-xxx.lance (xxx bytes)
    0001-xxx.lance (xxx bytes)
    0002-xxx.lance (xxx bytes)
    0003-xxx.lance (xxx bytes)

Dataset info:
  Version: 1
  Schema: id: int64, name: string, score: float, embedding: fixed_size_list<item: float>[128]
  Num fragments: 4
```

### 8.3 挂载 JuiceFS

#### 8.3.1 使用 SQLite 元数据引擎（无需 Redis）

```bash
# 创建挂载点
sudo mkdir -p /mnt/jfs

# 使用 SQLite 作为元数据引擎，本地磁盘作为对象存储
sudo /home/dji/Desktop/code/juicefs/juicefs mount \
  --meta sqlite:///tmp/juicefs-meta.db \
  --storage file \
  --bucket /tmp/juicefs-storage \
  /mnt/jfs
```

挂载成功后，`/mnt/jfs` 就是一个可用的 JuiceFS 文件系统。

#### 8.3.2 使用 Redis 元数据引擎（可选）

```bash
# 先启动 Redis
redis-server --daemonize yes

# 挂载
sudo /home/dji/Desktop/code/juicefs/juicefs mount \
  --meta redis://127.0.0.1:6379/1 \
  --storage file \
  --bucket /tmp/juicefs-storage \
  /mnt/jfs
```

#### 8.3.3 后台挂载（推荐）

```bash
# 后台运行模式
sudo /home/dji/Desktop/code/juicefs/juicefs mount \
  --meta sqlite:///tmp/juicefs-meta.db \
  --storage file \
  --bucket /tmp/juicefs-storage \
  --daemon \
  /mnt/jfs

# 验证挂载
df -h /mnt/jfs
# 预期: juicefs 1.0P  0  1.0P  0% /mnt/jfs

ls /mnt/jfs
# 预期: 可以正常列目录
```

### 8.4 将 Lance 数据集复制到 JuiceFS

```bash
# 创建数据目录
sudo mkdir -p /mnt/jfs/data

# 复制 Lance 数据集到 JuiceFS
sudo cp -r /tmp/test_lance_data.lance /mnt/jfs/data/

# 验证文件在 JuiceFS 中
ls -la /mnt/jfs/data/test_lance_data.lance/
ls -la /mnt/jfs/data/test_lance_data.lance/_versions/
ls -la /mnt/jfs/data/test_lance_data.lance/data/
```

### 8.5 测试预热功能

#### 8.5.1 数据集级预热

```bash
# 清空 JuiceFS 本地缓存（确保预热前没有缓存）
sudo rm -rf /var/jfsCache/local/

# 预热整个 Lance 数据集
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  /mnt/jfs/data/test_lance_data.lance

# 预期输出:
# Lance mode: latest version, manifest-only=false, include-indices=false
# Warmup: <N> paths with 50 workers
# <N> files warmed up, <X> bytes
```

#### 8.5.2 仅预热 Manifest

```bash
# 只预热 manifest 文件
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --lance-manifest-only \
  /mnt/jfs/data/test_lance_data.lance

# 预期: 只预热 _versions/ 下的 manifest 文件，不预热 data/ 目录
```

#### 8.5.3 列级元数据预热

```bash
# 预热 id 和 name 列的元数据
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --lance-columns "id,name" \
  /mnt/jfs/data/test_lance_data.lance

# 预期输出包含:
# Lance column warmup: columns=[id name], include-data=false
# warmup column "id" in /mnt/jfs/data/test_lance_data.lance/data/0000-xxx.lance: N byte ranges
# warmup column "name" in /mnt/jfs/data/test_lance_data.lance/data/0000-xxx.lance: N byte ranges
```

#### 8.5.4 列级预热 + 数据页

```bash
# 预热 score 列的元数据 + 数据页缓冲区
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --lance-columns "score" \
  --lance-column-data \
  /mnt/jfs/data/test_lance_data.lance
```

#### 8.5.5 指定版本预热

```bash
# 预热版本 0
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --lance-version 0 \
  /mnt/jfs/data/test_lance_data.lance

# 预期输出包含:
# Lance mode: version=0, manifest-only=false, include-indices=false
```

#### 8.5.6 检查缓存状态

```bash
# 检查哪些数据块已缓存（不实际预热）
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --check \
  /mnt/jfs/data/test_lance_data.lance

# --check 模式下只检查缓存命中率，不执行下载

# 查看本地缓存目录大小
sudo du -sh /var/jfsCache/local/
```

#### 8.5.7 驱逐缓存

```bash
# 驱逐已缓存的数据块
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  --evict \
  /mnt/jfs/data/test_lance_data.lance

# 验证缓存已被清空
sudo du -sh /var/jfsCache/local/
```

### 8.6 验证预热效果

#### 方法 1：读取延迟对比

```bash
# 清空缓存后，直接读取（冷读）
sudo rm -rf /var/jfsCache/local/
time sudo cat /mnt/jfs/data/test_lance_data.lance/data/*.lance > /dev/null

# 预热
sudo /home/dji/Desktop/code/juicefs/juicefs warmup \
  --lance \
  /mnt/jfs/data/test_lance_data.lance

# 再次读取（热读）
time sudo cat /mnt/jfs/data/test_lance_data.lance/data/*.lance > /dev/null

# 预期: 热读明显快于冷读
```

#### 方法 2：通过 Python 读取 Lance 验证

```python
#!/usr/bin/env python3
"""verify_lance_warmup.py — 验证预热后的 Lance 数据集可正常读取"""

import lance
import time

ds = lance.dataset("/mnt/jfs/data/test_lance_data.lance")
print(f"Dataset: version={ds.version}, fragments={ds.num_fragments}")

# 冷读 / 热读对比
start = time.time()
table = ds.to_table()
elapsed = time.time() - start
print(f"Full scan: {len(table)} rows in {elapsed:.3f}s")

# 列级读取
for col in ["id", "name", "score", "embedding"]:
    start = time.time()
    col_data = ds.to_table(columns=[col])
    elapsed = time.time() - start
    print(f"Column '{col}': {col_data.num_rows} rows in {elapsed:.3f}s")
```

运行：

```bash
# 冷读
sudo rm -rf /var/jfsCache/local/
sudo python3 verify_lance_warmup.py

# 预热
sudo /home/dji/Desktop/code/juicefs/juicefs warmup --lance /mnt/jfs/data/test_lance_data.lance

# 热读
sudo python3 verify_lance_warmup.py
```

### 8.7 测试后清理

```bash
# 卸载 JuiceFS
sudo umount /mnt/jfs

# 清理本地存储
sudo rm -rf /var/jfsCache/
sudo rm -f /tmp/juicefs-meta.db
sudo rm -rf /tmp/juicefs-storage/
sudo rm -rf /tmp/test_lance_data.lance
sudo rm -f /home/dji/Desktop/code/juicefs/juicefs  # 可选: 删除编译产物
```

### 8.8 故障排查

| 问题 | 可能原因 | 解决方法 |
|------|---------|---------|
| `open control file: no such file` | JuiceFS 未挂载 | 确认 `mount` 命令成功，`df -h /mnt/jfs` 有输出 |
| `find lance manifest: no manifest files found` | 目录结构不完整 | 确认 `_versions/` 下有 `.manifest` 文件 |
| `read footer: file too small` | 数据文件损坏 | 重新复制 Lance 数据集到 JuiceFS |
| `column "xxx" not found` | 列名不匹配 | 用 `python3 -c "import lance; print(lance.dataset(path).schema)"` 检查列名 |
| `warmup lance columns: ...` 警告 | V1 格式数据文件 | 确保数据文件是 V2 格式（pylance 1.x 默认写 V2） |
| JuiceFS 挂载失败 | 元数据引擎连接问题 | 检查 `--meta` URL，SQLite 用 `sqlite:///path/to/db` |
| 权限不足 | warmup 需要 root | 使用 `sudo` 执行 warmup 命令 |
