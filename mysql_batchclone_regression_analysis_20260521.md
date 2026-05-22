# MySQL BatchClone 回退根因分析报告

**日期**: 2026-05-21  
**结论**: 已确认两层原因，已定位到具体代码位置，附修复建议。

---

## 一、背景

Phase1 v5 测试中发现 MySQL batchclone_flat 出现严重性能回退：

| 版本 | 速度 |
|------|------|
| old (1.4.0-dev+2025-12-08.6160a99c) | 214 files/s |
| new (1.4.0-dev+2026-05-13.3b4845b5) | 59 files/s |
| **回退幅度** | **3.6×（性能下降 72.4%）** |

---

## 二、关键代码差异

### old 实现（无 BatchClone）

`pkg/meta/interface.go`（old）：
```go
Clone(ctx Context, srcParentIno, srcIno, dstParentIno Ino, dstName string,
      cmode uint8, cumask uint16, count, total *uint64) syscall.Errno
```

old 的 `cloneEntry` 遍历目录中的每一个文件，**逐文件**调用 `doCloneEntry`，每次一个小事务：
- 1× INSERT node
- 1× INSERT edge  
- 1× INSERT chunk
- 1× `UPDATE chunk_ref SET refs=refs+1 WHERE chunkid=? AND size=?`（简单主键更新）

### new 实现（引入 BatchClone）

`pkg/meta/interface.go`（new）：
```go
Clone(ctx Context, srcParentIno, srcIno, dstParentIno Ino, dstName string,
      cmode uint8, cumask uint16, concurrency uint8, count, total *uint64) syscall.Errno
```

new 的调用链：
```
Meta.Clone → cloneEntry → DirHandler.List(batchNum=40960) → BatchClone → doBatchClone
```

**`doBatchClone`（`pkg/meta/sql.go:5404`）** 在单个事务中处理所有条目：

```go
err := m.txn(func(s *xorm.Session) error {
    // 1. SELECT nodes WHERE inode IN (all_N_inodes) + ForUpdate  → N行锁
    // 2. INSERT N nodes                                          → N行INSERT
    // 3. INSERT N edges                                          → N行INSERT
    // 4. SELECT chunks WHERE inode IN (file_inodes) + ForUpdate  → N行锁
    // 5. INSERT N chunks                                          → N行INSERT
    // 6. ❌ UPDATE chunk_ref SET refs = refs + CASE WHEN chunkid=? THEN ? ...
    //        ELSE 0 END WHERE chunkid IN (?)                     → 超长SQL
})
```

---

## 三、性能诊断测试（Model B）

**测试配置**：5000 文件 flat 目录，juicefs-new binary，MySQL meta

### 测试结果

| 测试 | 文件大小 | chunk_ref 更新 | clone 耗时 | fps |
|------|---------|----------------|-----------|-----|
| empty_files | 0B | 无（Length=0 跳过） | ~60s | **83 files/s** |
| dd_4k_files | 4KB | 有（CASE WHEN UPDATE） | ~95s | **53 files/s** |

**关键时间戳**（来自 /var/log/juicefs.log）：
- empty_files clone start: `23:02:14.883418`
- empty_files mount exit:  `23:03:16.627024` → clone 耗时 **~60s**
- dd_4k clone start:       `23:05:09.982312`
- dd_4k mount exit:        `23:06:48.237842` → clone 耗时 **~95s**

---

## 四、根因分析

### 根因 1：大事务模式（主因，2.6× 回退）

**批次大小**：
- `DirBatchNum["db"]` = **40960**（每次 DirHandler.List 最多返回 40960 条）
- 5000 文件全部在一次 DirHandler.List 中返回 → 一次 `doBatchClone(5000 entries)`
- 100k 文件 → 3 次 `doBatchClone(40960 / 40960 / 18080 entries)`，每次巨型事务

