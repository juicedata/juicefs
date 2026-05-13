# JuiceFS 删除性能测试计划

## 一、测试目标

### 1.1 删除能力极限测试
- **小文件删除**: 每小时可删除多少个小文件（4KB）
- **大文件删除**: 每小时可删除多少数据量（100MB/文件）
- **后台清理任务效率**: GC、Trash 清理、Slice 清理、Compact 的速度

### 1.2 BatchUnlink/BatchClone 优化效果对比
- 在 **Redis/MySQL/TiKV** 三种元数据引擎下，对比优化前后的性能提升
- 测试维度：单目录批量删除、跨目录批量克隆

### 1.3 RMR 命令性能测试
- 测试 `juicefs rmr` 递归删除大目录的性能
- 对比 `rm -rf` vs `juicefs rmr --threads=N` 在不同线程数下的性能差异
- `rmr` 通过 `emptyDir` 实现，对子目录使用并发 goroutine，对非目录文件使用 `BatchUnlink`

---

## 二、JuiceFS 删除机制说明（内核视角）

### 2.1 删除流程（类似 VFS 的 unlink 路径）

```
用户调用 rm → FUSE → JuiceFS VFS → Meta Engine
                                    ↓
                              1. Unlink/BatchUnlink
                                    ↓
                              2. 文件进入 Trash（如果启用）
                                    ↓
                              3. 后台 cleanupDeletedFiles 清理 slice
                                    ↓
                              4. 后台 cleanupSlices 清理对象存储
```

### 2.2 后台清理任务（类似内核的 workqueue）

JuiceFS 有 **4 个后台清理任务**，在 `NewSession()` 时启动（`NoBGJob=false` 时）：

| 任务 | 触发间隔 | 功能 | 对应代码 |
|------|---------|------|---------|
| `cleanupDeletedFiles` | 1 小时 | 清理已删除文件的 slice 引用 | `base.go:999` |
| `cleanupSlices` | 1 小时 | 清理对象存储中的孤儿 slice | `base.go:1035` |
| `cleanupTrash` | 1 小时 | 清理过期 trash 中的文件 | `base.go:3059` |
| `symlinks.clean` | - | 清理过期符号链接缓存 | - |

### 2.3 BatchUnlink 优化原理

**优化前**: 每个文件一个独立事务
```
for each file:
    txn: check parent → check file → remove dentry → update parent → update stats
```

**优化后 (BatchUnlink)**: 一批文件一个事务
```
for batch in files:
    txn: check parent once → check all files → remove all dentries → update parent once → update stats once
```

在 Redis 中，batch size 约为 `1000/4 = 250` 个文件/事务（`redis.go:1927`）。

### 2.4 BatchClone 优化原理

类似 BatchUnlink，将多个文件的克隆操作合并到一个事务中，减少元数据引擎的往返次数。

---

## 三、测试脚本说明

### 3.1 脚本 1: `delete_perf_test.sh`（Shell 脚本，基于 FUSE 挂载）

**适用场景**: 端到端测试，模拟真实用户场景

**测试内容**:
1. **小文件删除**: 创建 **100万** 个 4KB 文件，测量 `rm -rf` 删除速度
2. **大文件删除**: 创建 **1万** 个 100MB 文件（总数据量 ~1TB），测量删除速度
3. **BatchUnlink 对比**: 逐文件删除 vs `rm -rf` 批量删除（**10万** 文件级别）
4. **BatchClone 对比**: 逐文件 `cp` vs `cp -r` 批量克隆（**10万** 文件级别）
5. **RMR 性能**: `rm -rf` vs `juicefs rmr --threads=N` 删除 100万 文件（1000 子目录 × 1000 文件）
6. **GC 效率**: `juicefs gc` 扫描和清理速度
7. **Trash 清理**: 文件进入 trash 后的清理速度
8. **综合压力**: 1000 个目录 × 100 个文件的混合删除

**使用方法**:
```bash
# 运行所有测试（Redis 引擎）
./delete_perf_test.sh all redis

# 只测试小文件删除（MySQL 引擎）
./delete_perf_test.sh small mysql

# 在所有引擎上测试 BatchUnlink
./delete_perf_test.sh batchunlink all

# 测试 RMR 性能
./delete_perf_test.sh rmr redis
```

**配置修改**（脚本开头）:
```bash
# 修改为你的元数据引擎地址
# 双机部署时，将 localhost 替换为元数据服务器 IP
REDIS_META="redis://localhost:6379/1"
MYSQL_META="mysql://user:password@tcp(localhost:3306)/juicefs"
TIKV_META="tikv://localhost:2379/juicefs"

# 修改为你的对象存储
OBJECT_STORAGE="--storage s3 --bucket http://mybucket.s3.amazonaws.com"
```

