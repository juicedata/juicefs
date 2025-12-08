#!/bin/bash

# JuiceFS Meta 操作观测脚本
# 用于实时监控和分析 getattr/lookup 操作的并发情况

MOUNT_POINT="${1:-/mnt/jfs}"
ACCESS_LOG="$MOUNT_POINT/.accesslog"

if [ ! -d "$MOUNT_POINT" ]; then
    echo "错误: 挂载点不存在: $MOUNT_POINT"
    exit 1
fi

echo "=========================================="
echo "JuiceFS Meta 操作观测工具"
echo "=========================================="
echo "挂载点: $MOUNT_POINT"
echo "访问日志: $ACCESS_LOG"
echo "=========================================="
echo ""

# 检查访问日志是否存在
if [ ! -f "$ACCESS_LOG" ]; then
    echo "警告: 访问日志不存在，请确保挂载时启用了访问日志"
    echo "挂载命令示例: juicefs mount ... --access-log $MOUNT_POINT/.accesslog"
    echo ""
fi

# 创建临时分析脚本
TMP_SCRIPT="/tmp/jfs_observe_$$.sh"
cat > "$TMP_SCRIPT" << 'EOF'
#!/bin/bash
ACCESS_LOG="$1"

if [ ! -f "$ACCESS_LOG" ]; then
    echo "访问日志不存在"
    exit 1
fi

echo "=== 实时监控 GetAttr/Lookup 操作 ==="
echo "按 Ctrl+C 停止"
echo ""

# 实时监控
tail -f "$ACCESS_LOG" 2>/dev/null | while IFS= read -r line; do
    if echo "$line" | grep -qE '(getattr|lookup)'; then
        # 提取关键信息
        timestamp=$(echo "$line" | awk '{print $1, $2}')
        pid=$(echo "$line" | grep -oP 'pid:\K[0-9]+')
        operation=$(echo "$line" | grep -oP '(getattr|lookup)')
        params=$(echo "$line" | grep -oP '\([^)]+\)')
        duration=$(echo "$line" | grep -oP '<\K[0-9.]+')
        
        printf "[%s] PID:%s %s %s <%.6fs>\n" "$timestamp" "$pid" "$operation" "$params" "$duration"
    fi
done
EOF

chmod +x "$TMP_SCRIPT"

# 菜单选择
echo "请选择观测模式:"
echo "1. 实时监控 GetAttr/Lookup 操作"
echo "2. 统计操作数量"
echo "3. 分析并发请求（相同时间窗口内的重复请求）"
echo "4. 分析最频繁访问的 inode/entry"
echo "5. 导出日志并分析（使用 juicefs profile）"
echo "6. 持续监控（每5秒更新一次统计）"
echo ""
read -p "请选择 (1-6): " choice

case $choice in
    1)
        echo ""
        echo "开始实时监控..."
        "$TMP_SCRIPT" "$ACCESS_LOG"
        ;;
    2)
        echo ""
        echo "=== 操作统计 ==="
        if [ -f "$ACCESS_LOG" ]; then
            total=$(grep -E '(getattr|lookup)' "$ACCESS_LOG" 2>/dev/null | wc -l)
            getattr=$(grep 'getattr' "$ACCESS_LOG" 2>/dev/null | wc -l)
            lookup=$(grep 'lookup' "$ACCESS_LOG" 2>/dev/null | wc -l)
            echo "总 GetAttr/Lookup 操作: $total"
            echo "GetAttr 操作: $getattr"
            echo "Lookup 操作: $lookup"
        else
            echo "访问日志不存在"
        fi
        ;;
    3)
        echo ""
        echo "=== 并发请求分析 ==="
        echo "分析相同时间窗口内的重复请求..."
        echo ""
        if [ -f "$ACCESS_LOG" ]; then
            # 按时间窗口（秒级）分组，统计每秒钟的操作数
            echo "每秒操作数统计:"
            grep -E '(getattr|lookup)' "$ACCESS_LOG" 2>/dev/null | \
                awk '{print $1, $2}' | \
                awk -F'[:.]' '{print $1":"$2":"$3}' | \
                sort | uniq -c | sort -rn | head -20
            
            echo ""
            echo "相同 inode 的并发 GetAttr 请求（最近50条）:"
            grep 'getattr' "$ACCESS_LOG" 2>/dev/null | tail -50 | \
                grep -oP '\([0-9]+\)' | sort | uniq -c | sort -rn | head -10
        else
            echo "访问日志不存在"
        fi
        ;;
    4)
        echo ""
        echo "=== 最频繁访问的 inode/entry ==="
        if [ -f "$ACCESS_LOG" ]; then
            echo "最频繁访问的 inode (GetAttr):"
            grep 'getattr' "$ACCESS_LOG" 2>/dev/null | \
                grep -oP '\([0-9]+\)' | sort | uniq -c | sort -rn | head -10
            
            echo ""
            echo "最频繁查找的 entry (Lookup):"
            grep 'lookup' "$ACCESS_LOG" 2>/dev/null | \
                grep -oP '\([0-9]+,[^)]+\)' | sort | uniq -c | sort -rn | head -10
        else
            echo "访问日志不存在"
        fi
        ;;
    5)
        echo ""
        echo "=== 导出并分析访问日志 ==="
        export_file="/tmp/jfs_profile_$$.alog"
        if [ -f "$ACCESS_LOG" ]; then
            echo "导出访问日志到: $export_file"
            cp "$ACCESS_LOG" "$export_file"
            echo ""
            echo "使用 juicefs profile 分析..."
            if command -v juicefs &> /dev/null; then
                juicefs profile "$export_file" --interval 0
            else
                echo "错误: 未找到 juicefs 命令"
                echo "请手动运行: juicefs profile $export_file --interval 0"
            fi
        else
            echo "访问日志不存在"
        fi
        ;;
    6)
        echo ""
        echo "=== 持续监控（每5秒更新）==="
        echo "按 Ctrl+C 停止"
        echo ""
        while true; do
            clear
            echo "=========================================="
            echo "JuiceFS Meta 操作统计 - $(date '+%Y-%m-%d %H:%M:%S')"
            echo "=========================================="
            if [ -f "$ACCESS_LOG" ]; then
                total=$(grep -E '(getattr|lookup)' "$ACCESS_LOG" 2>/dev/null | wc -l)
                getattr=$(grep 'getattr' "$ACCESS_LOG" 2>/dev/null | wc -l)
                lookup=$(grep 'lookup' "$ACCESS_LOG" 2>/dev/null | wc -l)
                echo "总操作数: $total"
                echo "GetAttr: $getattr"
                echo "Lookup: $lookup"
                echo ""
                echo "最近10条 GetAttr/Lookup 操作:"
                grep -E '(getattr|lookup)' "$ACCESS_LOG" 2>/dev/null | tail -10
            else
                echo "访问日志不存在"
            fi
            sleep 5
        done
        ;;
    *)
        echo "无效选择"
        ;;
esac

# 清理
rm -f "$TMP_SCRIPT"