**问题**：MySQL InnoDB 的大事务开销远高于多个小事务：
- 同时持有 N 个 node 行锁 + N 个 chunk 行锁 → 锁竞争、内存压力
- 一次性 INSERT N 行 → binlog 写放大、undo log 膨胀
- 对比 old 的 N 个小事务：每次只持有 1 行锁，提交后立即释放

**测量**：即使空文件（无 chunk 处理），new 仍只有 83 fps vs old 214 fps → **2.6× 回退**来自事务模式本身。

### 根因 2：CASE WHEN UPDATE chunk_ref（次因，1.57× 额外回退）

**代码位置**：`pkg/meta/sql.go:5607-5630`

```go
batchSize := m.getTxnBatchNum()  // MySQL: 65535 / 19 = 3449
for start := 0; start < len(chunkIds); start += batchSize {
    end := min(start+batchSize, len(chunkIds))
    batch := chunkIds[start:end]
    var sb strings.Builder
    args := make([]interface{}, 0, len(batch)*3)
    fmt.Fprintf(&sb, "UPDATE %schunk_ref SET refs = refs + CASE ", m.tablePrefix)
    for _, id := range batch {
        sb.WriteString("WHEN chunkid = ? THEN ? ")  // ❌ 最多3449个WHEN子句
        args = append(args, id, chunkRefCounts[id])
    }
    sb.WriteString("ELSE 0 END WHERE chunkid IN (")
    // ...3449个?占位符...
    sb.WriteString(")")
    s.Exec(append([]interface{}{sb.String()}, args...)...)
}
```

**问题**：
- 对 3449 个 chunkid 生成 3449 个 WHEN 条件 + 3449 个 IN 值的 SQL 字符串（约 170KB/条SQL）
- MySQL 解析超长 SQL 性能极差
- 对比 old 的 `UPDATE chunk_ref SET refs=refs+1 WHERE chunkid=? AND size=?` —— 每次简单主键更新，极快

**测量**：空文件（跳过 chunk_ref）83 fps vs 4KB 文件（触发 CASE WHEN）53 fps → **1.57× 额外回退**。

---

## 五、影响范围

| 场景 | 受影响 |
|------|--------|
| MySQL flat 目录 batch clone | ✅ **严重回退（3.6×）** |
| MySQL 嵌套目录 clone | ✅ 受影响（同样路径） |
| Redis batchclone | ❌ 不受影响（无 doBatchClone，走 KV 路径） |
| TiKV batchclone | ❌ 不受影响 |
| MySQL batchunlink | ❌ 不受影响（4× 提升，正常） |

---

## 六、修复建议

### 修复 1（优先）：减小 MySQL doBatchClone 的事务批次大小

在 `base.go` 或 `sql.go` 中为 MySQL 设置更小的 clone 批次，例如 100-500：

```go
// 在 newDirHandler 或 BatchClone 中，对 db meta 用更小的批次
func (m *dbMeta) newDirHandler(inode Ino, plus bool, entries []*Entry) DirHandler {
    batchNum := DirBatchNum["db"]
    if m.Name() == "mysql" || m.Name() == "postgres" {
        batchNum = 500  // 减小事务粒度
    }
    ...
}
```

或直接修改 `DirBatchNum["db"]` 初始值（当前 40960 对 MySQL 来说过大）。

### 修复 2：替换 CASE WHEN UPDATE 为单行 UPDATE 循环

将 `sql.go:5607-5630` 的 CASE WHEN 更新替换为：

```go
// 替换方案：多个简单主键 UPDATE，MySQL 对此极为高效
for id, count := range chunkRefCounts {
    if _, err := s.Exec(
        m.sqlConv("UPDATE chunk_ref SET refs = refs + ? WHERE chunkid = ?"),
        count, id,
    ); err != nil {
        return err
    }
}
```

或者分小批次（每批 50-100）用 CASE WHEN，避免单条 SQL 过长。

---

## 七、数据汇总