### 3.2 脚本 2: `delete_bench.go`（Go 程序，直接调用 Meta 接口）

**适用场景**: 精确测量 batchunlink/batchclone 的纯元数据操作性能，排除 FUSE 和网络开销

**测试内容**:
1. **小文件删除**: 直接调用 `Unlink` vs `BatchUnlink`
2. **大文件删除**: 直接调用 `BatchUnlink` 删除带 slice 的大文件
3. **BatchClone**: 直接调用 `Clone` vs `BatchClone`
4. **GC 扫描**: 调用 `ScanDeletedObject` 测量扫描速度
5. **Trash 清理**: 调用 `CleanupTrashBefore` 测量清理速度

**编译和运行**:
```bash
# 在 JuiceFS 源码目录下编译
cd /path/to/juicefs
go build -o delete_bench ./scripts/delete_bench.go

# 运行所有测试
./delete_bench -meta="redis://localhost:6379/1" -test=all

# 只测试 BatchUnlink
./delete_bench -meta="redis://localhost:6379/1" -test=small

# 调整测试规模
./delete_bench -meta="mysql://..." -test=all \
    -small-count=50000 \
    -large-count=500 \
    -batch-size=5000
```

---

## 四、测试机器推荐配置

### 4.1 数据量评估

| 测试项 | 文件数量 | 单文件大小 | 总数据量 | 元数据操作量 |
|--------|---------|-----------|---------|------------|
| 小文件删除 | **1,000,000** | 4 KB | ~4 GB | ~100万 创建 + 100万 删除 |
| 大文件删除 | **10,000** | 100 MB | **~1 TB** | ~1万 创建 + 1万 删除 |
| BatchUnlink | **100,000** | 4 KB | ~400 MB | ~10万 创建 + 10万 删除 |
| BatchClone | **100,000** | 4 KB | ~400 MB | ~10万 创建 + 10万 克隆 |
| RMR | **1,000,000** | 4 KB | ~4 GB | ~100万 创建 + 100万 删除 |
| 综合压力 | 100,000 | 4 KB | ~400 MB | ~10万 创建 + 10万 删除 |
| Trash 清理 | 50,000 | 4 KB | ~200 MB | ~5万 创建 + 5万 删除 + GC |

**总计（单次完整测试）**:
- 峰值数据量: **~2 TB**（含重复创建和 trash）
- 峰值文件数: **~300 万** 个文件
- 对象存储写入: **~3 TB**（含临时文件和 trash）

**注意**: 大文件删除测试（1TB）受限于磁盘空间，可根据实际配置调整 `LARGE_FILE_COUNT`。

### 4.2 推荐配置

#### 方案 A: 最小测试环境（验证功能）

| 组件 | 配置 | 说明 |
|------|------|------|
| CPU | 4 核 | Go 程序并发需要 |
| 内存 | 8 GB | JuiceFS 客户端缓存 |
| 磁盘 | 50 GB SSD | 本地对象存储（file 类型） |
| 网络 | 内网 | 连接元数据引擎 |

**适用**: 快速验证脚本、SQLite 本地测试

#### 方案 B: 标准测试环境（推荐）

| 组件 | 配置 | 说明 |
|------|------|------|
| CPU | 8-16 核 | 支持多线程创建/删除 |
| 内存 | 32 GB | 元数据缓存、Go GC |
| 磁盘 | 500 GB NVMe SSD | 本地对象存储 + 日志 |
| 网络 | 万兆内网 | 连接 Redis/MySQL/TiKV |

**适用**: 单引擎完整测试、BatchUnlink/BatchClone 对比

#### 方案 C: 生产级测试环境（极限性能）

| 组件 | 配置 | 说明 |
|------|------|------|
| CPU | 32-64 核 | 高并发元数据操作 |
| 内存 | 128 GB | 大量元数据缓存 |
| 磁盘 | 2 TB NVMe SSD × 2 | RAID 0，高 IOPS |
| 网络 | 25GbE | 低延迟连接元数据引擎 |
| 对象存储 | 独立 MinIO/S3 | 真实对象存储性能 |

**适用**: 三种引擎对比测试、极限性能压测

#### 方案 D: 双机部署（模拟真实场景）

