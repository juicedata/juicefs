# JuiceFS Internals

- JuiceFS 版本：1.0
- 元数据版本：v1

## 1 元数据结构

### 1.1 Redis

Redis 中 Key 的通用格式为 `{prefix}{JFSKey}`，其中：

- prefix 在 Redis 非集群模式下为空字符串，在集群模式中是一个大括号括起来的数据库 ID，如 `{10}`

- JFSKey 是指 JuiceFS 不同数据类型的 Key，具体列举在后续小节中

#### 1.1.1 Setting

保存文件系统的格式化信息，在执行 `juicefs format` 命令时创建。

- Key：`setting`
- Value Type：String
- Value：JSON 编码的文件系统格式化信息

#### 1.1.2 Counter

- Key：计数器名称
- Value Type：String
- Value：计数器的值，实际均为整数

计数器名称有：

- usedSpace：
- totalInodes：

- nextInode：
- nextChunk：
- nextSession：
- nextTrash：
- nextCleanupSlices：
- lastCleanupSessions：
- lastCleanupFiles：
- lastCleanupTrash：

#### 1.1.3 Session

- Key：`allSessions`
- Value Type：Sorted Set
- Value：所有连接此文件系统的非只读会话。在 Set 中：
  - Member：会话 ID
  - Score：此会话超时的时间点

#### 1.1.4 SessionInfo

- Key：`sessionInfos`
- Value Type：Hash
- Value：所有非只读会话的基本信息。在 Hash 中：
  - Key：会话 ID
  - Value：JSON 编码的会话信息

#### 1.1.5 Node