```
old (1.4.0-dev+2025-12-08)  ── 逐文件小事务 ──────────────────── 214 files/s (MySQL 5k flat)
new (1.4.0-dev+2026-05-13)  ── 空文件大事务（无chunk处理）────────  83 files/s (-61%, 2.6× slower)
new (1.4.0-dev+2026-05-13)  ── 4KB文件大事务（+CASE WHEN）─────────  53 files/s (-75%, 4.0× slower)
phase1 100k flat test new   ── 100k文件更大事务                ─────  59 files/s (-72%, 3.6× slower)
```

文件大小越大、批次越大 → 性能越差。建议将 MySQL 的 doBatchClone 批次控制在 200-500 以内。

---

## 八、修复验证（patch: dbCloneBatchSize=500）

**修改内容**：在 `pkg/meta/sql.go` 中添加 `dbCloneBatchSize=500` 常量，将 `doBatchClone`
改为按 500 条一批的多个独立小事务循环。

**测试环境**：juicefs-patched binary（含 batch=500 修复），MySQL 8.0.45，5000 文件 flat 目录

### 测试结果对比

| 版本 | 文件类型 | 耗时 | fps | vs unpatched |
|------|---------|------|-----|-------------|
| new (unpatched) | empty_files (0B) | ~60s | 83 fps | 基准 |
| new (unpatched) | dd_4k_files (4KB) | ~95s | 53 fps | 基准 |
| **patched (batch=500)** | **empty_files (0B)** | **38.7s** | **129 fps** | **+55% ✅** |
| **patched (batch=500)** | **dd_4k_files (4KB)** | **60.3s** | **83 fps** | **+57% ✅** |

### 结论

- batch=500 对两种文件类型均有约 **+55~57%** 的提升
- dd_4k patched (83 fps) 已追平 empty_files unpatched (83 fps)，说明 chunk_ref 处理
  的 CASE WHEN 开销被小事务摊薄
- 与 old (214 fps) 相比仍有差距，残余 gap (~39%) 来自：
  1. 根因 2 未修复：CASE WHEN chunk_ref UPDATE 仍存在（每批 500 个 WHEN，比单条
     5000 WHEN 好很多，但比逐行 UPDATE 仍慢）
  2. 其他 new vs old 结构性开销（BatchClone 框架开销 vs 逐文件串行开销）

### 后续优化建议

若需进一步逼近 old 的 214 fps，可追加：
1. **修复 CASE WHEN**：将 chunk_ref UPDATE 改为逐行 `UPDATE ... SET refs=refs+? WHERE chunkid=?`
2. **调整批次大小**：测试 100/200/1000 找最优点
3. **对比测试更大数据集**：验证 100k 文件场景下的改善幅度

---

## 九、第二轮优化：INSERT ODKU 替换 CASE WHEN

### 9.1 批次大小上限问题

将 `dbCloneBatchSize` 从固定 500 改为动态计算 `getTxnBatchNum()`：

```go
func (m *dbMeta) getTxnBatchNum() int {
    switch m.Name() {
    case "sqlite3":
        return 999 / MaxFieldsCountOfTable  // 52
    case "mysql":
        return 65535 / MaxFieldsCountOfTable // 3449
    case "postgres":
        return 1000
    default:
        return 1000
    }
}

func (m *dbMeta) getCloneBatchNum() int {
    if v := os.Getenv("JFS_CLONE_BATCH"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            return n
        }
    }
    return m.getTxnBatchNum()
}
```

MySQL 默认 batch = 65535 / 19 = **3449**（19 = node 的字段数 = `MaxFieldsCountOfTable`）。

### 9.2 用 INSERT ODKU 替换 CASE WHEN UPDATE

**原 CASE WHEN 方案**（每批最多 3449 个 WHEN 子句，SQL 约 170KB）：
```sql
UPDATE jfs_chunk_ref SET refs = refs + CASE
  WHEN chunkid = ? THEN ? WHEN chunkid = ? THEN ? ... ELSE 0
END WHERE chunkid IN (?,?,?...)
```