**服务器 1: 元数据服务器**
| 组件 | 配置 | 说明 |
|------|------|------|
| CPU | 8-16 核 | 元数据引擎需要 |
| 内存 | 32-64 GB | Redis/MySQL/TiKV 缓存 |
| 磁盘 | 500 GB NVMe SSD | 元数据持久化 |
| 网络 | 万兆内网 | 允许客户端连接 |
| 操作系统 | Ubuntu 22.04 / Rocky Linux 9 | |

**服务器 2: 客户端**
| 组件 | 配置 | 说明 |
|------|------|------|
| CPU | 16-32 核 | 高并发创建/删除 |
| 内存 | 64-128 GB | JuiceFS 客户端缓存、Go GC |
| 磁盘 | 2 TB NVMe SSD × 2 | 本地对象存储（file 类型）或缓存 |
| 网络 | 万兆内网 | 低延迟连接元数据服务器 |
| 操作系统 | Ubuntu 22.04 / Rocky Linux 9 | |

**部署脚本**:
- 元数据服务器: `deploy_meta_server.sh`
- 客户端: `deploy_client.sh`

**适用**: 模拟真实分布式场景、评估网络延迟影响

### 4.3 元数据引擎推荐配置

#### Redis
```bash
# 单机 Redis（测试用）
redis-server --maxmemory 8gb --maxmemory-policy allkeys-lru

# 推荐硬件: 4 核 / 16GB 内存 / SSD
```

#### MySQL
```bash
# 关键配置
[mysqld]
innodb_buffer_pool_size = 8G      # 缓冲池大小
innodb_log_file_size = 1G         # redo log 大小
innodb_flush_log_at_trx_commit = 2 # 性能模式
max_connections = 500
```

**推荐硬件**: 8 核 / 32GB 内存 / NVMe SSD

#### TiKV
```bash
# 最小测试集群（3 节点）
# 每节点: 8 核 / 32GB 内存 / NVMe SSD
```

### 4.4 对象存储选择

| 类型 | 适用场景 | 配置 |
|------|---------|------|
| `file` (本地磁盘) | 快速测试、无网络依赖 | 500GB+ NVMe |
| `minio` | 模拟 S3、可重复测试 | 独立部署 |
| `s3` | 真实生产环境 | 根据实际带宽 |

**建议**: 测试 BatchUnlink/BatchClone 时，使用 `file` 类型即可（主要瓶颈在元数据引擎）。测试 GC 效率时，建议使用 MinIO 模拟真实对象存储。

---

## 五、测试执行计划

### 5.1 双机部署流程

**步骤 1: 在元数据服务器上部署**
```bash
# 上传 deploy_meta_server.sh 到元数据服务器
chmod +x deploy_meta_server.sh

# 安装 Redis（示例）
./deploy_meta_server.sh redis install

# 查看连接信息
./deploy_meta_server.sh redis status
```

**步骤 2: 在客户端上部署**
```bash
# 上传 deploy_client.sh 和 delete_perf_test.sh 到客户端
chmod +x deploy_client.sh delete_perf_test.sh

# 安装 JuiceFS
export META_IP="192.168.1.100"  # 替换为元数据服务器 IP
./deploy_client.sh install

# 配置环境
./deploy_client.sh setup

# 挂载 JuiceFS
./deploy_client.sh mount

# 运行测试
./deploy_client.sh test
```

### 5.2 预检清单

```bash
# 1. 检查 JuiceFS 版本
juicefs --version

# 2. 检查元数据引擎连接
juicefs status redis://<meta-server-ip>:6379/1

# 3. 检查磁盘空间（确保足够 2TB+）
df -h

# 4. 检查内存
free -h

# 5. 设置系统限制（重要！）
ulimit -n 1048576    # 文件描述符
sysctl -w fs.file-max=2097152

# 6. 检查网络延迟（双机部署）
ping <meta-server-ip>
```

### 5.3 执行顺序

```bash
# 阶段 1: 快速验证（SQLite，约 30 分钟）
./delete_perf_test.sh all sqlite

# 阶段 2: 单引擎完整测试（Redis，约 4-8 小时）
./delete_perf_test.sh all redis

# 阶段 3: 三引擎对比测试（约 12-24 小时）
./delete_perf_test.sh all all

# 阶段 4: 精确测量（Go 程序，约 2-4 小时/引擎）
./delete_bench -meta="redis://<meta-ip>:6379/1" -test=all
./delete_bench -meta="mysql://..." -test=all
./delete_bench -meta="tikv://..." -test=all
```

### 5.4 结果分析

测试结果保存在 `/tmp/juicefs-delete-test-results-*/` 目录下：

