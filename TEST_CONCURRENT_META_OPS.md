# JuiceFS 并发 Meta 请求问题复现指南

本指南帮助您复现和观测多个进程同时访问同一挂载点时，对同一个 inode/entry 发出相同请求（getattr/lookup）的问题。

## 问题背景

在 Kubernetes 等高并发场景下，多个进程（尤其是相同镜像生成的容器）可能同时访问同一个挂载点，对同一个 inode/entry 同时发起 `getattr`/`lookup` 请求。这会导致：

1. **重复的 meta 请求**：多个进程同时请求相同的元数据
2. **meta engine 压力增加**：大量重复请求增加数据库/Redis 的负载
3. **性能下降**：重复的网络往返和数据库查询

## 准备工作

### 1. 确保 JuiceFS 已挂载并启用访问日志

```bash
# 挂载时启用访问日志
juicefs mount <meta-url> <mount-point> --access-log <mount-point>/.accesslog

# 或者如果已经挂载，可以通过配置启用
# 访问日志会自动创建在挂载点的 .accesslog 文件
```

### 2. 验证访问日志

```bash
# 检查访问日志是否存在
ls -lh <mount-point>/.accesslog

# 查看最近的访问日志
tail -f <mount-point>/.accesslog
```

## 测试脚本

### 方法一：使用 Bash 脚本（简单快速）

```bash
# 基本用法
./test_concurrent_meta_ops.sh <mount-point> [num-processes] [test-file]

# 示例：使用默认参数（10个进程）
./test_concurrent_meta_ops.sh /mnt/jfs

# 示例：指定20个并发进程
./test_concurrent_meta_ops.sh /mnt/jfs 20

# 示例：指定挂载点和测试文件
./test_concurrent_meta_ops.sh /mnt/jfs 15 my_test_file.txt
```

### 方法二：使用 Python 脚本（功能更丰富）

```bash
# 基本用法
python3 test_concurrent_meta_ops.py <mount-point> [options]

# 示例：使用默认参数
python3 test_concurrent_meta_ops.py /mnt/jfs

# 示例：指定进程数和迭代次数
python3 test_concurrent_meta_ops.py /mnt/jfs --processes 20 --iterations 200

# 示例：测试后自动分析访问日志
python3 test_concurrent_meta_ops.py /mnt/jfs --processes 20 --analyze

# 查看所有选项
python3 test_concurrent_meta_ops.py --help
```

## 观测方法

### 1. 实时监控访问日志

在测试运行时，在另一个终端执行：

```bash
# 实时查看 getattr/lookup 操作
tail -f <mount-point>/.accesslog | grep -E '(getattr|lookup)'

# 或者使用观测脚本
./observe_meta_ops.sh <mount-point>
```

### 2. 使用 juicefs profile 分析

```bash
# 导出访问日志
cat <mount-point>/.accesslog > /tmp/jfs_test.alog

# 分析访问日志
juicefs profile /tmp/jfs_test.alog --interval 0

# 实时监控（在测试运行时）
juicefs profile <mount-point>/.accesslog
```

### 3. 使用 juicefs stats 实时监控

```bash
# 在另一个终端运行，实时查看统计信息
juicefs stats <mount-point> -l 1

# 重点关注以下指标：
# - meta_ops_total: 总 meta 操作数
# - meta_ops_durations_histogram_seconds: meta 操作耗时分布
```

### 4. 查看 Prometheus Metrics（如果启用）

```bash
# 如果启用了 Prometheus metrics（默认端口 9567）
curl http://localhost:9567/metrics | grep meta_ops

# 关键指标：
# - meta_ops_total{method="GetAttr"}
# - meta_ops_total{method="Lookup"}
# - meta_ops_durations_histogram_seconds{method="GetAttr"}
# - meta_ops_durations_histogram_seconds{method="Lookup"}
```

### 5. 分析重复请求

```bash
# 统计相同 inode 的并发请求
grep -E '(getattr|lookup)' <mount-point>/.accesslog | \
  awk '{print $NF}' | sort | uniq -c | sort -rn | head -20

# 分析相同时间窗口内的并发请求
grep -E '(getattr|lookup)' <mount-point>/.accesslog | \
  awk '{print $1, $2}' | \
  awk -F'[:.]' '{print $1":"$2":"$3}' | \
  sort | uniq -c | sort -rn | head -20
```

### 6. 使用观测脚本（推荐）

```bash
./observe_meta_ops.sh <mount-point>
```

脚本提供以下功能：
1. 实时监控 GetAttr/Lookup 操作
2. 统计操作数量
3. 分析并发请求（相同时间窗口内的重复请求）
4. 分析最频繁访问的 inode/entry
5. 导出日志并分析（使用 juicefs profile）
6. 持续监控（每5秒更新一次统计）

