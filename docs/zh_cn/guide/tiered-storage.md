---
title: 分层存储
sidebar_position: 8
description: 了解 JuiceFS 分层存储（tier）的配置、迁移与恢复。
---

JuiceFS 从 v1.4 开始支持分层存储，可以把不同目录或文件映射到不同对象存储类型（Storage Class），例如把热数据保留在标准存储，把冷数据下沉到 IA / Glacier 类存储，降低成本。

## 核心概念

- **tier-id**：分层 ID，范围为 `0~3`。
  - `0` 为默认层（保留值）。
  - `1~3` 为可配置层。
- **tier-sc**：某个 tier-id 对应的对象存储类型（例如 `STANDARD_IA`、`INTELLIGENT_TIERING`、`GLACIER_IR`）。
- **文件/目录的 tier 属性**：存储在元数据中，决定后续写入或迁移时应使用的存储类型。

## 使用前提

1. 已完成 JuiceFS 文件系统格式化与挂载。
2. 底层对象存储支持目标存储类型，以及（如需）归档对象恢复能力。
3. 先通过 `juicefs config` 定义 tier 映射，再执行 `juicefs tier set`。

## 1. 配置分层映射

先为 tier 1~3 配置存储类型：

```shell
juicefs config redis://localhost --tier-id 1 --tier-sc STANDARD_IA -y
juicefs config redis://localhost --tier-id 2 --tier-sc INTELLIGENT_TIERING -y
juicefs config redis://localhost --tier-id 3 --tier-sc GLACIER_IR -y
```

查看当前映射：

```shell
juicefs tier list redis://localhost
```

输出中 `id=0` 对应 `default`。

## 2. 为文件或目录设置 tier

### 设置单个文件

```shell
juicefs tier set redis://localhost --id 1 /path/to/file
```

### 设置目录（仅目录本身）

为目录本身设置存储层级的作用是当后续有新文件或子目录创建在该目录下时，会继承其父目录的 tier-id，从而自动使用对应的存储类型。

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir
```

不带 `-r` 时，仅修改目标目录自身，不会递归处理子目录和文件。

### 递归设置目录

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir -r
```

递归模式会处理目录树中的文件与子目录。

### 重置回默认层（tier 0）

```shell
juicefs tier set redis://localhost --id 0 /path/to/file
juicefs tier set redis://localhost --id 0 /path/to/dir -r
```

## 3. 变更 tier 映射后的重写（`--force`）

如果你把某个 tier-id 的 `tier-sc` 从 A 改成了 B，已有文件的元数据 tier-id 仍然不变，但对象存储里现存对象通常还是 A。  
此时可用 `--force` 触发重写，把对象改写到新的存储类型：

```shell
juicefs tier set redis://localhost --id 2 /path/to/dir -r --force
```

## 4. 归档类对象恢复

对于 `GLACIER` / `DEEP_ARCHIVE` 等归档类型，可执行：

```shell
juicefs tier restore redis://localhost /path/to/dir -r
```

`restore` 只向对象存储发起恢复请求；是否可读取、何时可读取取决于对象存储服务端恢复进度。活动副本的默认存活期（以天为单位）为 3 天

## 5. 状态检查

可使用 `juicefs info` 查看文件 tier 信息：

```shell
juicefs info /mountpoint/path/to/file
```

重点关注：

- `tier: <id>-><storage-class>`：元数据中的 tier 与映射。
- `restore-status`：会显示对象是否处于解冻状态以及副本的过期时间。
- 当映射与对象实际存储类型不一致时，会显示 `expected(...),actual(...)`，提示需要执行 `tier set --force` 重写。
- 对 `tier-id=0`，会显示对象实际存储类型（`actual(...)`）。

## 注意事项

1. `tier set` 仅支持文件和目录路径。
2. `--id` 仅允许 `0~3`；其中 `--tier-id` 配置时仅允许 `1~3`。
3. 在写回缓存（writeback）场景下，若文件数据尚未上传到对象存储，`tier set` 可能失败；待数据上传完成后再重试。
4. 修改 `--tier-sc` 不会自动迁移历史对象，需要手动执行 `tier set ... --force`。