```
results-20240101-120000/
├── redis/
│   ├── small_file_delete/results.txt
│   ├── large_file_delete/results.txt
│   ├── batchunlink/results.txt
│   └── gc_only/gc_results.txt
├── mysql/
│   └── ...
└── report.txt          # 汇总报告
```

**关键指标**:
- `small_file_delete_rate`: 小文件删除速度（文件/小时）
- `large_file_delete_rate_gb`: 大文件删除速度（GB/小时）
- `batch_unlink_improvement`: BatchUnlink 提升倍数
- `batch_clone_improvement`: BatchClone 提升倍数
- `rmr_time`: RMR 命令删除耗时
- `rmrf_time`: rm -rf 删除耗时
- `rmr_improvement`: RMR 相对 rm -rf 的提升比例
- `gc_cleanup_time`: GC 清理耗时

---

## 六、注意事项

### 6.1 测试前
1. **清空元数据引擎**: 每个引擎使用独立的数据库/namespace
2. **关闭其他服务**: 避免 CPU/磁盘竞争
3. **预热**: 元数据引擎需要预热缓存

### 6.2 测试中
1. **监控资源**: 使用 `top`, `iostat`, `iftop` 监控瓶颈
2. **日志级别**: 建议设置 `--log-level warn` 减少日志开销
3. **超时处理**: 大文件测试可能需要数小时

### 6.3 测试后
1. **清理数据**: 使用 `juicefs destroy` 清理元数据
2. **释放对象存储**: 删除 bucket 中的残留数据
3. **保存结果**: 将 `results-*` 目录备份

---

## 七、BatchUnlink/BatchClone 优化对比方法

### 7.1 对比优化前/后的代码

JuiceFS 的 BatchUnlink/BatchClone 是较新的优化，如果你需要对比优化前后的效果：

```bash
# 1. 检出优化前的代码版本
git checkout <优化前版本>
go build -o juicefs-old ./cmd

# 2. 运行测试
./delete_perf_test.sh batchunlink redis

# 3. 检出优化后的代码版本
git checkout <优化后版本>
go build -o juicefs-new ./cmd

# 4. 再次运行测试
./delete_perf_test.sh batchunlink redis

# 5. 对比结果
```

### 7.2 使用 Go 程序精确对比

`delete_bench.go` 会自动对比：
- `single_unlink_time`: 循环调用 `Unlink()`
- `batch_unlink_time`: 调用 `BatchUnlink()`
- 输出 `batch_unlink_improvement`: 提升倍数

---

## 八、故障排查

| 问题 | 原因 | 解决 |
|------|------|------|
| "too many open files" | ulimit 不足 | `ulimit -n 1048576` |
| Redis OOM | 内存不足 | 增加 Redis 内存或减小测试规模 |
| MySQL deadlock | 并发过高 | 减小 `TEST_THREADS` |
| 测试卡住 | 后台 GC 阻塞 | 检查对象存储连接 |
| 结果不一致 | 缓存影响 | 每次测试前 `echo 3 > /proc/sys/vm/drop_caches` |

---

## 九、附录：关键代码参考

| 功能 | 文件 | 行号 |
|------|------|------|
| BatchUnlink | `pkg/meta/base.go` | 1836 |
| BatchClone | `pkg/meta/base.go` | 1854 |
| Redis BatchUnlink | `pkg/meta/redis.go` | 1907 |
| SQL BatchUnlink | `pkg/meta/sql.go` | 2753 |
| RMR 命令 | `cmd/rmr.go` | 36 |
| emptyDir (RMR 实现) | `pkg/meta/utils.go` | 290 |
| GC 命令 | `cmd/gc.go` | 36 |
| Compact 命令 | `cmd/compact.go` | 30 |
| 后台 cleanupDeletedFiles | `pkg/meta/base.go` | 999 |
| 后台 cleanupSlices | `pkg/meta/base.go` | 1035 |
| 后台 cleanupTrash | `pkg/meta/base.go` | 3059 |
| ScanDeletedObject | `pkg/meta/base.go` | 3273 |

---

## 十、脚本清单

| 脚本 | 用途 | 运行位置 |
|------|------|---------|
| `deploy_meta_server.sh` | 部署元数据引擎（Redis/MySQL/TiKV） | 元数据服务器 |
| `deploy_client.sh` | 部署 JuiceFS 客户端、挂载、运行测试 | 客户端服务器 |
| `delete_perf_test.sh` | 执行删除性能测试 | 客户端服务器 |
| `delete_bench.go` | Go 基准测试程序（直接调用 Meta 接口） | 客户端服务器（需源码编译） |