- Key：`i{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：String
- Value：二进制编码的文件属性

#### 1.1.6 Edge

- Key：`d{inode}`，inode 为目录 inode 号，以十进制字符串表示
- Value Type：Hash
- Value：inode 指代目录下的所有目录项。在 Hash 中：
  - Key：文件名称
  - Value：二进制编码的文件类型和 inode 号

#### 1.1.7 LinkParent

- Key：`p{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：Hash
- Value：inode 指代文件的所有父目录 inodes。在 Hash 中：
  - Key：父目录 inode
  - Value：此父目录 inode 的计数（硬链接可创建在与源文件相同目录下）

#### 1.1.8 Chunk

- Key：`c{inode}_{index}`，inode 和 index 分别为文件 inode 号和此 Chunk 在该文件中的序号，均以十进制字符串表示

- Value Type：list
- Value：此 Chunk 包含的所有 Slices 列表，每个 Slice 使用大端编码，一共 24 个字节：

```go
type Slice struct {
	pos  uint32 // Slice 在 Chunk 中的偏移位置
	id   uint64 // Slice 的 ID，全局唯一
	size uint32 // Slice 的总大小
	off  uint32 // 有效数据在此 Slice 中的偏移位置
	len  uint32 // 有效数据在此 Slice 中的大小
}
```

#### 1.1.9 ChunkRef

- Key：`sliceRef`
- Value Type：Hash
- Value：所有需记录的 Slices 的计数值。在 Hash 中：
  - Key：`k{id}_{size}`，id 和 size 分别为此 Slice 的唯一编号和大小，均以十进制字符串表示
  - Value：此 Slice 的计数值减一

#### 1.1.10 Symlink

- Key：`s{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：String
- Value：符号链接指向的路径

#### 1.1.11 Xattr

- Key：`x{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：Hash
- Value：此文件的所有扩展属性。在 Hash 中：
  - Key：扩展属性名称
  - Value：扩展属性值

#### 1.1.12 Flock

- Key：`lockf{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：Hash
- Value：此文件的所有 flocks。在 Hash 中：
  - Key：`{sid}_{owner}`，sid 为加锁的会话 ID，以十进制字符串表示；owner 为锁所有者，以十六进制表示
  - Value：锁类型，可能为 “R” 或者 “W”

#### 1.1.13 Plock

- Key：`lockp{inode}`，inode 为文件 inode 号，以十进制字符串表示
- Value Type：Hash
- Value：此文件的所有 plocks。在 Hash 中：
  - Key：`{sid}_{owner}`，sid 为加锁的会话 ID，以十进制字符串表示；owner 为锁所有者，以十六进制表示
  - Value：一个二进制编码的 plocks 列表，每个 plock 对应 24 个字节：

```go
type plock struct {
	ltype uint32
  pid   uint32
  start uint64
  end   uint64
}
```

#### 1.1.14 DelFiles

- Key：`delfiles`
- Value Type：Sorted Set
- Value：所有待清理的文件。在 Set 中：
  - Member：`{inode}:{length}`，inode 和 length 分别为此文件的 inode 号和大小，均以十进制字符串表示
  - Score：此文件加入集合的时间点

#### 1.1.15 DelSlices

- Key：`delSlices`
- Value Type：Hash
- Value：所有待清理的 Slices。在 Hash 中：
  - Key：`{id}_{ts}`，id 和 ts 分别为聚合后 Slice 的编号和加入列表的时间点，均以十进制字符串表示
  - Value：一个二进制编码的 Slices 列表，包含待删除的 Slices 信息。每个 Slice 对应 12 个字节：

```go
type Slice struct {
	id   uint64 // Slice 的 ID，全局唯一
	size uint32 // Slice 的总大小
}
```

#### 1.1.16 Sustained

- Key：`session{sid}`，sid 为会话 ID，以十进制字符串表示
- Value Type：List
- Value：此会话中所有待删除的文件列表。在 List 中：
  - Member：文件的 inode 号，以十进制字符串表示

### 1.2 SQL

元数据按类型存储在不同的表中，每张表命名时以 `jfs_` 开头，跟上其具体的表名，如 `jfs_chunk`。

#### Setting

#### Counter

#### Session2

#### Node

#### Edge

#### Chunk

```go
type chunk struct {
	Id     int64  `xorm:"pk bigserial"`
	Inode  Ino    `xorm:"unique(chunk) notnull"`
	Indx   uint32 `xorm:"unique(chunk) notnull"`
	Slices []byte `xorm:"blob notnull"`
}
```

其中 inode 和 index 与 Redis Chunk key 中的相同；Slices 是一段连续的 Buffer，每 24 个字节对应一个 Slice。具体编码方式与 Redis 中的相同。

#### ChunkRef

#### Symlink

#### Xattr

#### Flock

#### Plock

#### DelFiles

#### DelSlices

#### Sustained

### 1.3 TKV

#### Setting

#### Counter

#### Session2

#### Node

#### Edge

#### Chunk

Chunk key: `{prefix}A{inode}C{index}`，其中：

- prefix 是用来区分不同 volumes 的前缀，在创建文件系统时指定
- inode 是小端编码的 inode 号，占 8 个字节
- index 是大端编码的序号，占 4 个字节

通过 Get(key) 也是获取到一段连续且长度为 24 字节整数倍的 Buffer，内容与 SQL 中的一致。

#### ChunkRef

#### Symlink

#### Xattr

#### Flock

#### Plock

#### DelFiles

#### DelSlices

#### Sustained

## 2 数据对象

每个 Block 上传后即为对象存储中的一个对象，其命名格式为 `{fsname}/chunks/{hash}/{basename}`，其中：

- fsname 是文件系统名称
- “chunks” 为固定字符串，代表 JuiceFS 的数据对象
- hash 是根据 basename 算出来的哈希值，起到一定的隔离管理的作用
- basename 是对象的有效名称，格式为 `{sliceID}_{index}_{size}`，其中：
  - sliceID 为该对象所属 Slice 的 ID，JuiceFS 中每个 Slice 都有一个全局唯一的 ID
  - index 是该对象在所属 Slice 中的序号，一个 Slice 最多能拆成 16 个 Blocks，因此其取值范围为 [0, 16)
  - size 是该 Block 的大小，其取值范围为 (0, 4 MiB]

假设现在写入了一段连续的 10 MiB 数据，内部赋予的 SliceID 为 1，那么在对象存储中则会产生以下三个对象：

```
1_0_4194304
1_1_4194304
1_2_2097152
```

## 3 解析元数据和数据

### 3.1 根据路径查找文件

TODO

### 3.2 构建文件内容

上一节中，我们已经可以根据文件的路径找到此文件，并获取到其属性。根据文件属性中的 inode 和 size 字段，即可找到跟文件内容相关的元数据。现在假设有个文件的 inode 为 100，size 为 160 MiB，那么该文件一共有 `(size-1) / 64 MiB + 1 = 3` 个 Chunks，如下：

```
 File: |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _|
Chunk: |<---        Chunk 0        --->|<---        Chunk 1        --->|<-- Chunk 2 -->|
```

在单机 Redis 中，这意味着有 3 个 [Chunk Keys](#1.1.6-Chunk)，分别为 `c100_0`， `c100_1` 和 `c100_2`，每个 Key 对应一个 Slices 列表。这些 Slices 主要在数据写入时生成，可能互相之间有覆盖，也可能未完全填充满 Chunk。因此，在使用前需要顺序遍历这个 Slices 列表，并重新构建出最新版的数据分布，做到：

1. 有多个 Slice 覆盖的部分以最后加入的 Slice 为准
2. 没有被 Slice 覆盖的部分自动补零，用 sliceID = 0 来表示
3. 根据文件 size 截断 Chunk

现假设 Chunk 0 中有 3 个 Slices，分别为：

```go
Slice{pos: 10M, id: 10, size: 30M, off: 0, len: 30M}
Slice{pos: 20M, id: 11, size: 16M, off: 0, len: 16M}
Slice{pos: 16M, id: 12, size: 10M, off: 0, len: 10M}
```

图示如下（每个 '_' 表示 2 MiB）：

```
   Chunk: |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|
Slice 10:           |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _|
Slice 11:                     |_ _ _ _ _ _ _ _|
Slice 12:                 |_ _ _ _ _|

New List: |_ _ _ _ _|_ _ _|_ _ _ _ _|_ _ _ _ _|_ _|_ _ _ _ _ _ _ _ _ _ _ _|
               0      10      12         11    10             0
```

重构后的新列表包含且仅包含了此 Chunk 的最新数据分布，具体如下（去掉了 pos 参数，全部 Slices 按序连接即可）：
```go
Slice{id:  0, size: 10M, off:   0, len: 10M}
Slice{id: 10, size: 30M, off:   0, len:  6M}
Slice{id: 12, size: 10M, off:   0, len: 10M}
Slice{id: 11, size: 16M, off:  6M, len: 10M}
Slice{id: 10, size: 30M, off: 26M, len:  4M}
Slice{id:  0, size: 24M, off:   0, len: 24M} // 实际这一段也会省去
```

根据[数据对象命名规则](#2-数据对象)，即可得到此 Chunk 全 64MiB 的数据分布如下：

```
 0 ~ 10M: 补零
10 ~ 16M: 10_0_4194304, 10_1_4194304(0 ~ 2M)
16 ~ 26M: 12_0_4194304, 12_1_4194304, 12_2_2097152
26 ~ 36M: 11_1_4194304(2 ~ 4M), 11_2_4194304, 11_3_4194304
36 ~ 40M: 10_6_4194304(2 ~ 4M), 10_7_2097152
40 ~ 64M: 补零
```

据此，客户端可以快速找到应用所需数据。例如，在 offset 为 10MiB 位置读取 8MiB 数据，会涉及 3 个对象，具体为：

- 从 `10_0_4194304` 读取整个对象，对应读取数据的 0 ～ 4 MiB
- 从 `10_1_4194304` 读取 0 ～ 2 MiB，对应读取数据的 4 ～ 6 MiB
- 从 `12_0_4194304` 读取 0 ～ 2 MiB，对应读取数据的 6 ～ 8 MiB
