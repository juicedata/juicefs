---
title: 路径保护
description: 了解如何使用路径保护功能限制对 JuiceFS 中特定目录的访问。
sidebar_position: 3
---

路径保护是一项允许您使用正则表达式模式限制对 JuiceFS 中特定路径访问的功能。这对于保护敏感目录（如 `.git`）免受意外修改，或完全隐藏机密目录非常有用。

## 保护模式

支持两种保护模式：

| 模式 | 写操作 | 读操作 |
|------|--------|--------|
| `readonly` | 阻止 | 允许 |
| `deny` | 阻止 | 阻止 |

## 使用方法

在挂载时使用 `--path-protection` 选项启用路径保护：

### 只读模式

保护 `.git` 目录免受写操作，同时允许读取：

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/data/.*\\.git.*","mode":"readonly"}]}' \
  redis://localhost /mnt/jfs
```

### 禁止模式

完全禁止访问机密目录：

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/secrets/.*","mode":"deny"}]}' \
  redis://localhost /mnt/jfs
```

### 多条规则

可以指定多条保护规则：

```shell
juicefs mount \
  --path-protection='{"rules":[{"pattern":"^/mnt/jfs/data/.*\\.git.*","mode":"readonly"},{"pattern":"^/mnt/jfs/secrets/.*","mode":"deny"}]}' \
  redis://localhost /mnt/jfs
```

## 模式匹配

模式是匹配完整路径（包括挂载点）的正则表达式。建议使用 `^` 锚定模式到路径开头，以避免意外的子串匹配：

- 模式：`^/mnt/jfs/data/.*\.git.*` 匹配 `/mnt/jfs/data/project/.git/config`
- 模式：`^/mnt/jfs/secrets/.*` 匹配 `/mnt/jfs/secrets/api_key.txt`
- 不带 `^` 的模式：`/mnt/jfs/secrets/.*` 也会匹配 `/backup/mnt/jfs/secrets/foo`（非预期）

## 受影响的操作

### 写保护（readonly 和 deny 模式）

- 创建文件/目录
- 写入文件
- 删除文件/目录
- 重命名文件/目录
- 创建符号链接/硬链接
- 修改属性
- 设置/删除扩展属性

### 读保护（仅 deny 模式）

- 读取文件内容
- 查找文件/目录
- 列出目录内容
- 获取文件属性
- 获取扩展属性

## 错误码

| 错误 | 代码 | 描述 |
|------|------|------|
| `EPERM` | 1 | 操作不允许（受保护路径上的写操作） |
| `EACCES` | 13 | 权限被拒绝（deny 保护路径上的读操作） |

## 示例

```shell
# 测试写保护（应该失败）
touch /mnt/jfs/data/project/.git/test.txt
# 输出：touch: cannot touch '...': Operation not permitted

# 测试只读模式下允许读取（应该成功）
cat /mnt/jfs/data/project/.git/config
# 输出：成功

# 测试禁止模式下阻止读取（应该失败）
cat /mnt/jfs/secrets/api_key.txt
# 输出：cat: ...: Permission denied

# 测试禁止模式下阻止查找（应该失败）
ls /mnt/jfs/secrets/
# 输出：ls: cannot access ...: Permission denied
```

## 注意事项

1. 路径保护规则按顺序评估。第一个匹配的规则决定保护模式。
2. 模式必须匹配包含挂载点前缀的完整路径。建议使用 `^` 锚定模式。
3. 无效的正则表达式模式将导致挂载失败并显示错误消息。