## 如何识别问题

### 1. 查看访问日志中的重复请求

在访问日志中，您会看到类似这样的模式：

```
2024.01.15 10:23:45.123456 [uid:1000,gid:1000,pid:12345] getattr (12345): OK <0.001234>
2024.01.15 10:23:45.123457 [uid:1000,gid:1000,pid:12346] getattr (12345): OK <0.001235>
2024.01.15 10:23:45.123458 [uid:1000,gid:1000,pid:12347] getattr (12345): OK <0.001236>
```

**关键特征**：
- 相同的时间戳（或非常接近）
- 相同的 inode 号（括号中的数字）
- 不同的 PID（不同的进程）
- 相同的操作类型（getattr 或 lookup）

### 2. 统计并发请求数量

```bash
# 统计同一秒内的操作数
grep -E '(getattr|lookup)' <mount-point>/.accesslog | \
  awk '{print $1, $2}' | \
  awk -F'[:.]' '{print $1":"$2":"$3}' | \
  sort | uniq -c | sort -rn | head -20
```

如果看到同一秒内有大量操作（例如 > 10），说明存在并发请求。

### 3. 查看 meta_ops_total 指标

```bash
# 如果启用了 Prometheus metrics
curl -s http://localhost:9567/metrics | grep 'meta_ops_total{method="GetAttr"}' | head -1
curl -s http://localhost:9567/metrics | grep 'meta_ops_total{method="Lookup"}' | head -1
```

如果操作数远大于实际需要的操作数（例如，10个进程访问同一个文件，理论上只需要1次 getattr，但实际可能有10次），说明存在重复请求。

## 预期结果

### 问题复现成功时，您应该看到：

1. **访问日志中**：
   - 多个进程在同一时间（或非常接近的时间）访问相同的 inode
   - 相同 inode 的 getattr 请求数量 = 并发进程数

2. **统计信息中**：
   - `meta_ops_total{method="GetAttr"}` 和 `meta_ops_total{method="Lookup"}` 的值远大于实际需要的操作数
   - 同一时间窗口内的操作数 = 并发进程数

3. **性能影响**：
   - meta engine（Redis/SQL/TKV）的负载增加
   - 如果 meta engine 是远程的，网络流量增加

## 优化后的预期效果

如果实现了 single flight 优化，您应该看到：

1. **访问日志中**：
   - 相同 inode 的并发请求被合并
   - 实际发送到 meta engine 的请求数 < 并发进程数

2. **统计信息中**：
   - `meta_ops_total` 的值接近实际需要的操作数
   - 相同时间窗口内的操作数减少

3. **性能提升**：
   - meta engine 负载降低
   - 网络流量减少
   - 响应时间可能略有改善（因为减少了重复的数据库查询）

## 故障排查

### 问题：访问日志不存在

**解决方案**：
1. 确保挂载时启用了访问日志：`--access-log <path>`
2. 检查挂载点权限
3. 检查 `.accesslog` 文件是否被隐藏（某些配置可能隐藏内部文件）

### 问题：看不到并发请求

**可能原因**：
1. 进程启动时间不同步，导致请求分散在不同时间
2. 文件系统缓存命中，导致请求被缓存拦截

**解决方案**：
1. 增加并发进程数
2. 增加每个进程的迭代次数
3. 清除文件系统缓存（如果可能）
4. 使用不同的文件/目录进行测试

### 问题：测试脚本执行失败

**检查项**：
1. 挂载点是否存在且可访问
2. 是否有写入权限
3. Python 脚本依赖是否满足（multiprocessing 是标准库）

## 进阶测试

### 模拟 Kubernetes 场景

```bash
# 使用 Docker 容器模拟多个 Pod
for i in {1..10}; do
  docker run -d --rm \
    -v <mount-point>:/mnt/jfs:shared \
    alpine sh -c "while true; do stat /mnt/jfs/test_file.txt; sleep 0.1; done"
done
```

### 压力测试

```bash
# 使用 Python 脚本进行长时间压力测试
python3 test_concurrent_meta_ops.py /mnt/jfs \
  --processes 50 \
  --iterations 1000 \
  --analyze
```

## 相关文件

- `test_concurrent_meta_ops.sh` - Bash 测试脚本
- `test_concurrent_meta_ops.py` - Python 测试脚本（功能更丰富）
- `observe_meta_ops.sh` - 观测和分析脚本

## 参考

- [JuiceFS 访问日志文档](https://juicefs.com/docs/administration/fault_diagnosis_and_analysis#access-log)
- [JuiceFS 性能监控文档](https://juicefs.com/docs/administration/fault_diagnosis_and_analysis#performance-monitor)
- [JuiceFS 内部架构文档](https://juicefs.com/docs/development/internals)