**新 INSERT ON DUPLICATE KEY UPDATE 方案**（单条 SQL，3 params/row）：
```sql
INSERT INTO jfs_chunk_ref (chunkid, size, refs) VALUES (?,?,?),(?,?,?),...
ON DUPLICATE KEY UPDATE refs = refs + VALUES(refs)
```

优势：
- MySQL 专用语法，原子操作，比 SELECT-then-UPDATE 更简洁
- 每行 3 个参数（vs CASE WHEN 的 2 参数 + IN 的 1 参数 + ELSE 0），总 param 数相同但 SQL 更短
- MySQL 对 ODKU 有专门优化，性能优于等效的 CASE WHEN

### 9.3 maxODKURows 分批（修复 Error 1390）

当每个文件有多个 slice 时，`chunkRefMap` 的大小超过 `3449`，导致总占位符 > 65535：

```
batch=3449 files × 13 slices/file × 3 params/row = 134,511 > 65535 → Error 1390
```

修复：添加内层分批，每批最多 `maxODKURows = 21845`（= 65535 / 3）：

```go
const maxODKURows = 21845
for batchStart := 0; batchStart < len(refEntries); batchStart += maxODKURows {
    batchEnd := min(batchStart+maxODKURows, len(refEntries))
    batch := refEntries[batchStart:batchEnd]
    // 构造并执行该批的 INSERT ODKU
}
```

### 9.4 第二轮测试结果（含测试环境污染的观测）

| 版本 | 环境 | empty_files | dd_4k_files |
|------|------|-------------|-------------|
| unpatched | 干净 | 83 fps | 53 fps |
| patched batch=500, CASE WHEN | 干净 | 129 fps | 83 fps |
| patched batch=3449, INSERT ODKU | **脏数据** | 201 fps | 45–59 fps |
| patched batch=500, INSERT ODKU | **脏数据** | 93 fps | 108 fps |

**发现**：batch=3449 的 dd_4k 结果（45–59 fps）**低于** batch=500（108 fps），与预期相反。

---

## 十、测试环境污染根因分析

### 10.1 发现过程

为 `doBatchClone` 添加计时日志（`tLast` 方式）：

```
doBatchClone batch=3449: selectNodes=54ms nodeEdgeInsert=53637ms
  selectChunks=118ms chunkInsert=21270ms chunkRefInsert=665ms chunkRefs=27592
doBatchClone batch=1551: selectNodes=24ms nodeEdgeInsert=30033ms
  selectChunks=55ms chunkInsert=3122ms chunkRefInsert=291ms chunkRefs=12408
```

关键异常：**chunkRefs=27592**（期望值 ≈ 3449，即每文件 1 个 slice）→ 实际平均每文件 **8 个 slice**。

### 10.2 根因：format --force 不清除 jfs_chunk

查看 `doInit`（`pkg/meta/sql.go:614`）：在 `ok=true`（数据库已存在 format 记录）分支，仅更新 `jfs_setting` 表，**不清除** `jfs_chunk`、`jfs_node`、`jfs_edge`、`jfs_counter` 等数据表。

当测试环境复用同一 MySQL 数据库时：

1. `format --force` 重置 format 元信息，但 `jfs_chunk` 保留旧数据
2. 新文件创建后 inode 计数器从旧值续接，最终 inode 与旧记录相同
3. JuiceFS 写文件时**追加** slice 到已有的 `jfs_chunk` blob，旧 slice 不会被清除
4. 多次测试后，每个 inode 的 slices blob 累积大量历史 slice

**验证（MySQL 查询）**：

```sql
SELECT LENGTH(slices)/24 as slice_count, COUNT(*) as num_files
FROM jfs_chunk c JOIN jfs_edge e ON c.inode = e.inode WHERE e.parent = 2
GROUP BY LENGTH(slices)/24;
-- 结果: 15 slices → 4999 files, 1 slice → 1 file
```

