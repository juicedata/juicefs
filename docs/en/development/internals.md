---
title: Internals
sidebar_position: 4
slug: /internals
---

This article introduces implementation details of JuiceFS, use this as a reference if you'd like to contribute. The content below is based on JuiceFS v1.0.0, metadata version v1.

Before digging into source code, you should read [Data Processing Workflow](../introduction/io_processing.md).

## Keyword Definition

High level concepts:

- File system: i.e. JuiceFS Volume, represents a separate namespace. Files can be moved freely within the same file system, while data copies are required between different file systems.
- Metadata engine: A supported database instance of your choice, that stores and manages file system metadata. There are three categories of metadata engines currently supported by JuiceFS.
  - Redis: Redis and various protocol-compatible services
  - SQL: MySQL, PostgreSQL, SQLite, etc.
  - TKV: TiKV, BadgerDB, etc.
- Datastore: Object storage service that stores and manages file system data, such as Amazon S3, Aliyun OSS, etc. It can also be served by other storage systems that are compatible with object storage semantics, such as local file systems, Ceph RADOS, TiKV, etc.
- Client: can be in various forms, such as mount process, S3 gateway, WebDAV server, Java SDK, etc.
- File: refers to all types of files in general in this documentation, including regular files, directory files, link files, device files, etc.
- Directory: is a special kind of file used to organize the tree structure, and its contents are an index to a set of other files.

Low level concepts (learn more at [Data Processing Workflow](../introduction/io_processing.md)):

- Chunk: Logical concept, file is split into 64MiB chunks, allowing fast lookups during file reads;
- Slice: Logical concept, basic unit for file writes. Block's purpose is to improve read speed, and slice exists to improve file edits and random writes. All file writes are assigned a new or existing slice, and when file is read, what application sees is the consolidated view of all slices.
- Block: A chunk contains one or more blocks (4MiB by default), block is the basic storage unit in object storage. JuiceFS Client reads multiple blocks concurrently which greatly improves read performance. Apart from this, block is also the basic storage unit on disk cache, so this design improves cache eviction efficiency. Apart from this, block is immutable, all file edits is achieved through new blocks: after file edit, new blocks are uploaded to object storage, and new slices are appended to the slice list in the corresponding file metadata;

## Learn source code  {#source-code-structure}

Assuming you're already familiar with Go, as well as [JuiceFS architecture](https://juicefs.com/docs/community/architecture), this is the overall code structure:

* [`cmd`](https://github.com/juicedata/juicefs/tree/main/cmd) is the top-level entrance, all JuiceFS functionalities is rooted here, e.g. the `juicefs format` command resides in `cmd/format.go`；
* [`pkg`](https://github.com/juicedata/juicefs/tree/main/pkg) is actual implementation:
  * `pkg/fuse/fuse.go` provides abstract FUSE API;
  * `pkg/vfs` contains actual FUSE implementation, Metadata requests are handled in `pkg/meta`, read requests are handled in `pkg/vfs/reader.go` and write requests are handled by `pkg/vfs/writer.go`;
  * `pkg/meta` directory is the implementation of all metadata engines, where:
    * `pkg/meta/interface.go` is the interface definition for all types of metadata engines
    * `pkg/meta/redis.go` is the interface implementation of Redis database
    * `pkg/meta/sql.go` is the interface definition and general interface implementation of relational database, and the implementation of specific databases is in a separate file (for example, the implementation of MySQL is in `pkg/meta/sql_mysql.go`)
    * `pkg/meta/tkv.go` is the interface definition and general interface implementation of the KV database, and the implementation of a specific database is in a separate file (for example, the implementation of TiKV is in `pkg/meta/tkv_tikv.go`)
  * `pkg/object` contains all object storage integration code;
* [`sdk/java`](https://github.com/juicedata/juicefs/tree/main/sdk/java) is the Hadoop Java SDK, it uses `sdk/java/libjfs` through JNI.

## FUSE interface implementation {#fuse-interface-implementation}

JuiceFS implements a userspace file system based on [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) (Filesystem in Userspace), and the implementation library [`libfuse`](https://github.com/libfuse/libfuse) provides two APIs: high-level API and low-level API, where the high-level API is based on file name and path, and the low-level API is based on inode.

JuiceFS is implemented based on low-level API (in fact JuiceFS does not depend on `libfuse`, but [`go-fuse`](https://github.com/hanwen/go-fuse)), because this is the same set of APIs used by kernel VFS when interacting with FUSE. If JuiceFS were to use high level API, it'll have to implement the VFS tree within `libfuse`, and then expose path based API. This method works better for systems that already expose path based APIs (e.g. HDFS, S3). If metadata itself implements file / directory tree based on inode, the inode → path → inode conversions will have an impact on performance (this is the reason why FUSE API for HDFS doesn't perform well). JuiceFS Metadata directly implements file tree and API based on inode, so naturally it uses FUSE low level API.

## Metadata Structure

File systems are usually organized in a tree structure, where nodes represent files and edges represent directory containment relationships. There are more than ten metadata structures in JuiceFS. Most of them are used to maintain the organization of file tree and properties of individual nodes, while the rest are used to manage system configuration, client sessions, asynchronous tasks, etc. All metadata structures are described below.

### General Structure

#### Setting

It is created when the `juicefs format` command is executed, and some of its fields can be modified later by the `juicefs config` command. The structure is specified as follows.

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

- Name: name of the file system, specified by the user when formatting
- UUID: unique ID of the file system, automatically generated by the system when formatting
- Storage: short name of the object storage used to store data, such as `s3`, `oss`, etc.
- Bucket: the bucket path of the object storage
- AccessKey: access key used to access the object storage
- SecretKey: secret key used to access the object storage
- SessionToken: session token used to access the object storage, as some object storage supports the use of temporary token to obtain permission for a limited time
- BlockSize: size of the data block when splitting the file (the default is 4 MiB)
- Compression: compression algorithm that is executed before uploading data blocks to the object storage (the default is no compression)
- Shards: number of buckets in the object storage, only one bucket by default; when Shards > 1, data objects will be randomly hashed into Shards buckets
- HashPrefix: whether to set a hash prefix for the object name, false by default
- Capacity: quota limit for the total capacity of the file system
- Inodes: quota limit for the total number of files in the file system
- EncryptKey: the encrypted private key of the data object, which can be used only if the data encryption function is enabled
- KeyEncrypted: whether the saved key is encrypted or not, by default the SecretKey, EncryptKey and SessionToken will be encrypted
- TrashDays: number of days the deleted files are kept in trash, the default is 1 day
- MetaVersion: the version of the metadata structure, currently V1 (V0 and V1 are the same)
- MinClientVersion: the minimum client version allowed to connect, clients earlier than this version will be denied
- MaxClientVersion: the maximum client version allowed to connect
- EnableACL: enable ACL or not

This structure is serialized into JSON format and stored in the metadata engine.

#### Counter

Maintains the value of each counter in the system and the start timestamps of some background tasks, specifically

- usedSpace: used capacity of the file system
- totalInodes: number of used files in the file system
- nextInode: the next available inode number (in Redis, the maximum inode number currently in use)
- nextChunk: the next available sliceId (in Redis, the largest sliceId currently in use)
- nextSession: the maximum SID (sessionID) currently in use
- nextTrash: the maximum trash inode number currently in use
- nextCleanupSlices: timestamp of the last check on the cleanup of residual slices
- lastCleanupSessions: timestamp of the last check on the cleanup of residual stale sessions
- lastCleanupFiles: timestamp of the last check on the cleanup of residual files
- lastCleanupTrash: timestamp of the last check on the cleanup of trash

#### Session

Records the session IDs of clients connected to this file system and their timeouts. Each client sends a heartbeat message to update the timeout, and those who have not updated for a long time will be automatically cleaned up by other clients.

:::tip
Read-only clients cannot write to the metadata engine, so their sessions **will not** be recorded.
:::

#### SessionInfo

Records specific metadata of the client session so that it can be viewed with the `juicefs status` command. This is specified as

```go
type SessionInfo struct {
    Version    string // JuiceFS version
    HostName   string // Host name
    MountPoint string // path to mount point. S3 gateway and WebDAV server are "s3gateway" and "webdav" respectively
    ProcessID  int    // Process ID
}
```

This structure is serialized into JSON format and stored in the metadata engine.

#### Node

Records attribute information of each file, as follows

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

There are a few fields that need clarification.

- Atime/Atimensec: See [`--atime-mode`](../reference/command_reference.mdx#mount-metadata-options)
- Nlink
  - Directory file: initial value is 2 ('.' and '..'), add 1 for each subdirectory
  - Other files: initial value is 1, add 1 for each hard link created
- Length
  - Directory file: fixed at 4096
  - Soft link (symbolic link) file: the string length of the path to which the link points
  - Other files: the length of the actual content of the file

This structure is usually encoded in binary format and stored in the metadata engine.

#### Edges

Records information on each edge in the file tree, as follows

```
parentInode, name -> type, inode
```

where parentInode is the inode number of the parent directory, and the others are the name, type, and inode number of the child files, respectively.

#### LinkParent

Records the parent directory of some files. The parent directory of most files is recorded in the Parent field of the attribute; however, for files that have been created with hard links, there may be more than one parent directory, so the Parent field is set to 0, and all parent inodes are recorded independently, as follows

```
inode -> parentInode, links
```

where links is the count of the parentInode, because multiple hard links can be created in the same directory, and these hard links share one inode.

#### Chunk

Records information on each Chunk, as follows

```
inode, index -> []Slices
```

where inode is the inode number of the file to which the Chunk belongs, and index is the number of all Chunks in the file, starting from 0. The Chunk value is an array of Slices. Each Slice represents a piece of data written by the client, and is appended to this array in the order of writing time. When there is an overlap between different Slices, the later Slice is used.

```go
type Slice struct {
    Pos  uint32 // offset of the Slice in the Chunk
    ID   uint64 // ID of the Slice, globally unique
    Size uint32 // size of the Slice
    Off  uint32 // offset of valid data in this Slice
    Len  uint32 // size of valid data in this Slice
}
```

This structure is encoded and saved in binary format, taking up 24 bytes.

#### SliceRef {#sliceref}

Records the reference count of a Slice, as follows

```
sliceId, size -> refs
```

Since the reference count of most Slices is 1, to reduce the number of related entries in the database, the actual value minus 1 is used as the stored count value in Redis and TKV. In this way, most of the Slices have a refs value of 0, and there is no need to create related entries in the database.

#### Symlink

Records the location of the softlink file, as follows

```
inode -> target
```

#### Xattr

Records extended attributes (Key-Value pairs) of a file, as follows

```
inode, key -> value
```

#### Flock

Records BSD locks (flock) of a file, specifically.

```
inode, sid, owner -> ltype
```

where `sid` is the client session ID, `owner` is a string of numbers, usually associated with a process, and `ltype` is the lock type, which can be 'R' or 'W'.

#### Plock

Record POSIX record locks (fcntl) of a file, specifically

```
inode, sid, owner -> []plockRecord
```

Here plock is a more fine-grained lock that can only lock a certain segment of the file.

```go
type plockRecord struct {
    ltype uint32 // lock type
    pid   uint32 // process ID
    start uint64 // start position of the lock
    end   uint64 // end position of the lock
}
```

This structure is encoded and stored in binary format, taking up 24 bytes.

#### DelFiles

Records the list of files to be cleaned. It is needed as data cleanup of files is an asynchronous and potentially time-consuming operation that can be interrupted by other factors.

```
inode, length -> expire
```

where length is the length of the file and expire is the time when the file was deleted.

#### DelSlices

Records delayed deleted Slices. When the Trash feature is enabled, old Slices deleted by the Slice Compaction will be kept for the same amount of time as the Trash configuration, to be available for data recovery if necessary.

```
sliceId, deleted -> []slice
```

where sliceId is the ID of the new slice after compaction, deleted is the timestamp of the compaction, and the mapped value is the list of all old slices that were compacted. Each slice only encodes its ID and size.

```go
type slice struct {
    ID   uint64
    Size uint32
}
```

This structure is encoded and stored in binary format, taking up 12 bytes.

#### Sustained

Records the list of files that need to be kept temporarily during the session. If a file is still open when it is deleted, the data cannot be cleaned up immediately, but needs to be held temporarily until the file is closed.

```
sid -> []inode
```

where `sid` is the session ID and the mapped value is the list of temporarily undeleted file inodes.

### Redis

The common format of keys in Redis is `${prefix}${JFSKey}`, where

- In standalone mode the prefix is an empty string, while in cluster mode it is a database number enclosed in curly braces, e.g. "{10}"
- JFSKey is the Key of different data structures in JuiceFS, which are listed in the subsequent subsections

In Redis Keys, integers (including inode numbers) are represented as decimal strings if not otherwise specified.

#### Setting {#redis-setting}

- Key: `setting`
- Value Type: String
- Value: file system formatting information in JSON format

#### Counter

- Key: counter name
- Value Type: String
- Value: value of the counter, which is actually an integer

#### Session

- Key: `allSessions`
- Value Type: Sorted Set
- Value: all non-read-only sessions connected to this file system. In Set,
  - Member: session ID
  - Score: timeout point of this session

#### SessionInfo

- Key: `sessionInfos`
- Value Type: Hash
- Value: basic meta-information on all non-read-only sessions. In Hash,
  - Key: session ID
  - Value: session information in JSON format

#### Node {#redis-node}

- Key: `i${inode}`
- Value Type: String
- Value: binary encoded file attribute

#### Edge {#redis-edge}

- Key: `d${inode}`
- Value Type: Hash
- Value: all directory entries in this directory. In Hash,
  - Key: file name
  - Value: binary encoded file type and inode number

#### LinkParent

- Key: `p${inode}`
- Value Type: Hash
- Value: all parent inodes of this file. in Hash.
  - Key: parent inode
  - Value: count of this parent inode

#### Chunk {#redis-chunk}

- Key: `c${inode}_${index}`
- Value Type: List
- Value: list of Slices, each Slice is binary encoded with 24 bytes

#### SliceRef

- Key: `sliceRef`
- Value Type: Hash
- Value: the count value of all Slices to be recorded. In Hash,
  - Key: `k${sliceId}_${size}`
  - Value: reference count of this Slice minus 1 (if the reference count is 1, the corresponding entry is generally not created)

#### Symlink

- Key: `s${inode}`
- Value Type: String
- Value: path that the symbolic link points to

#### Xattr

- Key: `x${inode}`
- Value Type: Hash
- Value: all extended attributes of this file. In Hash,
  - Key: name of the extended attribute
  - Value: value of the extended attribute

#### Flock

- Key: `lockf${inode}`
- Value Type: Hash
- Value: all flocks of this file. In Hash,
  - Key: `${sid}_${owner}`, owner in hexadecimal
  - Value: lock type, can be 'R' or 'W'

#### Plock {#redis-plock}

- Key: `lockp${inode}`
- Value Type: Hash
- Value: all plocks of this file. In Hash,
  - Key: `${sid}_${owner}`, owner in hexadecimal
  - Value: array of bytes, where every 24 bytes corresponds to a [plockRecord](#plock)

#### DelFiles

- Key：`delfiles`
- Value Type: Sorted Set
- Value: list of all files to be cleaned. In Set,
  - Member: `${inode}:${length}`
  - Score: the timestamp when this file was added to the set

#### DelSlices {#redis-delslices}

- Key: `delSlices`
- Value Type: Hash
- Value: all Slices to be cleaned. In Hash,
  - Key: `${sliceId}_${deleted}`
  - Value: array of bytes, where every 12 bytes corresponds to a [slice](#delslices)

#### Sustained

- Key: `session${sid}`
- Value Type: List
- Value: list of files temporarily reserved in this session. In List,
  - Member: inode number of the file

### SQL

Metadata is stored in different tables by type, and each table is named with `jfs_` followed by its specific structure name to form the table name, e.g. `jfs_node`. Some tables use `Id` with the `bigserial` type as primary keys to ensure that each table has a primary key, and the `Id` columns do not contain actual information.

#### Setting {#sql-setting}

```go
type setting struct {
    Name  string `xorm:"pk"`
    Value string `xorm:"varchar(4096) notnull"`
}
```

There is only one entry in this table with "format" as Name and file system formatting information in JSON as Value.

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

There is no separate table for this, but it is recorded in the `Info` column of `session2`.

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

Most of the fields are the same as [Attr](#node), but the timestamp precision is lower, i.e., Atime/Mtime/Ctime are in microseconds.

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

There is no separate table for this. All `Parent`s are found based on the `Inode` index in `edge`.

#### Chunk {#sql-chunk}

```go
type chunk struct {
    Id     int64  `xorm:"pk bigserial"`
    Inode  Ino    `xorm:"unique(chunk) notnull"`
    Indx   uint32 `xorm:"unique(chunk) notnull"`
    Slices []byte `xorm:"blob notnull"`
}
```

Slices are an array of bytes, and each [Slice](#chunk) corresponds to 24 bytes.

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

Records is an array of bytes, and each [plockRecord](#plock) corresponds to 24 bytes.

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

Slices is an array of bytes, and each [slice](#delslices) corresponds to 12 bytes.

#### Sustained

```go
type sustained struct {
    Id    int64  `xorm:"pk bigserial"`
    Sid   uint64 `xorm:"unique(sustained) notnull"`
    Inode Ino    `xorm:"unique(sustained) notnull"`
}
```

### TKV

The common format of keys in TKV (Transactional Key-Value Database) is `${prefix}${JFSKey}`, where

- prefix is used to distinguish between different file systems, usually `${VolumeName}0xFD`, where `0xFD` is used as a special byte to handle cases when there is an inclusion relationship between different file system names. In addition, for databases that are not shareable (e.g. BadgerDB), the empty string is used as prefix.
- JFSKey is the JuiceFS Key for different data types, which is listed in the following subsections.

In TKV's Keys, all integers are stored in encoded binary form.

- inode and counter value occupy 8 bytes and are encoded with **small endian**.
- SID, sliceId and timestamp occupy 8 bytes and are encoded with **big endian**.

#### Setting {#tkv-setting}

```
setting -> file system formatting information in JSON format
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
SI${sid} -> session information in JSON format
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

where index takes up 4 bytes and is encoded with **big endian**. Slices is an array of bytes, one [Slice](#chunk) per 24 bytes.

#### SliceRef

```
K${sliceId}${size} -> counter value
```

where size takes up 4 bytes and is encoded with **big endian**.

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

where flocks is an array of bytes, one flock per 17 bytes.

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

where plocks is an array of bytes and the corresponding plock is variable-length.

```go
type plock struct {
    sid     uint64
    owner     uint64
    size     uint32
    records []byte
}
```

where size is the length of the records array and every 24 bytes in records corresponds to one [plockRecord](#plock).

#### DelFiles

```
D${inode}${length} -> timestamp
```

where length takes up 8 bytes and is encoded with **big endian**.

#### DelSlices {#tkv-delslices}

```
L${timestamp}${sliceId} -> slices
```

where slices is an array of bytes, and one [slice](#delslices) corresponds to 12 bytes.

#### Sustained

```
SS${sid}${inode} -> 1
```

Here the Value value is only used as a placeholder.

## File Data Format

### Finding files by path

According to the design of [Edge](#edges), only the direct children of each directory are recorded in the metadata engine. When an application provides a path to access a file, JuiceFS needs to look it up level by level. Now suppose the application wants to open the file `/dir1/dir2/testfile`, then it needs to

1. search for the entry with name "dir1" in the Edge structure of the root directory (inode number is fixed to 1) and get its inode number N1
2. search for the entry with the name "dir2" in the Edge structure of N1 and get its inode number N2
3. search for the entry with the name "testfile" in the Edge structure of N2, and get its inode number N3
4. search for the [Node](#node) structure corresponding to N3 to get the attributes of the file

Failure in any of the above steps will result in the file pointed to by that path not being found.

### File data splitting

From the previous section, we know how to find the file based on its path and get its attributes. The metadata related to the contents of the file can be found based on the inode and size fields in the file properties. Now suppose a file has an inode of 100 and a size of 160 MiB, then the file has `(size-1) / 64 MiB + 1 = 3` Chunks, as follows.

```
 File: |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|_ _ _ _ _ _ _ _|
Chunk: |<---        Chunk 0        --->|<---        Chunk 1        --->|<-- Chunk 2 -->|
```

In standalone Redis, this means that there are 3 [Chunk Keys](#chunk), i.e.,`c100_0`, `c100_1` and `c100_2`, each corresponding to a list of Slices. These Slices are mainly generated when the data is written and may overwrite each other or may not fill the Chunk completely, so you need to traverse this list of Slices sequentially and reconstruct the latest version of the data distribution before using it, so that

1. the part covered by more than one Slice is based on the last added Slice
2. the part that is not covered by Slice is automatically zeroed, and is represented by sliceId = 0
3. truncate Chunk according to file size

Now suppose there are 3 Slices in Chunk 0

```go
Slice{pos: 10M, id: 10, size: 30M, off: 0, len: 30M}
Slice{pos: 20M, id: 11, size: 16M, off: 0, len: 16M}
Slice{pos: 16M, id: 12, size: 10M, off: 0, len: 10M}
```

It can be illustrated as follows (each '_' denotes 2 MiB)

```
   Chunk: |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _|
Slice 10:           |_ _ _ _ _ _ _ _ _ _ _ _ _ _ _|
Slice 11:                     |_ _ _ _ _ _ _ _|
Slice 12:                 |_ _ _ _ _|

New List: |_ _ _ _ _|_ _ _|_ _ _ _ _|_ _ _ _ _|_ _|_ _ _ _ _ _ _ _ _ _ _ _|
               0      10      12         11    10             0
```

The reconstructed new list contains and only contains the latest data distribution for this Chunk as follows

```go
Slice{pos:   0, id:  0, size: 10M, off:   0, len: 10M}
Slice{pos: 10M, id: 10, size: 30M, off:   0, len:  6M}
Slice{pos: 16M, id: 12, size: 10M, off:   0, len: 10M}
Slice{pos: 26M, id: 11, size: 16M, off:  6M, len: 10M}
Slice{pos: 36M, id: 10, size: 30M, off: 26M, len:  4M}
Slice{pos: 40M, id:  0, size: 24M, off:   0, len: 24M} // can be omitted
```

### Data objects

#### Object naming {#object-storage-naming-format}

Block is the basic unit for JuiceFS to manage data. Its size is 4 MiB by default, and can be changed only when formatting a file system, within the interval [64 KiB, 16 MiB]. Each Block is an object in the object storage after upload, and is named in the format `${fsname}/chunks/${hash}/${basename}`, where

- fsname is the file system name
- "chunks" is a fixed string representing the data object of JuiceFS
- hash is the hash value calculated from basename, which plays a role in isolation management
- basename is the valid name of the object in the format of `${sliceId}_${index}_${size}`, where
  - sliceId is the ID of the Slice to which the object belongs, and each Slice in JuiceFS has a globally unique ID
  - index is the index of the object in the Slice it belongs to, by default a Slice can be split into at most 16 Blocks, so its value range is [0, 16)
  - size is the size of the Block, and by default it takes the value of (0, 4 MiB]

Currently there are two hash algorithms, and both use the sliceId in basename as the parameter. Which algorithm will be chosen to use follows the [HashPrefix](#setting) of the file system.

```go
func hash(sliceId int) string {
    if HashPrefix {
        return fmt.Sprintf("%02X/%d", sliceId%256, sliceId/1000/1000)
    }
    return fmt.Sprintf("%d/%d", sliceId/1000/1000, sliceId/1000)
}
```

Suppose a file system named `jfstest` is written with a continuous 10 MiB of data and internally given a SliceID of 1 with HashPrefix disabled, then the following three objects will be generated in the object storage.

```
jfstest/chunks/0/0/1_0_4194304
jfstest/chunks/0/0/1_1_4194304
jfstest/chunks/0/0/1_2_2097152
```

Similarly, now taking the 64 MiB chunk in the previous section as an example, its actual data distribution is as follows

```
 0 ~ 10M: Zero
10 ~ 16M: 10_0_4194304, 10_1_4194304(0 ~ 2M)
16 ~ 26M: 12_0_4194304, 12_1_4194304, 12_2_2097152
26 ~ 36M: 11_1_4194304(2 ~ 4M), 11_2_4194304, 11_3_4194304
36 ~ 40M: 10_6_4194304(2 ~ 4M), 10_7_2097152
40 ~ 64M: Zero
```

According to this, the client can quickly find the data needed for the application. For example, reading 8 MiB data at offset 10 MiB location will involve 3 objects, as follows

- Read the entire object from `10_0_4194304`, corresponding to 0 to 4 MiB of the read data
- Read 0 to 2 MiB from `10_1_4194304`, corresponding to 4 to 6 MiB of the read data
- Read 0 to 2 MiB from `12_0_4194304`, corresponding to 6 to 8 MiB of the read data

To facilitate obtaining the list of objects of a certain file, JuiceFS provides the `info` command, e.g. `juicefs info /mnt/jfs/test.tmp`.

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

The empty objectName in the table means a file hole and is read as 0. As you can see, the output is consistent with the previous analysis.

It is worth mentioning that the 'size' here is size of the original data in the Block, rather than that of the actual object in object storage. The original data is written directly to object storage by default, so the 'size' is equal to object size. However, when data compression or data encryption is enabled, the size of the actual object will change and may no longer be the same as the 'size'.

#### Data compression

You can configure the compression algorithm (supporting `lz4` and `zstd`) with the `--compress <value>` parameter when formatting a file system, so that all data blocks of this file system will be compressed before uploading to object storage. The object name remains the same as default, and the content is the result of the compression algorithm, without any other meta information. Therefore, the compression algorithm in the [file system formatting Information](#setting) is not allowed to be modified, otherwise it will cause the failure of reading existing data.

#### Data encryption

The RSA private key can be configured to enable [static data encryption](../security/encryption.md) when formatting a file system with the `--encrypt-rsa-key <value>` parameter, which allows all data blocks of this file system to be encrypted before uploading to the object storage. The object name is still the same as default, while its content becomes a header plus the result of the data encryption algorithm. The header contains a random seed and the symmetric key used for decryption, and the symmetric key itself is encrypted with the RSA private key. Therefore, it is not allowed to modify the RSA private key in the [file system formatting Information](#setting), otherwise reading existing data will fail.

:::note
If both compression and encryption are enabled, the original data will be compressed and then encrypted before uploading to the object storage.
:::
