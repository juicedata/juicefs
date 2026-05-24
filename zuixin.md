# JuiceFS Phase1 v5 最新代码三引擎复测报告

## 1. 任务完成状态

- 复测日期: 2026-05-23
- 覆盖范围: Redis / TiKV / MySQL 的 batchunlink 与 batchclone_flat
- 数据规模: 每轮 100000 文件
- 二进制来源: 当前工作树源码重新编译
  - 版本输出: `juicefs version 1.4.0-dev+unknown`
- 测试产物:
  - MySQL: `/tmp/jfs-mysql-latest-perf-20260523-093245`
  - Redis/TiKV: `/tmp/jfs-rdtkv-latest-perf-20260523-133754`

## 2. 环境与口径

- 网络:
  - client: 172.22.118.154
  - meta: 172.22.118.153
- 元数据服务状态:
  - Redis 6379 正常监听
  - PD 2379 正常监听
  - TiKV 20160 / 20180 正常监听
  - MySQL 3306 在前序测试中已验证可用
- 删除口径: `juicefs rmr --threads 16`
- 克隆口径: `juicefs clone`
- 文件模型: flat 目录，100000 个 4KB 文件
- 指标:
  - `files_per_s = files / elapsed_s`
  - `相对 old 提升倍数 = latest / old`
  - `相对 phase1 new 提升倍数 = latest / phase1_new`

## 3. 与 phase1 结果的关键差异

本次复测与 [phase1_v5_内网统一逻辑_最终结果_20260521.md](phase1_v5_内网统一逻辑_最终结果_20260521.md) 有两点关键差异：

1. 本次使用的是当前工作树重新编译的最新二进制，不再使用此前可能存在版本错位的旧二进制。
2. 本次 batchclone 使用 `juicefs clone` 直接命中 clone 控制路径，而不是 `cp -r` 间接触发。

因此，本次结果更适合作为“当前代码实际能力”的基线，而 phase1 结果更适合作为“当时发布二进制 + 当时测试流程”的历史对照。

## 4. 最新复测总表

### 4.1 批量删除 batchunlink

| Engine | Phase1 Old files/s | Phase1 New files/s | Latest files/s | Latest vs Old | Latest vs Phase1 New |
|---|---:|---:|---:|---:|---:|
| Redis | 1612 | 25000 | 49480.46 | 30.70x | 1.98x |
| TiKV | 191 | 3125 | 17787.26 | 93.13x | 5.69x |
| MySQL | 48 | 198 | 223.98 | 4.67x | 1.13x |

### 4.2 批量克隆 batchclone_flat

| Engine | Phase1 Old files/s | Phase1 New files/s | Latest files/s | Latest vs Old | Latest vs Phase1 New |
|---|---:|---:|---:|---:|---:|
| Redis | 4761 | 20000 | 26130.13 | 5.49x | 1.31x |
| TiKV | 621 | 1282 | 1297.30 | 2.09x | 1.01x |
| MySQL | 214 | 59 | 5134.79 | 23.99x | 87.03x |

## 5. 最新复测原始结果

### 5.1 MySQL

1. `test=batchclone_flat create_sec=2659.734 clone_sec=19.475 files=100000 files_per_s=5134.79 src_count=100000 dst_count=100000`
2. `test=batchunlink create_sec=2665.006 unlink_sec=446.466 files=100000 files_per_s=223.98 remain=0 method=rmr`

### 5.2 Redis

1. `engine=redis test=batchclone_flat create_sec=120.605 clone_sec=3.827 files=100000 files_per_s=26130.13 src_count=100000 dst_count=100000`
2. `engine=redis test=batchunlink create_sec=118.732 unlink_sec=2.021 files=100000 files_per_s=49480.46 remain=0 method=rmr`

### 5.3 TiKV

1. `engine=tikv test=batchclone_flat create_sec=423.681 clone_sec=77.083 files=100000 files_per_s=1297.30 src_count=100000 dst_count=100000`
2. `engine=tikv test=batchunlink create_sec=427.179 unlink_sec=5.622 files=100000 files_per_s=17787.26 remain=0 method=rmr`

## 6. MySQL batchclone 补充说明

MySQL 的最新 batchclone 结果为 `5134.79 files/s`，已经和 phase1 中 `59 files/s` 的回退结果完全不在一个量级。

结合此前对 [mysql_batchclone_regression_analysis_20260521.md](mysql_batchclone_regression_analysis_20260521.md) 的定位，以及本次直接用最新源码重编二进制复测，可以确认：

1. 当前代码中的 MySQL batchclone 路径已经修复，不存在 phase1 中那种严重回退。
2. phase1 中 MySQL batchclone 的异常结果，至少部分来自二进制版本错位或测试路径偏差，而不是当前实现的真实上限。
3. 从本次 100k 结果看，MySQL batchclone 已经从历史回退点恢复到显著优于 old 的水平。

## 7. 结论

1. 当前最新代码下，三种元数据引擎的 batchclone 与 batchunlink 都工作正常，10 万文件场景复测通过。
2. Redis 仍然是三者中 batchclone 和 batchunlink 的最快实现。
3. TiKV 的 batchclone 基本与 phase1 new 持平，但 batchunlink 相比 phase1 new 大幅提升，说明此前二进制或环境口径很可能偏旧。
4. MySQL 的 batchunlink 与 phase1 new 相比小幅提升，但 batchclone 从 phase1 的严重回退状态恢复到 `5134.79 files/s`，说明当前修复已经生效。
5. 这轮复测支持一个更强的结论：phase1 中“新版本 batchclone 普遍退化”的判断不成立，至少对当前源码重新编译版本不成立。

## 8. 后续建议

1. 若要形成正式结论，建议再补 2 到 3 轮重复测试，取均值与波动范围。
2. 若要与 phase1 完全同口径归档，建议再补一轮 `cp -r` 方式的三引擎 clone 对照，用于说明 `cp -r` 与 `juicefs clone` 的路径差异。
3. 可以把本报告作为新的基线，并基于它继续做 batch size、threads、文件大小分布的参数扫描。