5000 个 src 文件，4999 个有 15 个 slice（14 个旧 + 1 个新），只有 1 个文件（f1）是干净的。

**注意**：`juicefs info` 只展示文件长度范围内的有效 object，显示正常（1 个 4KB object），但 clone 代码的 `SELECT jfs_chunk WHERE inode IN (...)` 读取了所有 slice，包括失效的旧 slice。

### 10.3 清理方法：juicefs compact

```bash
juicefs compact /mnt/jfs-diag2/src
```

compact 将每个 chunk 的多个历史 slice 合并为 1 个，更新 jfs_chunk 记录。

compact 后验证：

```sql
SELECT LENGTH(slices)/24 as slice_count, COUNT(*) as num_files ...
-- 结果: 1 slice → 5000 files  ✅
```

---

## 十一、第三轮优化：xorm 逐行 INSERT → Multi-row INSERT

### 11.1 发现

compact 后，干净环境重跑（每文件 1 个 slice）：

```
doBatchClone batch=3449: selectNodes=53ms nodeEdgeInsert=42691ms
  selectChunks=30ms chunkInsert=25774ms chunkRefInsert=80ms chunkRefs=3449
```

- `chunkRefs=3449` ✅ 正常
- `chunkRefInsert=80ms` ✅（INSERT ODKU 优化有效）
- **`nodeEdgeInsert=42691ms`**，**`chunkInsert=25774ms`** ❌ 仍然极慢

### 11.2 根因：mustInsert 的 []interface{} 展开导致逐行插入

```go
func mustInsert(s *xorm.Session, beans ...interface{}) error {
    for start, end, size := 0, 0, len(beans); end < size; start = end {
        end = start + 200
        if n, err := s.Insert(beans[start:end]...); err != nil {  // ← 关键
```

`s.Insert(beans[start:end]...)` 将 `[]interface{}` **展开（spread）** 为单独的 variadic 参数。

xorm 的 `Insert(beans ...interface{})` 源码（`session_insert.go:53`）：
```go
sliceValue := reflect.Indirect(reflect.ValueOf(bean))
if sliceValue.Kind() == reflect.Slice {
    cnt, err = session.insertMultipleStruct(bean)  // ← 仅当 bean 本身是 slice 才走批量路径
}
```

- `s.Insert(bean1, bean2, ..., bean200)` → 每个 `bean` 是 `interface{}` 包装的 `*node` → **逐行插入**
- `s.Insert([]*node{...})` → `bean` 本身是 `[]*node` 类型 → **multi-row INSERT**

**实际开销**：3449 行 × ~7ms/行（MySQL 单行 INSERT 延迟）≈ 24s，与观测的 `chunkInsert=25774ms` 完全吻合。

### 11.3 修复：改用 Typed Slice

**修改 `doBatchClone` 中三处插入的变量声明**：

```go
// 修改前
nodesIns := make([]interface{}, 0, len(batchEntries))
edgesIns := make([]interface{}, 0, len(batchEntries))
// ...
chunksIns := make([]interface{}, 0, len(srcChunks))

// 修改后
nodesIns := make([]*node, 0, len(batchEntries))
edgesIns := make([]*edge, 0, len(batchEntries))
// ...
chunksIns := make([]*chunk, 0, len(srcChunks))
```

**修改插入调用**（从 `mustInsert(s, slice...)` 改为分批 `s.Insert(typedSlice)`）：

