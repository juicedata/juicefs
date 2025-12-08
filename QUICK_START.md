# 快速开始：复现并发 Meta 请求问题

## 一分钟快速测试

```bash
# 1. 确保 JuiceFS 已挂载（假设挂载点为 /mnt/jfs）
# 2. 运行测试脚本
./test_concurrent_meta_ops.sh /mnt/jfs 20

# 3. 在另一个终端实时监控
tail -f /mnt/jfs/.accesslog | grep -E '(getattr|lookup)'
```

## 完整测试流程

### 步骤 1: 运行测试

```bash
# 使用 Python 脚本（推荐，功能更丰富）
python3 test_concurrent_meta_ops.py /mnt/jfs --processes 20 --iterations 100 --analyze
```

### 步骤 2: 实时观测（在另一个终端）

```bash
# 方法1: 使用观测脚本
./observe_meta_ops.sh /mnt/jfs

# 方法2: 使用 juicefs profile
juicefs profile /mnt/jfs/.accesslog

# 方法3: 使用 juicefs stats
juicefs stats /mnt/jfs -l 1
```

### 步骤 3: 分析结果

```bash
# 查看访问日志中的并发请求
grep -E '(getattr|lookup)' /mnt/jfs/.accesslog | \
  awk '{print $1, $2}' | \
  awk -F'[:.]' '{print $1":"$2":"$3}' | \
  sort | uniq -c | sort -rn | head -20
```

## 识别问题特征

在访问日志中查找：
- ✅ 相同时间戳（或非常接近）
- ✅ 相同的 inode 号
- ✅ 不同的 PID（不同进程）
- ✅ 相同的操作类型（getattr/lookup）

示例：
```
2024.01.15 10:23:45.123456 [uid:1000,gid:1000,pid:12345] getattr (12345): OK <0.001>
2024.01.15 10:23:45.123457 [uid:1000,gid:1000,pid:12346] getattr (12345): OK <0.001>
2024.01.15 10:23:45.123458 [uid:1000,gid:1000,pid:12347] getattr (12345): OK <0.001>
```

## 常用命令

```bash
# 统计 getattr/lookup 操作数
grep -E '(getattr|lookup)' /mnt/jfs/.accesslog | wc -l

# 查看最频繁访问的 inode
grep 'getattr' /mnt/jfs/.accesslog | grep -oP '\([0-9]+\)' | sort | uniq -c | sort -rn | head -10

# 查看 Prometheus metrics
curl -s http://localhost:9567/metrics | grep meta_ops_total
```

## 详细文档

查看 `TEST_CONCURRENT_META_OPS.md` 获取完整文档。

