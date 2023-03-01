# Count space and inodes usage for each directory

## Background

Currently, we have several counters to globally count the used space and inodes, which can be used to show information or set quota. However, we do not have efficient ways to show used information of or set quota for each directory.

## Proposal

This document give a proposal to efficiently and almost immediately collect used space and inodes for each directory. The "efficiently" means this operation cannot affect the performance of normal IO operations like `mknod`, `write` .etc. And the "almost immediately" means this operation cannot be lazy or scheduled, we must update the used space and inodes actively, but there may be a little latency (between several seconds and 1 minute).

## Implementation

### Storage

The counters should be stored in meta engines, in this section we introduce how to store them in three kinds of meta engines.

#### Redis

Redis engine stores the counters in hashes.

```go
func (m *redisMeta) dirUsedSpaceKey() string {
    return m.prefix + "dirUsedSpace"
}
 
func (m *redisMeta) dirUsedInodesKey() string {
    return m.prefix + "dirUsedInodes"
}
```

#### SQL

SQL engine stores the counters in a table.

```go
type dirUsage struct {
    Inode       Ino    `xorm:"pk"`
    UsedSpace   uint64 `xorm:"notnull"`
    UsedInodes  uint64 `xorm:"notnull"`
}
```

#### TKV

TKV engine stores each counter in one key.

```go
func (m *kvMeta) dirUsageKey(inode Ino) []byte {
    return m.fmtKey("U", inode)
}
```

### Usage

In this section we represent how and when to update and read the counters.

#### Update

The are several file types among the children, we should clarify how to deal with each kinds of files first.

| Type          | Used space      | Used inodes |
| ------------- | --------------- | ----------- |
| Normal file   | `align4K(size)` | 1           |
| Directory     | 4KiB            | 1           |
| Symlink       | 4KiB            | 1           |
| FIFO          | 4KiB            | 1           |
| Block device  | 4KiB            | 1           |
| Char device   | 4KiB            | 1           |
| Socket        | 4KiB            | 1           |

Each meta engine should implement `doUpdateDirUsage`.

```go
type engine interface {
    ...
    doUpdateDirUsage(ctx Context, ino Ino, space int64, inodes int64)
}
```

Relevant IO operations should call `doUpdateDirUsage` asynchronously.

```go
func (m *baseMeta) Mknod(ctx Context, parent Ino, ...) syscall.Errno {
    ...
    err := m.en.doMknod(ctx, m.checkRoot(parent), ...)
    ...
    go m.en.doUpdateDirUsage(ctx, parent, 1<<12, 1)
    return err
}

func (m *baseMeta) Unlink(ctx Context, parent Ino, name string) syscall.Errno {
    ...
    err := m.en.doUnlink(ctx, m.checkRoot(parent), name)
    ...
    go m.en.doUpdateDirUsage(ctx, parent, -align4K(attr.size), -1)
    return err
}
```

#### Read

Each meta engine should implement `doGetDirUsage`.

```go
type engine interface {
    ...
    doGetDirUsage(ctx Context, ino Ino) (space, inodes uint64, err syscall.Errno)
}
```

Now we can fasly recursively calculate the space and inodes usage in a directory by `doGetDirUsage`.

```go
// walk all directories in root
func (m *baseMeta) fastWalkDir(ctx Context, inode Ino, walkDir func(Context, Ino)) syscall.Errno {
    walkDir(ctx, inode)
    var entries []*Entry
    st := m.en.doReaddir(ctx, inode, 0, &entries, -1) // disable plus
    ...
    for _, entry := range entries {
    	if ent.Attr.Typ != TypeDirectory {
            continue
    	}
    	m.fastWalkDir(ctx, entry.Inode, walkFn)
        ...
    }
    return 0
}
func (m *baseMeta) getDirUsage(ctx Context, root Ino) (space, inodes uint64, err syscall.Errno) {
    m.fastWalkDir(ctx, root, func(_ Context, ino Ino) {
        s, i, err := m.doGetDirUsage(ctx, ino)
        ...
        space += s
        inodes += i
    })
    return
}
```


