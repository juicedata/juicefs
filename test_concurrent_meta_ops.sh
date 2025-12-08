#!/bin/bash

# 测试脚本：复现多个进程同时访问同一挂载点时的重复 meta 请求问题
# 使用方法: ./test_concurrent_meta_ops.sh <mount_point> [num_processes] [test_file]

MOUNT_POINT="${1:-/mnt/jfs}"
NUM_PROCESSES="${2:-10}"
TEST_FILE="${3:-test_file.txt}"

echo "=========================================="
echo "JuiceFS 并发 Meta 请求测试脚本"
echo "=========================================="
echo "挂载点: $MOUNT_POINT"
echo "并发进程数: $NUM_PROCESSES"
echo "测试文件: $TEST_FILE"
echo "=========================================="
echo ""

# 检查挂载点是否存在
if [ ! -d "$MOUNT_POINT" ]; then
    echo "错误: 挂载点 $MOUNT_POINT 不存在"
    exit 1
fi

# 创建测试文件（如果不存在）
TEST_PATH="$MOUNT_POINT/$TEST_FILE"
if [ ! -f "$TEST_PATH" ]; then
    echo "创建测试文件: $TEST_PATH"
    echo "This is a test file for concurrent meta operations" > "$TEST_PATH"
fi

# 创建测试目录
TEST_DIR="$MOUNT_POINT/test_dir_$$"
mkdir -p "$TEST_DIR"
echo "创建测试目录: $TEST_DIR"

# 在测试目录中创建一些文件
for i in {1..5}; do
    echo "test content $i" > "$TEST_DIR/file_$i.txt"
done

echo ""
echo "开始并发测试..."
echo ""

# 创建临时目录存储各进程的日志
LOG_DIR="/tmp/jfs_test_$$"
mkdir -p "$LOG_DIR"

# 启动多个进程同时进行 getattr 和 lookup 操作
for i in $(seq 1 $NUM_PROCESSES); do
    (
        # 每个进程执行多次操作
        for j in {1..100}; do
            # 1. GetAttr 操作 - 获取文件属性
            stat "$TEST_PATH" > /dev/null 2>&1
            
            # 2. GetAttr 操作 - 获取目录属性
            stat "$TEST_DIR" > /dev/null 2>&1
            
            # 3. Lookup 操作 - 查找文件
            ls -l "$TEST_DIR/file_1.txt" > /dev/null 2>&1
            
            # 4. Lookup 操作 - 查找目录中的文件
            ls "$TEST_DIR" > /dev/null 2>&1
            
            # 5. 嵌套的 Lookup - 访问子目录
            if [ -d "$TEST_DIR/subdir" ]; then
                ls "$TEST_DIR/subdir" > /dev/null 2>&1
            fi
        done
        
        echo "进程 $i 完成" > "$LOG_DIR/process_$i.log"
    ) &
done

# 等待所有后台进程完成
echo "等待所有进程完成..."
wait

echo ""
echo "所有进程已完成"
echo ""

# 统计结果
echo "=========================================="
echo "测试完成，请查看以下观测方法："
echo "=========================================="
echo ""
echo "1. 查看访问日志（实时监控）:"
echo "   tail -f $MOUNT_POINT/.accesslog | grep -E '(getattr|lookup)'"
echo ""
echo "2. 统计访问日志中的 getattr/lookup 操作:"
echo "   grep -E '(getattr|lookup)' $MOUNT_POINT/.accesslog | wc -l"
echo ""
echo "3. 使用 juicefs profile 分析访问日志:"
echo "   cat $MOUNT_POINT/.accesslog > /tmp/jfs_test.alog"
echo "   juicefs profile /tmp/jfs_test.alog --interval 0"
echo ""
echo "4. 使用 juicefs stats 实时监控（在另一个终端）:"
echo "   juicefs stats $MOUNT_POINT -l 1"
echo ""
echo "5. 查看 Prometheus metrics（如果启用）:"
echo "   curl http://localhost:9567/metrics | grep meta_ops"
echo ""
echo "6. 分析日志中的重复请求（相同 inode/entry 的并发请求）:"
echo "   grep -E '(getattr|lookup)' $MOUNT_POINT/.accesslog | \\"
echo "     awk '{print \$NF}' | sort | uniq -c | sort -rn | head -20"
echo ""
echo "=========================================="
echo "清理测试文件（可选）:"
echo "  rm -rf $TEST_DIR"
echo "=========================================="

