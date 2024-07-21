---
title: 内部实现
sidebar_position: 3
slug: /internals
---

本文介绍 JuiceFS 的实现细节，用来为开发者了解和贡献开源代码作参考。其中内容对应的 JuiceFS 代码版本为 v1.0.0，元数据版本为 v1。

在深入学习源码前，我们还推荐阅读：

* [JuiceFS 读写请求处理流程](../introduction/io_processing.md)
* 网易存储团队的工程师写的这几篇博客（注意文章内容可能与最新版本代码有出入，一切请以代码为准）：[JuiceFS 调研（基于开源版本代码）](https://aspirer.wang/?p=1560)、[JuiceFS 源码阅读 - 上](https://mp.weixin.qq.com/s/mdqFJLpaJ249rUUEnRiP3Q)、[JuiceFS 源码阅读 - 中](https://mp.weixin.qq.com/s/CLQbQ-cLLGFsShPKUrCUJg)。

## 关键词定义

高层概念：

- 文件系统（File System）：即 JuiceFS Volume，代表一个独立的命名空间。文件在同文件系统内可自由移动，不同文件系统之间则需要数据拷贝；
- 元数据引擎（Metadata Engine）：用来存储和管理文件系统元数据的组件，通常由支持事务的数据库担任。目前已支持的元数据引擎共有三大类：
  - Redis：Redis 及各种协议兼容的服务；
  - SQL：MySQL、PostgreSQL、SQLite 等；
  - TKV：TiKV、BadgerDB、etcd 等。
- 数据存储：用来存储和管理文件系统数据的组件，通常由对象存储担任，如 Amazon S3、Aliyun OSS 等；也可由能兼容对象存储语义的其他存储系统担任，如本地文件系统、Ceph RADOS、TiKV 等；
- JuiceFS 客户端（JuiceFS Client）：有多种形式，如挂载进程、S3 网关、WebDAV 服务器、Java SDK 等；
- 文件：本文中泛指所有类型的文件，包括普通文件、目录文件、链接文件、设备文件等；
- 目录：一种特殊的文件，用来组织文件树型结构，其内容是一组其他文件的索引。

底层概念（详见 [JuiceFS 读写请求处理流程](../introduction/io_processing.md)）：

- Chunk：对文件分割的逻辑单位，大小 64MiB。Chunk 的存在让 JuiceFS 在读取大文件时能快速定位，提升读取性能；
- Slice：数据写入的逻辑单位，每一次写入都会分配一个已有或新的 Slice，而在元数据中则在 Chunk 下维护着 Chunk Slice 列表。
- Block：文件分割后的实际最小存储单位，默认大小 4MiB。一个 Chunk 包含一个或多个 Slice，而一个 Slice 又包含一个或多个 Block。

## 代码结构 {#source-code-structure}

[JuiceFS 源码](https://github.com/juicedata/juicefs)的大体结构如下：

* [`cmd`](https://github.com/juicedata/juicefs/tree/main/cmd) 是代码结构总入口，所有相关功能都能在此找到入口，如 `juicefs format` 命令对应着 `cmd/format.go`；
* [`pkg`](https://github.com/juicedata/juicefs/tree/main/pkg) 是具体实现，核心逻辑都在其中：
  * `pkg/fuse/fuse.go` 是 FUSE 实现的入口，提供抽象 FUSE 接口；
  * `pkg/vfs` 是具体的 FUSE 接口实现，元数据请求会调用 `pkg/meta` 中的实现，读请求会调用 `pkg/vfs/reader.go`，写请求会调用 `pkg/vfs/writer.go`；
  * `pkg/meta` 目录中是所有元数据引擎的实现，其中：
    * `pkg/meta/interface.go` 是所有类型元数据引擎的接口定义
    * `pkg/meta/redis.go` 是 Redis 数据库的接口实现
    * `pkg/meta/sql.go` 是关系型数据库的接口定义及通用接口实现，特定数据库的实现在单独文件中（如 MySQL 的实现在 `pkg/meta/sql_mysql.go`）
    * `pkg/meta/tkv.go` 是 KV 类数据库的接口定义及通用接口实现，特定数据库的实现在单独文件中（如 TiKV 的实现在 `pkg/meta/tkv_tikv.go`）
  * `pkg/object` 是与各种对象存储对接的实现。
* [`sdk/java`](https://github.com/juicedata/juicefs/tree/main/sdk/java) 是 Hadoop Java SDK 的实现，底层依赖 `sdk/java/libjfs` 这个库（通过 JNI 调用）。

## FUSE 接口实现 {#fuse-interface-implementation}

JuiceFS 基于 [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace)（Filesystem in Userspace）实现了一个用户态文件系统，FUSE 接口在 Linux 系统中的实现库 [`libfuse`](https://github.com/libfuse/libfuse) 提供两种 API：high-level API 和 low-level API，其中 high-level API 基于文件名和路径，low-level API 基于 inode。

JuiceFS 基于 low-level API 实现（事实上 JuiceFS 不依赖 `libfuse`，而是 [`go-fuse`](https://github.com/hanwen/go-fuse)），这是因为内核的 VFS 跟 FUSE 库交互就使用 low-level API。如果使用 high-level API 的话，其实是在 `libfuse` 内部做了 VFS 树的模拟，然后对外暴露基于路径的 API，这种模式适合元数据本身是基于路径提供的 API 的系统，比如 HDFS 或者 S3 之类。而如果元数据本身也是基于 inode 的目录树，这种 inode → path → inode 的反复转换就会影响性能（所以 HDFS 的 FUSE 接口实现性能都不好）。JuiceFS 的元数据是按照 inode 组织的，也直接提供基于 inode 的 API，那么使用 FUSE 的 low-level API 就非常简单和自然，性能也很好。

## 元数据结构

文件系统通常组织成树型结构，其中节点代表文件，边代表目录的包含关系。文件无法悬空停留，其（根目录除外）必然属于某个目录；目录可以包含一个或多个子文件。JuiceFS 中一共有十多种元数据结构，其中大部分用来维护文件树的组织关系和各个节点的属性，其余的用来管理系统配置，客户端会话和异步任务等。以下具体介绍所有的元数据结构。

### 通用结构

#### Setting

保存文件系统的格式化信息，在执行 `juicefs format` 命令时创建，后续可通过 `juicefs config` 命令修改其中的部分字段。结构具体如下：

```go
type Format struct {
    Name             string
    UUID             string
    Storage          string
    Bucket           string
    AccessKey        string `json:",omitempty"`
    SecretKey        string `json:",omitempty"`
    SessionToken     string `json:",omitempty"`
    BlockSize        int
    Compression      string `json:",omitempty"`
    Shards           int    `json:",omitempty"`
    HashPrefix       bool   `json:",omitempty"`
    Capacity         uint64 `json:",omitempty"`
    Inodes           uint64 `json:",omitempty"`
    EncryptKey       string `json:",omitempty"`
    KeyEncrypted     bool   `json:",omitempty"`
    TrashDays        int    `json:",omitempty"`
    MetaVersion      int    `json:",omitempty"`
    MinClientVersion string `json:",omitempty"`
    MaxClientVersion string `json:",omitempty"`
    EnableACL        bool
}
```

- Name：文件系统名称，在格式化时由用户指定
- UUID：文件系统的唯一 ID，在格式化时由系统自动生成
- Storage：用来保存数据的对象存储简称，如 `s3`、`oss` 等
- Bucket：对象存储的桶路径
- AccessKey：用来访问对象存储的 access key
- SecretKey：用来访问对象存储的 secret key
- SessionToken：用来访问对象存储的 session token，部分对象存储支持使用临时的 token 以获得有限时间的权限
- BlockSize：存储文件时拆分成的数据块大小，默认为 4 MiB
- Compression：数据块上传到对象存储前执行的压缩算法，默认为不压缩
- Shards：对象存储中分片桶的个数，默认为只有一个桶；当 Shards > 1 时，数据对象会随机哈希到 Shards 个桶中
- HashPrefix：是否为对象名称设置一个散列的前缀，默认为不设置
- Capacity：文件系统的总容量配额限制
- Inodes：文件系统的总文件数配额限制
- EncryptKey：数据对象的加密私钥，只要在开启了数据加密功能后才有用
- KeyEncrypted：保存的密钥是否处于加密状态，默认会将 SecretKey、EncryptKey 和 SessionToken 加密保存
- TrashDays：文件在回收站中被保留的天数，默认为 1 天
- MetaVersion：元数据结构的版本，目前为 V1（V0 和 V1 相同）
- MinClientVersion：允许连接的最小客户端版本，早于此版本的客户端会被拒绝连接
- MaxClientVersion：允许连接的最大客户端版本
- EnableACL: 是否开启 ACL 功能

此结构会序列化成 JSON 格式保存在元数据引擎中。

#### Counter

维护系统中的各个计数器值和一些后台任务的启动时间戳，具体有：

- usedSpace：文件系统的已使用容量
- totalInodes：文件系统的已使用文件数
- nextInode：下一个可用的 inode 号（Redis 中为当前已用的最大 inode 号）
- nextChunk：下一个可用的 sliceId（Redis 中为当前已用的最大 sliceId）
- nextSession：当前已用的最大 SID（sessionID）
- nextTrash：当前已用的最大 trash inode 号
- nextCleanupSlices：上一次检查清理残留 slices 的时间点
- lastCleanupSessions：上一次检查清理残留 stale sessions 的时间点
- lastCleanupFiles：上一次检查清理残留文件的时间点
- lastCleanupTrash：上一次检查清理回收站的时间点

#### Session

记录连接到此文件系统的客户端会话 ID 和其超时时间。每个客户端会定时发送心跳消息以更新超时时间，长时间未更新者会被其他客户端自动清理。

:::tip 注意
只读客户端无法写入元数据引擎，因此其会话**不会**被记录。
:::

#### SessionInfo

记录客户端会话的具体元信息，使其可以通过 `juicefs status` 命令查看。具体为：

```go
type SessionInfo struct {
    Version    string // JuiceFS 版本
    HostName   string // 主机名称
    MountPoint string // 挂载点路径。S3 网关和 WebDAV 服务分别为 "s3gateway" 和 "webdav"
    ProcessID  int    // 进程 ID
}
```

此结构会序列化成 JSON 格式保存在元数据引擎中。

#### Node

记录每个文件的属性信息，具体为：

```go
type Attr struct {
    Flags     uint8  // reserved flags
    Typ       uint8  // type of a node
    Mode      uint16 // permission mode
    Uid       uint32 // owner id
    Gid       uint32 // group id of owner
    Rdev      uint32 // device number
    Atime     int64  // last access time
    Mtime     int64  // last modified time
    Ctime     int64  // last change time for meta
    Atimensec uint32 // nanosecond part of atime
    Mtimensec uint32 // nanosecond part of mtime
    Ctimensec uint32 // nanosecond part of ctime
    Nlink     uint32 // number of links (sub-directories or hardlinks)
    Length    uint64 // length of regular file

    Parent    Ino  // inode of parent; 0 means tracked by parentKey (for hardlinks)
    Full      bool // the attributes are completed or not
    KeepCache bool // whether to keep the cached page or not

    AccessACL  uint32 // access ACL id (identical ACL rules share the same access ACL ID.)
    DefaultACL uint32 // default ACL id (default ACL and the access ACL share the same cache and store)
}
```

其中几个需要说明的字段：

- Atime/Atimensec：参考 [`--atime-mode`](../reference/command_reference.mdx#mount-metadata-options)
- Nlink：
  - 目录文件：初始值为 2（'.' 和 '..'），每有一个子目录 Nlink 值加 1
  - 其他文件：初始值为 1，每创建一个硬链接 Nlink 值加 1
- Length：
  - 目录文件：固定为 4096
  - 软链接（symbolic link）文件：为链接指向路径的字符串长度
  - 其他文件：为文件实际内容的长度

此结构一般会编码成二进制格式保存在元数据引擎中。

#### Edge

记录文件树中每条边的信息，具体为：

```
parentInode, name -> type, inode
```

其中 parentInode 是父目录的 inode 号，其他分别为子文件的名称、类型和 inode 号。

#### LinkParent

记录部分文件的父目录。绝大部分文件的父目录记在其属性的 Parent 字段中；但对于创建过硬链接的文件，其父目录可能有多个，此时会将 Parent 字段置 0，同时独立记录其所有父目录 inodes，具体为：

```
inode -> parentInode, links
```

其中 links 是 parentInode 的计数，因为一个目录中可以创建多个硬链接，这些硬连接共享 inode。

#### Chunk

记录每个 Chunk 的信息，具体为：

```
inode, index -> []Slices
```

其中 inode 是此 Chunk 所属文件的 inode 号，index 是其在这个文件所有 Chunks 中序号，从 0 开始。Chunk 值内容为一个 Slices 数组，每个 Slice 代表一段客户端写入的数据，并且按写入时间顺序 append 到这个数组中。当不同 Slices 之间有重叠时，以后加入的 Slice 为准。Slice 的具体结构为：

```go
type Slice struct {
    Pos  uint32 // Slice 在 Chunk 中的偏移位置
    ID   uint64 // Slice 的 ID，全局唯一
    Size uint32 // Slice 的总大小
    Off  uint32 // 有效数据在此 Slice 中的偏移位置
    Len  uint32 // 有效数据在此 Slice 中的大小
}
```

此结构会编码成二进制格式保存，占 24 个字节。

#### SliceRef

记录 Slice 的引用计数，具体为：

```
sliceId, size -> refs
```

由于绝大部分 Slice 的引用计数均为 1，为减少数据库中相关 entry 数量，在 Redis 和 TKV 中以实际值减 1 作为存储的计数值。这样，大部分的 Slice 对应 refs 值为 0，则不必在数据库中创建相关 entry。

#### Symlink

记录软链接文件的指向位置，具体为：

```
inode -> target
```

#### Xattr

记录文件相关的扩展属性（Key-Value 对），具体为：

```
inode, key -> value
```

#### Flock

记录文件相关的 BSD locks（flock），具体为：

```
inode, sid, owner -> ltype
```

其中 `sid` 为客户端会话 ID，`owner` 为一串数字，通常与进程相关联；`ltype` 为锁类型，可以为 'R' 或者 'W'。

#### Plock

记录文件相关的 POSIX record locks（fcntl），具体为：

```
inode, sid, owner -> []plockRecord
```

这里 plock 是一种更细粒度的锁，可以只锁定文件中的某一片段：

```go
type plockRecord struct {
    ltype uint32 // 锁类型
    pid   uint32 // 进程 ID
    start uint64 // 锁起始位置
    end   uint64 // 锁结束位置
}
```

此结构会编码成二进制格式保存，占 24 个字节。

#### DelFiles

记录待清理的文件列表。由于文件的数据清理是一个异步且可能长耗时的操作，可能被其他因素中断，因此会由此列表进行跟踪：

```
inode, length -> expire
```

其中 length 为文件长度，expire 为文件被删除的时间。

#### DelSlices

记录延迟删除的 Slices。当回收站功能开启时，因 Slice Compaction 功能删除的旧 Slices 会被保留与回收站配置相同的时间，以被在必要时可用来恢复数据。其内容为：

```
sliceId, deleted -> []slice
```

其中 sliceId 为 compact 后新 Slice 的 ID，deleted 为 compact 完成的时间戳，映射值为被 compacted 的所有旧 slice 列表，每个 slice 仅编码了 ID 和 size 信息：

```go
type slice struct {
    ID   uint64
    Size uint32
}
```

此结构会编码成二进制格式保存，占 12 个字节。

#### Sustained

记录会话中需临时保留的文件列表。当文件被删除时若其仍处于打开状态，则不能立即清理数据，而需要暂时保留直至其被关闭。

```
sid -> []inode
```

其中 `sid` 为会话 ID，映射值为暂时未删除的文件 inodes 列表。

### Redis

Redis 中 Key 的通用格式为 `${prefix}${JFSKey}`，其中：

- 在 Redis 非集群模式下 prefix 为空字符串，在集群模式中是一个大括号括起来的数据库编号，如 "{10}"
- JFSKey 是指 JuiceFS 不同数据结构的 Key，具体列举在后续小节中

在 Redis 的 Keys 中，如无特殊说明整数（包括 inode 号）都以十进制字符串表示。

#### Setting {#redis-setting}

- Key：`setting`
- Value Type：String
- Value：JSON 格式的文件系统格式化信息

#### Counter

- Key：计数器名称
- Value Type：String
- Value：计数器的值，实际均为整数

#### Session

- Key：`allSessions`
- Value Type：Sorted Set
- Value：所有连接此文件系统的非只读会话。在 Set 中：
  - Member：会话 ID
  - Score：此会话超时的时间点

#### SessionInfo

- Key：`sessionInfos`
- Value Type：Hash
- Value：所有非只读会话的基本元信息。在 Hash 中：
  - Key：会话 ID
  - Value：JSON 格式的会话信息

#### Node {#redis-node}

- Key：`i${inode}`
- Value Type：String
- Value：二进制编码的文件属性

#### Edge {#redis-edge}

- Key：`d${inode}`
- Value Type：Hash
- Value：此目录下的所有目录项。在 Hash 中：
  - Key：文件名称
  - Value：二进制编码的文件类型和 inode 号

#### LinkParent

- Key：`p${inode}`
- Value Type：Hash
- Value：此文件的所有父目录 inodes。在 Hash 中：
  - Key：父目录 inode
  - Value：此父目录 inode 的计数

#### Chunk {#redis-chunk}

- Key：`c${inode}_${index}`
- Value Type：list
- Value：Slices 列表，每个 Slice 均以二进制编码，各占 24 个字节

#### SliceRef {#sliceref}

- Key：`sliceRef`
- Value Type：Hash
- Value：所有需记录的 Slices 的计数值。在 Hash 中：
  - Key：`k${sliceId}_${size}`
  - Value：此 Slice 的引用计数值减 1（若引用计数为 1，则一般不创建对应 entry）

#### Symlink

- Key：`s${inode}`
- Value Type：String
- Value：符号链接指向的路径

#### Xattr

- Key：`x${inode}`
- Value Type：Hash
- Value：此文件的所有扩展属性。在 Hash 中：
  - Key：扩展属性名称
  - Value：扩展属性值

#### Flock

- Key：`lockf${inode}`
- Value Type：Hash
- Value：此文件的所有 flocks。在 Hash 中：
  - Key：`${sid}_${owner}`，owner 以十六进制表示
  - Value：锁类型，可能为 'R' 或者 'W'

#### Plock {#redis-plock}

- Key：`lockp${inode}`
- Value Type：Hash
- Value：此文件的所有 plocks。在 Hash 中：
  - Key：`${sid}_${owner}`，owner 以十六进制表示
  - Value：字节数组，其中每 24 字节对应一个 [plockRecord](#plock)

#### DelFiles

- Key：`delfiles`
- Value Type：Sorted Set
- Value：所有待清理的文件列表。在 Set 中：
  - Member：`${inode}:${length}`
  - Score：此文件加入集合的时间点

#### DelSlices {#redis-delslices}

- Key：`delSlices`
- Value Type：Hash
- Value：所有待清理的 Slices。在 Hash 中：
  - Key：`${sliceId}_${deleted}`
  - Value：字节数组，其中每 12 字节对应一个 [slice](#delslices)

#### Sustained

- Key：`session${sid}`
- Value Type：List
- Value：此会话中临时保留的文件列表。在 List 中：
  - Member：文件的 inode 号

### SQL

元数据按类型存储在不同的表中，每张表命名时以 `jfs_` 开头，跟上其具体的结构体名称组成表名，如 `jfs_node`。部分表中加入了 `bigserial` 类型的 `Id` 列作为主键，其仅用来确保每张表中都有主键，并不包含实际信息。

#### Setting {#sql-setting}

```go
type setting struct {
    Name  string `xorm:"pk"`
    Value string `xorm:"varchar(4096) notnull"`
}
```

固定只有一条 entry，Name 为 "format"，Value 为 JSON 格式的文件系统格式化信息。

#### Counter

```go
type counter struct {
    Name  string `xorm:"pk"`
    Value int64  `xorm:"notnull"`
}
```

#### Session

```go
type session2 struct {
    Sid    uint64 `xorm:"pk"`
    Expire int64  `xorm:"notnull"`
    Info   []byte `xorm:"blob"`
}
```

#### SessionInfo

没有独立的表，而是记在 `session2` 的 `Info` 列中。

#### Node {#sql-node}

```go
type node struct {
    Inode  Ino    `xorm:"pk"`
    Type   uint8  `xorm:"notnull"`
    Flags  uint8  `xorm:"notnull"`
    Mode   uint16 `xorm:"notnull"`
    Uid    uint32 `xorm:"notnull"`
    Gid    uint32 `xorm:"notnull"`
    Atime  int64  `xorm:"notnull"`
    Mtime  int64  `xorm:"notnull"`
    Ctime  int64  `xorm:"notnull"`
    Nlink  uint32 `xorm:"notnull"`
    Length uint64 `xorm:"notnull"`
    Rdev   uint32
    Parent Ino
    AccessACLId  uint32 `xorm:"'access_acl_id'"`
    DefaultACLId uint32 `xorm:"'default_acl_id'"`
}
```

大部分字段与 [Attr](#node) 相同，但时间戳使用了较低精度，其中 Atime/Mtime/Ctime 的单位为微秒。

#### Edge {#sql-edge}

```go
type edge struct {
    Id     int64  `xorm:"pk bigserial"`
    Parent Ino    `xorm:"unique(edge) notnull"`
    Name   []byte `xorm:"unique(edge) varbinary(255) notnull"`
    Inode  Ino    `xorm:"index notnull"`
    Type   uint8  `xorm:"notnull"`
}
```

#### LinkParent

没有独立的表，而是根据 `edge` 中的 `Inode` 索引找到所有 `Parent`。

#### Chunk {#sql-chunk}

```go
type chunk struct {
    Id     int64  `xorm:"pk bigserial"`
    Inode  Ino    `xorm:"unique(chunk) notnull"`
    Indx   uint32 `xorm:"unique(chunk) notnull"`
    Slices []byte `xorm:"blob notnull"`
}
```

Slices 是一段字节数组，每 24 字节对应一个 [Slice](#chunk)。

#### SliceRef

```go
type sliceRef struct {
    Id   uint64 `xorm:"pk chunkid"`
    Size uint32 `xorm:"notnull"`
    Refs int    `xorm:"notnull"`
}
```

#### Symlink

```go
type symlink struct {
    Inode  Ino    `xorm:"pk"`
    Target []byte `xorm:"varbinary(4096) notnull"`
}
```

#### Xattr

```go
type xattr struct {
    Id    int64  `xorm:"pk bigserial"`
    Inode Ino    `xorm:"unique(name) notnull"`
    Name  string `xorm:"unique(name) notnull"`
    Value []byte `xorm:"blob notnull"`
}
```

#### Flock

```go
type flock struct {
    Id    int64  `xorm:"pk bigserial"`
    Inode Ino    `xorm:"notnull unique(flock)"`
    Sid   uint64 `xorm:"notnull unique(flock)"`
    Owner int64  `xorm:"notnull unique(flock)"`
    Ltype byte   `xorm:"notnull"`
}
```

#### Plock {#sql-plock}

```go
type plock struct {
    Id      int64  `xorm:"pk bigserial"`
    Inode   Ino    `xorm:"notnull unique(plock)"`
    Sid     uint64 `xorm:"notnull unique(plock)"`
    Owner   int64  `xorm:"notnull unique(plock)"`
    Records []byte `xorm:"blob notnull"`
}
```

Records 是一段字节数组，每 24 字节对应一个 [plockRecord](#plock)。

#### DelFiles

```go
type delfile struct {
    Inode  Ino    `xorm:"pk notnull"`
    Length uint64 `xorm:"notnull"`
    Expire int64  `xorm:"notnull"`
}
```

#### DelSlices {#sql-delslices}

```go
type delslices struct {
    Id      uint64 `xorm:"pk chunkid"`
    Deleted int64  `xorm:"notnull"`
    Slices  []byte `xorm:"blob notnull"`
}
```

Slices 是一段字节数组，每 12 字节对应一个 [slice](#delslices)。

#### Sustained

```go
type sustained struct {
    Id    int64  `xorm:"pk bigserial"`
    Sid   uint64 `xorm:"unique(sustained) notnull"`
    Inode Ino    `xorm:"unique(sustained) notnull"`
}
```

### TKV

TKV（Transactional Key-Value Database）中 Key 的通用格式为 `${prefix}${JFSKey}`，其中：

- prefix 用来区分不同的文件系统，通常是 `${VolumeName}0xFD`，其中的 `0xFD` 作为特殊字节用来处理不同文件系统名称间存在包含关系的情况。此外，对于无法公用的数据库（如 BadgerDB）则直接使用空字符串作前缀
- JFSKey 是指 JuiceFS 为不同数据类型设计的 Key，具体列举在后续小节中

在 TKV 的 Keys 中，所有整数都以编码后的二进制形式存储：

- inode 和 counter value 占 8 个字节，使用**小端**编码
- SID、sliceId 和 timestamp 占 8 个字节，使用**大端**编码

#### Setting {#tkv-setting}

```
setting -> JSON 格式的文件系统格式化信息
```

#### Counter

```
C${name} -> counter value
```

#### Session

```
SE${sid} -> timestamp
```

#### SessionInfo

```
SI${sid} -> JSON 格式的会话信息
```

#### Node {#tkv-node}

```
A${inode}I -> encoded Attr
```

#### Edge {#tkv-edge}

```
A${inode}D${name} -> encoded {type, inode}
```

#### LinkParent

```
A${inode}P${parentInode} -> counter value
```

#### Chunk {#tkv-chunk}

```
A${inode}C${index} -> Slices
```

其中 index 占 4 个字节，使用**大端**编码。Slices 是一段字节数组，每 24 字节对应一个 [Slice](#chunk)。

#### SliceRef

```
K${sliceId}${size} -> counter value
```

其中 size 占 4 个字节，使用**大端**编码。

#### Symlink

```
A${inode}S -> target
```

#### Xattr

```
A${inode}X${name} -> xattr value
```

#### Flock

```
F${inode} -> flocks
```

其中 flocks 是一段字节数组，每 17 字节对应一个 flock：

```go
type flock struct {
    sid   uint64
    owner uint64
    ltype uint8
}
```

#### Plock {#tkv-plock}

```
P${inode} -> plocks
```

其中 plocks 是一段字节数组，对应的 plock 是变长的：

```go
type plock struct {
    sid     uint64
    owner     uint64
    size     uint32
    records []byte
}
```

其中 size 是 records 数组的长度，records 中每 24 字节对应一个 [plockRecord](#plock)。

#### DelFiles

```
D${inode}${length} -> timestamp
```

其中 length 占 8 个字节，使用**大端**编码。

#### DelSlices {#tkv-delslices}

```
L${timestamp}${sliceId} -> slices
```

其中 slices 是一段字节数组，每 12 字节对应一个 [slice](#delslices)。

#### Sustained

```
SS${sid}${inode} -> 1
```

这里 Value 值仅用来占位。

## 4 文件数据格式

### 根据路径查找文件

根据 [Edge](#edge) 的设计，元数据引擎中只记录了每个目录的直接子节点。当应用提供一个路径来访问文件时，JuiceFS 需要逐级查找。现在假设应用想打开文件 `/dir1/dir2/testfile`，则需要：

1. 在根目录（Inode 号固定为 1）的 Edge 结构中搜寻 name 为 "dir1" 的 entry，得到其 inode 号 N1
2. 在 N1 的 Edge 结构中搜寻 name 为 "dir2" 的 entry，得到其 inode 号 N2
3. 在 N2 的 Edge 结构中搜寻 name 为 "testfile" 的 entry，得到其 inode 号 N3
4. 根据 N3 搜寻其对应的 [Node](#node) 结构，得到该文件的相关属性

在以上步骤中，任何一步搜寻失败都会导致该路径指向的文件未找到。

### 文件数据拆分

上一节中，我们已经可以根据文件的路径找到此文件，并获取到其属性。根据文件属性中的 inode 和 size 字段，即可找到跟文件内容相关的元数据。现在假设有个文件的 inode 为 100，size 为 160 MiB，那么该文件一共有 `(size-1) / 64 MiB + 1 = 3` 个 Chunks，如下：

```
 File: |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _|
Chunk: |<---        Chunk 0        --->|<---        Chunk 1        --->|<-- Chunk 2 -->|
```

在单机 Redis 中，这意味着有 3 个 [Chunk Keys](#chunk)，分别为 `c100_0`， `c100_1` 和 `c100_2`，每个 Key 对应一个 Slices 列表。这些 Slices 主要在数据写入时生成，可能互相之间有覆盖，也可能未完全填充满 Chunk。因此，在使用前需要顺序遍历这个 Slices 列表，并重新构建出最新版的数据分布，做到：

1. 有多个 Slice 覆盖的部分以最后加入的 Slice 为准
2. 没有被 Slice 覆盖的部分自动补零，用 sliceId = 0 来表示
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

重构后的新列表包含且仅包含了此 Chunk 的最新数据分布，具体如下：

```go
Slice{pos:   0, id:  0, size: 10M, off:   0, len: 10M}
Slice{pos: 10M, id: 10, size: 30M, off:   0, len:  6M}
Slice{pos: 16M, id: 12, size: 10M, off:   0, len: 10M}
Slice{pos: 26M, id: 11, size: 16M, off:  6M, len: 10M}
Slice{pos: 36M, id: 10, size: 30M, off: 26M, len:  4M}
Slice{pos: 40M, id:  0, size: 24M, off:   0, len: 24M} // 实际这一段也会省去
```

### 数据对象

#### 对象命名 {#object-storage-naming-format}

Block 是 JuiceFS 管理数据的基本单元，其大小默认为 4 MiB，且可在文件系统格式化时配置，允许调整的区间范围为 [64 KiB, 16 MiB]。每个 Block 上传后即为对象存储中的一个对象，其命名格式为 `${fsname}/chunks/${hash}/${basename}`，其中：

- fsname 是文件系统名称
- “chunks”为固定字符串，代表 JuiceFS 的数据对象
- hash 是根据 basename 算出来的哈希值，起到一定的隔离管理的作用
- basename 是对象的有效名称，格式为 `${sliceId}_${index}_${size}`，其中：
  - sliceId 为该对象所属 Slice 的 ID，JuiceFS 中每个 Slice 都有一个全局唯一的 ID
  - index 是该对象在所属 Slice 中的序号，默认一个 Slice 最多能拆成 16 个 Blocks，因此其取值范围为 [0, 16)
  - size 是该 Block 的大小，默认情况下其取值范围为 (0, 4 MiB]

目前使用的 hash 算法有两种，以 basename 中的 sliceId 为参数，根据文件系统格式化时的 [HashPrefix](#setting) 配置选择：

```go
func hash(sliceId int) string {
    if HashPrefix {
        return fmt.Sprintf("%02X/%d", sliceId%256, sliceId/1000/1000)
    }
    return fmt.Sprintf("%d/%d", sliceId/1000/1000, sliceId/1000)
}
```

假设一个名为 `jfstest` 的文件系统中写入了一段连续的 10 MiB 数据，内部赋予的 SliceID 为 1，且未开启 HashPrefix，那么在对象存储中则会产生以下三个对象：

```
jfstest/chunks/0/0/1_0_4194304
jfstest/chunks/0/0/1_1_4194304
jfstest/chunks/0/0/1_2_2097152
```

类似地，现在以上一节的 64 MiB 的 Chunk 为例，它的实际数据分布如下：

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

为方便直接查看文件内容对应的对象列表，JuiceFS 提供了 `info` 命令，如 `juicefs info /mnt/jfs/test.tmp`：

```bash
objects:
+------------+---------------------------------+----------+---------+----------+
| chunkIndex |            objectName           |   size   |  offset |  length  |
+------------+---------------------------------+----------+---------+----------+
|          0 |                                 | 10485760 |       0 | 10485760 |
|          0 | jfstest/chunks/0/0/10_0_4194304 |  4194304 |       0 |  4194304 |
|          0 | jfstest/chunks/0/0/10_1_4194304 |  4194304 |       0 |  2097152 |
|          0 | jfstest/chunks/0/0/12_0_4194304 |  4194304 |       0 |  4194304 |
|          0 | jfstest/chunks/0/0/12_1_4194304 |  4194304 |       0 |  4194304 |
|          0 | jfstest/chunks/0/0/12_2_2097152 |  2097152 |       0 |  2097152 |
|          0 | jfstest/chunks/0/0/11_1_4194304 |  4194304 | 2097152 |  2097152 |
|          0 | jfstest/chunks/0/0/11_2_4194304 |  4194304 |       0 |  4194304 |
|          0 | jfstest/chunks/0/0/11_3_4194304 |  4194304 |       0 |  4194304 |
|          0 | jfstest/chunks/0/0/10_6_4194304 |  4194304 | 2097152 |  2097152 |
|          0 | jfstest/chunks/0/0/10_7_2097152 |  2097152 |       0 |  2097152 |
|        ... |                             ... |      ... |     ... |      ... |
+------------+---------------------------------+----------+---------+----------+
```

表中空的 objectName 表示文件空洞，读取时均为 0。可以看到，输出结果与之前分析一致。

值得一提的是，这里的 size 是 Block 中原始数据的大小，而不是对象存储中实际对象的大小。默认情况下，原始数据拆分后直接写到对象存储，此时 size 与对象大小是相等的。但当开启了数据压缩或数据加密功能后，实际对象的大小会发生变化，此时其与 size 很可能不再相同。

#### 数据压缩

在文件系统格式化时可以通过 `--compress <value>` 参数配置压缩算法（支持 LZ4 和 zstd），使得此文件系统的所有数据 Block 会经过压缩后再上传到对象存储。此时对象名称仍与默认配置相同，且内容为原始数据经压缩算法后的结果，不携带任何其它元信息。因此，文件[文统格式化信息](#setting)中的压缩算法不允许修改，否则会导致读取已有数据失败。

#### 数据加密

在文件系统格式化时可以通过 `--encrypt-rsa-key <value>` 参数配置 RSA 私钥以开启[静态数据加密](../security/encryption.md)功能，使得此文件系统的所有数据 Block 会经过加密后再上传到对象存储。此时对象名称仍与默认配置相同，内容为一段 header 加上数据经加密算法后的结果。这段 header 里记录了用来解密的对称密钥以及随机种子，而对称密钥本身又经过 RSA 私钥加密。因此，文件[文统格式化信息](#setting)中的 RSA 私钥目前不允许修改，否则会导致读取已有数据失败。

:::note 备注
若同时开启压缩和加密，原始数据会先压缩再加密后上传到对象存储。
:::