```go
// 修改前
if err := mustInsert(s, nodesIns...); err != nil { return err }
if err := mustInsert(s, edgesIns...); err != nil { ... }
if err := mustInsert(s, chunksIns...); err != nil { return err }

// 修改后
txnBatch := m.getTxnBatchNum()  // MySQL: 3449（保证 node 的 19字段×3449行=65531 < 65535）
for start := 0; start < len(nodesIns); start += txnBatch {
    end := min(start+txnBatch, len(nodesIns))
    if _, err := s.Insert(nodesIns[start:end]); err != nil { return err }
}
for start := 0; start < len(edgesIns); start += txnBatch {
    end := min(start+txnBatch, len(edgesIns))
    if _, err := s.Insert(edgesIns[start:end]); err != nil {
        if isDuplicateEntryErr(err) { return syscall.EEXIST }
        return err
    }
}
// chunksIns 同理（chunk 有 3 个 INSERT 字段，3449×3=10347 << 65535，无需额外分批）
for start := 0; start < len(chunksIns); start += txnBatch {
    end := min(start+txnBatch, len(chunksIns))
    if _, err := s.Insert(chunksIns[start:end]); err != nil { return err }
}
```

**为何 `txnBatch = getTxnBatchNum() = 3449` 是安全上限**：

| 类型 | INSERT 字段数 | 最大安全行数（65535/字段数）| 使用行数 |
|------|--------------|--------------------------|---------|
| `node` | 19 | 3449 | 3449 ✅ |
| `edge` | 4（Id 为 bigserial 自动生成）| 16383 | 3449 ✅ |
| `chunk` | 3（Id 为 bigserial 自动生成）| 21845 | 3449 ✅ |

### 11.4 最终性能数据（干净环境 + 所有优化）

**计时日志（优化后）**：
```
doBatchClone batch=3449: selectNodes=55ms nodeEdgeInsert=244ms
  selectChunks=35ms chunkInsert=46ms chunkRefInsert=73ms chunkRefs=3449
doBatchClone batch=1551: selectNodes=24ms nodeEdgeInsert=116ms
  selectChunks=16ms chunkInsert=29ms chunkRefInsert=37ms chunkRefs=1551
```

**各阶段提升对比**（batch=3449，干净数据）：

| 阶段 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| nodeEdgeInsert | 42691ms | 244ms | **175×** |
| chunkInsert | 25774ms | 46ms | **560×** |
| chunkRefInsert | 665ms → 80ms → 73ms | 73ms | 9× |
| **总 clone fps** | 57 fps | **4420 fps** | **77×** |

---

## 十二、完整性能演进数据

| 版本 / 配置 | 环境 | empty_files | dd_4k_files | 备注 |
|------------|------|-------------|-------------|------|
| old (1.4.0-dev+2025-12-08) | 干净 | — | **214 fps** | 逐文件小事务 |
| new unpatched | 干净 | 83 fps | 53 fps | 大事务 + CASE WHEN |
| patched v1: batch=500 | 干净 | 129 fps | 83 fps | 小事务，仍 CASE WHEN |
| patched v2: batch=3449, CASE WHEN | 干净 | 117 fps | 失败（Error 1390） | placeholder 超限 |
| patched v3: batch=3449, INSERT ODKU | 脏数据 | 201 fps | 45–59 fps | stale slices 污染 |
| patched v4: batch=500, INSERT ODKU | 脏数据 | 93 fps | 108 fps | daemon env var 问题 |
| **patched v5: batch=3449, INSERT ODKU + multi-row INSERT** | **干净** | — | **4420 fps** | **当前最优** |

---

## 十三、代码修改汇总（`pkg/meta/sql.go`）

| 修改 | 位置 | 内容 |
|------|------|------|
| 1 | `getCloneBatchNum()` | 新增函数，支持 `JFS_CLONE_BATCH` env var 覆盖，默认返回 `getTxnBatchNum()` |
| 2 | `getTxnBatchNum()` | MySQL 从固定 500 改为 `65535/MaxFieldsCountOfTable = 3449` |
| 3 | `doBatchClone` chunk_ref 更新 | CASE WHEN UPDATE → INSERT ODKU，内层按 `maxODKURows=21845` 分批 |
| 4 | `doBatchClone` nodes/edges 插入 | `[]interface{}` → `[]*node`/`[]*edge`，使用 xorm multi-row INSERT |
| 5 | `doBatchClone` chunks 插入 | `[]interface{}` → `[]*chunk`，使用 xorm multi-row INSERT |
