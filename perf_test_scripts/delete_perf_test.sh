#!/bin/bash
#
# JuiceFS 删除性能综合测试脚本（客户端版本）
# 测试内容：
#   1. 不同元数据引擎下的删除能力极限（小文件个数/大文件总数据量）
#   2. 后台清理任务效率（gc, cleanup-trash, cleanup-slices, compact）
#   3. batchunlink/batchclone 优化前后对比（三种元数据引擎）
#   4. rmr 命令性能测试（递归删除大目录）
#
# 使用方式：
#   ./delete_perf_test.sh [测试类型] [元数据引擎]
#
# 环境要求：
#   - Linux 系统（推荐 Ubuntu 22.04+）
#   - JuiceFS 已编译安装
#   - 元数据引擎已部署（Redis/MySQL/TiKV）
#   - 对象存储已配置
#   - 如果是双机部署：元数据服务器和对象存储需可访问
#

set -euo pipefail

# ==================== 配置区域 ====================
# 元数据引擎连接串（请根据实际情况修改）
# 双机部署时，将 localhost 替换为元数据服务器 IP
REDIS_META="redis://localhost:6379/1"
MYSQL_META="mysql://user:password@tcp(localhost:3306)/juicefs"
TIKV_META="tikv://localhost:2379/juicefs"
SQLITE_META="sqlite3:///tmp/juicefs_test.db"

# 对象存储配置（请根据实际情况修改）
# 双机部署时，确保对象存储可被客户端访问
# 格式: --storage s3 --bucket http://bucket.endpoint --access-key xxx --secret-key yyy
OBJECT_STORAGE="--storage file --bucket /tmp/juicefs-objstore"

# 测试挂载点
MOUNT_POINT="/tmp/juicefs-mount"

# 测试结果输出目录
RESULT_DIR="/tmp/juicefs-delete-test-results-$(date +%Y%m%d-%H%M%S)"

# 测试参数（已调大以触及性能极限）
# 小文件测试：100万 个文件，分布在 1000 个目录中
SMALL_FILE_COUNT=1000000     # 小文件删除测试：文件数量（100万）
SMALL_FILE_DIRS=1000         # 小文件分布的目录数
SMALL_FILE_SIZE=4096         # 小文件大小（4KB）

# 大文件测试：1万 个文件，总数据量 1TB
LARGE_FILE_COUNT=10000       # 大文件删除测试：文件数量（1万）
LARGE_FILE_SIZE=$((1024*1024*100))  # 大文件大小（100MB），总数据量约 1TB

# BatchUnlink/BatchClone 测试规模
BATCH_UNLINK_COUNT=100000    # batchunlink 测试文件数量（10万）
BATCH_CLONE_COUNT=100000     # batchclone 测试文件数量（10万）

# RMR 测试规模
RMR_DIR_COUNT=1000           # rmr 测试：子目录数量
RMR_FILES_PER_DIR=1000       # rmr 测试：每个子目录文件数
# 总计：100万 个文件，分布在 1000 个目录中

TEST_THREADS=16              # 并发线程数（已增大）

# ==================== 工具函数 ====================

log_info() {
    echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

log_error() {
    echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') - $*" >&2
}

log_section() {
    echo ""
    echo "========================================"
    echo "  $*"
    echo "========================================"
}

# 获取元数据引擎名称
get_engine_name() {
    local meta_url="$1"
    if [[ "$meta_url" == redis://* ]]; then
        echo "redis"
    elif [[ "$meta_url" == mysql://* ]] || [[ "$meta_url" == mysql://* ]]; then
        echo "mysql"
    elif [[ "$meta_url" == tikv://* ]]; then
        echo "tikv"
    elif [[ "$meta_url" == sqlite3://* ]]; then
        echo "sqlite"
    else
        echo "unknown"
    fi
}

# 格式化输出（类似 C 语言的 printf）
format_number() {
    local num="$1"
    echo "$num" | sed ':a;s/\B[0-9]\{3\}\>/,&/;ta'
}

# 创建测试目录
create_test_dir() {
    local test_name="$1"
    local engine="$2"
    local test_dir="${RESULT_DIR}/${engine}/${test_name}"
    mkdir -p "$test_dir"
    echo "$test_dir"
}

# 记录测试结果
record_result() {
    local result_file="$1"
    local metric="$2"
    local value="$3"
    local unit="$4"
    echo "${metric}|${value}|${unit}|$(date +%s)" >> "$result_file"
}

# 格式化时间输出（秒 -> 小时:分钟:秒）
format_time() {
    local seconds="$1"
    local h=$((seconds / 3600))
    local m=$(((seconds % 3600) / 60))
    local s=$((seconds % 60))
    printf "%02d:%02d:%02d" "$h" "$m" "$s"
}

# 估算测试所需磁盘空间
estimate_space() {
    local test_name="$1"
    local size_mb=0
    case "$test_name" in
        small)
            size_mb=$((SMALL_FILE_COUNT * SMALL_FILE_SIZE / 1024 / 1024))
            ;;
        large)
            size_mb=$((LARGE_FILE_COUNT * LARGE_FILE_SIZE / 1024 / 1024))
            ;;
        batchunlink)
            size_mb=$((BATCH_UNLINK_COUNT * SMALL_FILE_SIZE / 1024 / 1024))
            ;;
        batchclone)
            size_mb=$((BATCH_CLONE_COUNT * SMALL_FILE_SIZE / 1024 / 1024 * 2))
            ;;
        rmr)
            size_mb=$((RMR_DIR_COUNT * RMR_FILES_PER_DIR * SMALL_FILE_SIZE / 1024 / 1024))
            ;;
        mixed)
            size_mb=$((SMALL_FILE_COUNT * SMALL_FILE_SIZE / 1024 / 1024))
            ;;
    esac
    echo "$size_mb"
}

# ==================== JuiceFS 操作函数 ====================

# 格式化文件系统
juicefs_format() {
    local meta_url="$1"
    local name="$2"
    log_info "格式化 JuiceFS: $name"
    juicefs format "$meta_url" "$name" $OBJECT_STORAGE --trash-days 1
}

# 挂载文件系统
juicefs_mount() {
    local meta_url="$1"
    log_info "挂载 JuiceFS 到 $MOUNT_POINT"
    mkdir -p "$MOUNT_POINT"
    if mountpoint -q "$MOUNT_POINT"; then
        umount "$MOUNT_POINT" 2>/dev/null || true
        sleep 1
    fi
    juicefs mount "$meta_url" "$MOUNT_POINT" --background --no-usage-report
    sleep 2
}

# 卸载文件系统
juicefs_umount() {
    log_info "卸载 JuiceFS"
    if mountpoint -q "$MOUNT_POINT"; then
        umount "$MOUNT_POINT" 2>/dev/null || true
        sleep 1
    fi
}

# 销毁文件系统
juicefs_destroy() {
    local meta_url="$1"
    log_info "销毁 JuiceFS"
    juicefs destroy "$meta_url" --force 2>/dev/null || true
}

# ==================== 测试场景 1: 小文件删除性能 ====================
# 测试每小时可删除多少个小文件（100万级别）
test_small_file_delete() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "small_file_delete" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/small_files"
    local total_size_mb=$((SMALL_FILE_COUNT * SMALL_FILE_SIZE / 1024 / 1024))

    log_section "测试 1: 小文件删除性能 [$engine]"
    log_info "文件数量: $(format_number $SMALL_FILE_COUNT), 文件大小: ${SMALL_FILE_SIZE} bytes"
    log_info "分布目录: $(format_number $SMALL_FILE_DIRS) 个, 总数据量: $(format_number $total_size_mb) MB"
    log_info "预计所需空间: $(format_number $total_size_mb) MB"

    # 检查磁盘空间
    local avail_mb=$(df -m "$MOUNT_POINT" | awk 'NR==2 {print $4}')
    if [ "$avail_mb" -lt "$((total_size_mb * 2))" ]; then
        log_error "磁盘空间不足: 可用 ${avail_mb}MB, 需要 ${total_size_mb}MB"
        return 1
    fi

    # 创建测试文件（分布在多个目录中）
    log_info "创建测试文件..."
    mkdir -p "$data_dir"
    local start_time
    start_time=$(date +%s)

    # 使用多进程创建小文件
    local pids=()
    local dirs_per_thread=$((SMALL_FILE_DIRS / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local dir_start=$((t * dirs_per_thread))
            local dir_end=$((dir_start + dirs_per_thread))
            for d in $(seq "$dir_start" "$((dir_end - 1))"); do
                local dir="${data_dir}/batch_${d}"
                mkdir -p "$dir"
                local files_per_dir=$((SMALL_FILE_COUNT / SMALL_FILE_DIRS))
                for i in $(seq 1 "$files_per_dir"); do
                    local idx=$((d * files_per_dir + i))
                    # 使用 printf 生成零字节文件（更快）
                    printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${dir}/file_${idx}.txt" 2>/dev/null || \
                        dd if=/dev/zero of="${dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
                done
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done

    local create_end=$(date +%s)
    local create_time=$((create_end - start_time))
    log_info "创建完成: $(format_number $SMALL_FILE_COUNT) 个文件, 耗时: $(format_time $create_time)"
    record_result "$result_file" "small_file_create_time" "$create_time" "seconds"

    sync
    sleep 2

    # 执行删除测试 - rm -rf
    log_info "开始删除测试 (rm -rf)..."
    local delete_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local delete_end=$(date +%s)
    local delete_time=$((delete_end - delete_start))
    local files_per_hour=$((SMALL_FILE_COUNT * 3600 / delete_time))

    log_info "rm -rf 删除完成: 耗时: $(format_time $delete_time)"
    log_info "删除速率: $(format_number $files_per_hour) 文件/小时"
    record_result "$result_file" "rmrf_delete_time" "$delete_time" "seconds"
    record_result "$result_file" "rmrf_delete_rate" "$files_per_hour" "files/hour"

    # 等待后台清理完成
    log_info "等待后台清理任务完成..."
    sleep 30

    # 测试 GC 效率
    test_gc_efficiency "$meta_url" "$test_dir"
}

# ==================== 测试场景 2: 大文件删除性能 ====================
# 测试每小时可删除多少数据量（1TB级别）
test_large_file_delete() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "large_file_delete" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/large_files"
    local total_size_mb=$((LARGE_FILE_COUNT * LARGE_FILE_SIZE / 1024 / 1024))
    local total_size_gb=$((total_size_mb / 1024))

    log_section "测试 2: 大文件删除性能 [$engine]"
    log_info "文件数量: $(format_number $LARGE_FILE_COUNT), 单文件大小: $((LARGE_FILE_SIZE / 1024 / 1024)) MB"
    log_info "总大小: $(format_number $total_size_gb) GB ($(format_number $total_size_mb) MB)"
    log_info "预计所需空间: $(format_number $total_size_gb) GB"

    # 检查磁盘空间
    local avail_mb=$(df -m "$MOUNT_POINT" | awk 'NR==2 {print $4}')
    if [ "$avail_mb" -lt "$((total_size_mb * 2))" ]; then
        log_error "磁盘空间不足: 可用 ${avail_mb}MB, 需要 ${total_size_mb}MB"
        return 1
    fi

    # 创建测试文件
    log_info "创建测试文件..."
    mkdir -p "$data_dir"
    local start_time
    start_time=$(date +%s)

    local pids=()
    local files_per_thread=$((LARGE_FILE_COUNT / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                dd if=/dev/zero of="${data_dir}/large_file_${idx}.bin" bs=1M count=$((LARGE_FILE_SIZE / 1024 / 1024)) 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done

    local create_end=$(date +%s)
    local create_time=$((create_end - start_time))
    log_info "创建完成: $(format_number $LARGE_FILE_COUNT) 个文件, 耗时: $(format_time $create_time)"
    record_result "$result_file" "large_file_create_time" "$create_time" "seconds"

    sync
    sleep 2

    # 执行删除测试 - rm -rf
    log_info "开始删除测试 (rm -rf)..."
    local delete_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local delete_end=$(date +%s)
    local delete_time=$((delete_end - delete_start))
    local mb_per_hour=$((total_size_mb * 3600 / delete_time))
    local gb_per_hour=$((mb_per_hour / 1024))

    log_info "rm -rf 删除完成: 耗时: $(format_time $delete_time)"
    log_info "删除速率: $(format_number $mb_per_hour) MB/小时 ($(format_number $gb_per_hour) GB/小时)"
    record_result "$result_file" "rmrf_delete_time" "$delete_time" "seconds"
    record_result "$result_file" "rmrf_delete_rate_mb" "$mb_per_hour" "MB/hour"
    record_result "$result_file" "rmrf_delete_rate_gb" "$gb_per_hour" "GB/hour"

    # 测试 GC 效率
    sleep 30
    test_gc_efficiency "$meta_url" "$test_dir"
}

# ==================== 测试场景 3: 后台清理任务效率 ====================
test_gc_efficiency() {
    local meta_url="$1"
    local test_dir="$2"
    local result_file="${test_dir}/gc_results.txt"

    log_info "测试 GC 效率..."

    # 测试 1: GC 扫描速度（只扫描不删除）
    log_info "GC 扫描测试（只扫描）..."
    local scan_start=$(date +%s)
    juicefs gc "$meta_url" --threads 10 2>&1 | tee "${test_dir}/gc_scan.log"
    local scan_end=$(date +%s)
    local scan_time=$((scan_end - scan_start))
    log_info "GC 扫描耗时: $(format_time $scan_time)"
    record_result "$result_file" "gc_scan_time" "$scan_time" "seconds"

    # 测试 2: GC 清理速度（扫描+删除）
    log_info "GC 清理测试（扫描+删除）..."
    local cleanup_start=$(date +%s)
    juicefs gc "$meta_url" --delete --threads 10 2>&1 | tee "${test_dir}/gc_cleanup.log"
    local cleanup_end=$(date +%s)
    local cleanup_time=$((cleanup_end - cleanup_start))
    log_info "GC 清理耗时: $(format_time $cleanup_time)"
    record_result "$result_file" "gc_cleanup_time" "$cleanup_time" "seconds"

    # 测试 3: Compact 效率
    log_info "Compact 测试..."
    local compact_start=$(date +%s)
    juicefs gc "$meta_url" --compact --threads 10 2>&1 | tee "${test_dir}/gc_compact.log"
    local compact_end=$(date +%s)
    local compact_time=$((compact_end - compact_start))
    log_info "Compact 耗时: $(format_time $compact_time)"
    record_result "$result_file" "compact_time" "$compact_time" "seconds"
}

# ==================== 测试场景 4: BatchUnlink 优化对比 ====================
# 对比优化前后的批量删除性能
test_batchunlink_comparison() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "batchunlink" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/batch_unlink_files"

    log_section "测试 4: BatchUnlink 优化对比 [$engine]"
    log_info "测试文件数量: $(format_number $BATCH_UNLINK_COUNT)"

    # 创建测试文件（单目录，触发 batchunlink）
    log_info "创建测试文件（单目录）..."
    mkdir -p "$data_dir"
    local start_time
    start_time=$(date +%s)

    # 使用多进程创建
    local pids=()
    local files_per_thread=$((BATCH_UNLINK_COUNT / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${data_dir}/file_${idx}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${data_dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done

    local create_end=$(date +%s)
    log_info "创建完成: 耗时: $(format_time $((create_end - start_time)))"

    sync
    sleep 2

    # 测试 1: 逐文件删除（模拟优化前的行为）
    log_info "测试逐文件删除..."
    # 重新创建文件
    rm -rf "$data_dir"
    mkdir -p "$data_dir"
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${data_dir}/file_${idx}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${data_dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done
    sync

    local single_start=$(date +%s)
    # 使用 find + rm 逐文件删除
    find "$data_dir" -type f -exec rm -f {} \;
    sync
    local single_end=$(date +%s)
    local single_time=$((single_end - single_start))
    local single_rate=$((BATCH_UNLINK_COUNT * 3600 / single_time))

    log_info "逐文件删除: 耗时: $(format_time $single_time), 速率: $(format_number $single_rate) 文件/小时"
    record_result "$result_file" "single_unlink_time" "$single_time" "seconds"
    record_result "$result_file" "single_unlink_rate" "$single_rate" "files/hour"

    # 测试 2: 批量删除（触发 batchunlink 优化）
    log_info "测试批量删除（rm -rf 触发 batchunlink）..."
    # 重新创建文件
    rm -rf "$data_dir"
    mkdir -p "$data_dir"
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${data_dir}/file_${idx}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${data_dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done
    sync

    local batch_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local batch_end=$(date +%s)
    local batch_time=$((batch_end - batch_start))
    local batch_rate=$((BATCH_UNLINK_COUNT * 3600 / batch_time))

    log_info "批量删除: 耗时: $(format_time $batch_time), 速率: $(format_number $batch_rate) 文件/小时"
    record_result "$result_file" "batch_unlink_time" "$batch_time" "seconds"
    record_result "$result_file" "batch_unlink_rate" "$batch_rate" "files/hour"

    # 计算提升比例
    if [ "$single_time" -gt 0 ]; then
        local improvement=$((single_time * 100 / batch_time - 100))
        log_info "BatchUnlink 提升: ${improvement}%"
        record_result "$result_file" "batch_unlink_improvement" "$improvement" "percent"
    fi
}

# ==================== 测试场景 5: BatchClone 优化对比 ====================
# 对比优化前后的批量克隆性能
test_batchclone_comparison() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "batchclone" "$engine")
    local result_file="${test_dir}/results.txt"
    local src_dir="${MOUNT_POINT}/clone_src"
    local dst_dir="${MOUNT_POINT}/clone_dst"

    log_section "测试 5: BatchClone 优化对比 [$engine]"
    log_info "测试文件数量: $(format_number $BATCH_CLONE_COUNT)"

    # 创建源文件
    log_info "创建源文件..."
    mkdir -p "$src_dir"
    local start_time
    start_time=$(date +%s)

    local pids=()
    local files_per_thread=$((BATCH_CLONE_COUNT / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${src_dir}/file_${idx}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${src_dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done

    local create_end=$(date +%s)
    log_info "创建完成: 耗时: $(format_time $((create_end - start_time)))"

    sync
    sleep 2

    # 测试 1: 逐文件复制（模拟优化前的行为）
    log_info "测试逐文件复制..."
    mkdir -p "$dst_dir"

    local single_start=$(date +%s)
    find "$src_dir" -type f | while read -r f; do
        cp -a "$f" "${dst_dir}/$(basename "$f")"
    done
    sync
    local single_end=$(date +%s)
    local single_time=$((single_end - single_start))
    local single_rate=$((BATCH_CLONE_COUNT * 3600 / single_time))

    log_info "逐文件复制: 耗时: $(format_time $single_time), 速率: $(format_number $single_rate) 文件/小时"
    record_result "$result_file" "single_clone_time" "$single_time" "seconds"
    record_result "$result_file" "single_clone_rate" "$single_rate" "files/hour"

    rm -rf "$dst_dir"

    # 测试 2: 批量克隆（触发 batchclone 优化，使用 cp -r）
    log_info "测试批量克隆（cp -r 触发 batchclone）..."

    local batch_start=$(date +%s)
    cp -r "$src_dir" "$dst_dir"
    sync
    local batch_end=$(date +%s)
    local batch_time=$((batch_end - batch_start))
    local batch_rate=$((BATCH_CLONE_COUNT * 3600 / batch_time))

    log_info "批量克隆: 耗时: $(format_time $batch_time), 速率: $(format_number $batch_rate) 文件/小时"
    record_result "$result_file" "batch_clone_time" "$batch_time" "seconds"
    record_result "$result_file" "batch_clone_rate" "$batch_rate" "files/hour"

    # 计算提升比例
    if [ "$single_time" -gt 0 ]; then
        local improvement=$((single_time * 100 / batch_time - 100))
        log_info "BatchClone 提升: ${improvement}%"
        record_result "$result_file" "batch_clone_improvement" "$improvement" "percent"
    fi

    # 清理
    rm -rf "$src_dir" "$dst_dir"
}

# ==================== 测试场景 6: RMR 命令性能测试 ====================
# 测试 juicefs rmr 递归删除大目录的性能
test_rmr_performance() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "rmr" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/rmr_test"
    local total_files=$((RMR_DIR_COUNT * RMR_FILES_PER_DIR))
    local total_size_mb=$((total_files * SMALL_FILE_SIZE / 1024 / 1024))

    log_section "测试 6: RMR 命令性能 [$engine]"
    log_info "子目录数: $(format_number $RMR_DIR_COUNT), 每目录文件数: $(format_number $RMR_FILES_PER_DIR)"
    log_info "总文件数: $(format_number $total_files), 总数据量: $(format_number $total_size_mb) MB"

    # 创建深层目录结构
    log_info "创建测试目录结构..."
    mkdir -p "$data_dir"
    local start_time
    start_time=$(date +%s)

    local pids=()
    local dirs_per_thread=$((RMR_DIR_COUNT / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local dir_start=$((t * dirs_per_thread))
            local dir_end=$((dir_start + dirs_per_thread))
            for d in $(seq "$dir_start" "$((dir_end - 1))"); do
                local dir="${data_dir}/subdir_${d}"
                mkdir -p "$dir"
                for i in $(seq 1 "$RMR_FILES_PER_DIR"); do
                    local idx=$((d * RMR_FILES_PER_DIR + i))
                    printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${dir}/file_${idx}.txt" 2>/dev/null || \
                        dd if=/dev/zero of="${dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
                done
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done

    local create_end=$(date +%s)
    local create_time=$((create_end - start_time))
    log_info "创建完成: $(format_number $total_files) 个文件, 耗时: $(format_time $create_time)"
    record_result "$result_file" "rmr_create_time" "$create_time" "seconds"

    sync
    sleep 2

    # 测试 1: rm -rf 删除
    log_info "测试 rm -rf 删除..."
    # 重新创建（因为上面已经创建了）
    local rmrf_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local rmrf_end=$(date +%s)
    local rmrf_time=$((rmrf_end - rmrf_start))
    local rmrf_rate=$((total_files * 3600 / rmrf_time))

    log_info "rm -rf 完成: 耗时: $(format_time $rmrf_time), 速率: $(format_number $rmrf_rate) 文件/小时"
    record_result "$result_file" "rmrf_time" "$rmrf_time" "seconds"
    record_result "$result_file" "rmrf_rate" "$rmrf_rate" "files/hour"

    # 重新创建用于 rmr 测试
    log_info "重新创建目录用于 rmr 测试..."
    mkdir -p "$data_dir"
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local dir_start=$((t * dirs_per_thread))
            local dir_end=$((dir_start + dirs_per_thread))
            for d in $(seq "$dir_start" "$((dir_end - 1))"); do
                local dir="${data_dir}/subdir_${d}"
                mkdir -p "$dir"
                for i in $(seq 1 "$RMR_FILES_PER_DIR"); do
                    local idx=$((d * RMR_FILES_PER_DIR + i))
                    printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${dir}/file_${idx}.txt" 2>/dev/null || \
                        dd if=/dev/zero of="${dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
                done
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done
    sync
    sleep 2

    # 测试 2: juicefs rmr 删除
    log_info "测试 juicefs rmr 删除..."
    local rmr_start=$(date +%s)
    juicefs rmr "$data_dir" --threads 50
    sync
    local rmr_end=$(date +%s)
    local rmr_time=$((rmr_end - rmr_start))
    local rmr_rate=$((total_files * 3600 / rmr_time))

    log_info "rmr 完成: 耗时: $(format_time $rmr_time), 速率: $(format_number $rmr_rate) 文件/小时"
    record_result "$result_file" "rmr_time" "$rmr_time" "seconds"
    record_result "$result_file" "rmr_rate" "$rmr_rate" "files/hour"

    # 计算 rmr 相对 rm -rf 的提升
    if [ "$rmrf_time" -gt 0 ]; then
        local rmr_improvement=$((rmrf_time * 100 / rmr_time - 100))
        log_info "rmr 相对 rm -rf 提升: ${rmr_improvement}%"
        record_result "$result_file" "rmr_improvement" "$rmr_improvement" "percent"
    fi

    # 测试不同线程数的 rmr 性能
    log_info "测试不同线程数的 rmr 性能..."
    for threads in 10 50 100 200; do
        # 重新创建
        rm -rf "$data_dir"
        mkdir -p "$data_dir"
        for t in $(seq 0 $((TEST_THREADS - 1))); do
            (
                local dir_start=$((t * dirs_per_thread))
                local dir_end=$((dir_start + dirs_per_thread))
                for d in $(seq "$dir_start" "$((dir_end - 1))"); do
                    local dir="${data_dir}/subdir_${d}"
                    mkdir -p "$dir"
                    for i in $(seq 1 "$RMR_FILES_PER_DIR"); do
                        local idx=$((d * RMR_FILES_PER_DIR + i))
                        printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${dir}/file_${idx}.txt" 2>/dev/null || \
                            dd if=/dev/zero of="${dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
                    done
                done
            ) &
            pids+=($!)
        done
        for pid in "${pids[@]}"; do
            wait "$pid"
        done
        sync
        sleep 1

        local thread_start=$(date +%s)
        juicefs rmr "$data_dir" --threads "$threads"
        sync
        local thread_end=$(date +%s)
        local thread_time=$((thread_end - thread_start))
        local thread_rate=$((total_files * 3600 / thread_time))

        log_info "rmr (threads=$threads): 耗时: $(format_time $thread_time), 速率: $(format_number $thread_rate) 文件/小时"
        record_result "$result_file" "rmr_threads_${threads}_time" "$thread_time" "seconds"
        record_result "$result_file" "rmr_threads_${threads}_rate" "$thread_rate" "files/hour"
    done
}

# ==================== 测试场景 7: 综合删除压力测试 ====================
# 模拟真实场景下的混合删除负载
test_mixed_delete_pressure() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "mixed_delete" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/mixed_delete"

    log_section "测试 7: 综合删除压力测试 [$engine]"

    # 创建混合文件结构
    log_info "创建混合文件结构..."
    mkdir -p "$data_dir"

    # 创建多层目录结构
    local dirs=()
    for d in $(seq 1 100); do
        local dir="${data_dir}/level1_${d}"
        mkdir -p "$dir"
        dirs+=("$dir")
        for sd in $(seq 1 10); do
            mkdir -p "${dir}/level2_${sd}"
        done
    done

    # 在不同目录中创建文件
    local total_files=0
    for dir in "${dirs[@]}"; do
        for sd in $(seq 1 10); do
            local subdir="${dir}/level2_${sd}"
            for f in $(seq 1 100); do
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${subdir}/file_${f}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${subdir}/file_${f}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
                total_files=$((total_files + 1))
            done
        done
    done

    log_info "创建了 $(format_number $total_files) 个文件，分布在 1000 个目录中"
    sync
    sleep 2

    # 执行删除
    log_info "开始删除..."
    local delete_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local delete_end=$(date +%s)
    local delete_time=$((delete_end - delete_start))
    local files_per_hour=$((total_files * 3600 / delete_time))

    log_info "删除完成: 耗时: $(format_time $delete_time)"
    log_info "删除速率: $(format_number $files_per_hour) 文件/小时"
    record_result "$result_file" "mixed_delete_time" "$delete_time" "seconds"
    record_result "$result_file" "mixed_delete_rate" "$files_per_hour" "files/hour"
}

# ==================== 测试场景 8: Trash 清理效率 ====================
# 测试 trash 清理的速度
test_trash_cleanup() {
    local meta_url="$1"
    local engine="$(get_engine_name "$meta_url")"
    local test_dir
    test_dir=$(create_test_dir "trash_cleanup" "$engine")
    local result_file="${test_dir}/results.txt"
    local data_dir="${MOUNT_POINT}/trash_test"

    log_section "测试 8: Trash 清理效率 [$engine]"

    # 创建文件并删除（进入 trash）
    log_info "创建文件并删除到 trash..."
    mkdir -p "$data_dir"
    local trash_files=50000

    local pids=()
    local files_per_thread=$((trash_files / TEST_THREADS))
    for t in $(seq 0 $((TEST_THREADS - 1))); do
        (
            local offset=$((t * files_per_thread))
            for i in $(seq 1 "$files_per_thread"); do
                local idx=$((offset + i))
                printf '%0.s\0' $(seq 1 $SMALL_FILE_SIZE) > "${data_dir}/file_${idx}.txt" 2>/dev/null || \
                    dd if=/dev/zero of="${data_dir}/file_${idx}.txt" bs=$SMALL_FILE_SIZE count=1 2>/dev/null
            done
        ) &
        pids+=($!)
    done
    for pid in "${pids[@]}"; do
        wait "$pid"
    done
    sync

    # 删除到 trash
    local trash_start=$(date +%s)
    rm -rf "$data_dir"
    sync
    local trash_end=$(date +%s)
    local trash_time=$((trash_end - trash_start))
    log_info "删除到 trash: 耗时: $(format_time $trash_time)"
    record_result "$result_file" "trash_delete_time" "$trash_time" "seconds"

    # 等待一段时间，然后手动触发 trash 清理
    log_info "等待 10 秒后触发 trash 清理..."
    sleep 10

    # 通过设置 trash-days 为 0 并触发 gc 来清理
    local cleanup_start=$(date +%s)
    juicefs config "$meta_url" --trash-days 0
    juicefs gc "$meta_url" --delete --threads 10 2>&1 | tee "${test_dir}/trash_cleanup.log"
    local cleanup_end=$(date +%s)
    local cleanup_time=$((cleanup_end - cleanup_start))

    log_info "Trash 清理: 耗时: $(format_time $cleanup_time)"
    record_result "$result_file" "trash_cleanup_time" "$cleanup_time" "seconds"

    # 恢复 trash-days
    juicefs config "$meta_url" --trash-days 1
}

# ==================== 汇总报告 ====================
generate_report() {
    log_section "测试完成，生成报告"

    local report_file="${RESULT_DIR}/report.txt"
    {
        echo "JuiceFS 删除性能测试报告"
        echo "========================"
        echo "测试时间: $(date)"
        echo "测试结果目录: $RESULT_DIR"
        echo ""
        echo "测试配置:"
        echo "  小文件数量: $(format_number $SMALL_FILE_COUNT)"
        echo "  小文件大小: ${SMALL_FILE_SIZE} bytes"
        echo "  大文件数量: $(format_number $LARGE_FILE_COUNT)"
        echo "  大文件大小: $((LARGE_FILE_SIZE / 1024 / 1024)) MB"
        echo "  并发线程数: $TEST_THREADS"
        echo ""
        echo "各引擎测试结果:"
    } > "$report_file"

    for engine_dir in "$RESULT_DIR"/*; do
        if [ -d "$engine_dir" ]; then
            local engine=$(basename "$engine_dir")
            echo "" >> "$report_file"
            echo "--- $engine ---" >> "$report_file"

            for result_file in "$engine_dir"/*/results.txt; do
                if [ -f "$result_file" ]; then
                    local test_name=$(basename "$(dirname "$result_file")")
                    echo "  [$test_name]" >> "$report_file"
                    while IFS='|' read -r metric value unit timestamp; do
                        printf "    %-40s: %s %s\n" "$metric" "$(format_number "$value")" "$unit" >> "$report_file"
                    done < "$result_file"
                fi
            done
        fi
    done

    cat "$report_file"
    log_info "报告已保存到: $report_file"
}

# ==================== 主函数 ====================

show_usage() {
    cat <<EOF
JuiceFS 删除性能测试脚本

用法: $0 [命令] [引擎类型]

命令:
    all             运行所有测试
    small           小文件删除性能测试
    large           大文件删除性能测试
    batchunlink     BatchUnlink 优化对比测试
    batchclone      BatchClone 优化对比测试
    rmr             RMR 命令性能测试
    mixed           综合删除压力测试
    trash           Trash 清理效率测试
    gc              GC 效率测试

引擎类型:
    redis           使用 Redis 作为元数据引擎
    mysql           使用 MySQL 作为元数据引擎
    tikv            使用 TiKV 作为元数据引擎
    sqlite          使用 SQLite 作为元数据引擎（仅本地测试）
    all             测试所有引擎

示例:
    $0 all redis                    # 在 Redis 上运行所有测试
    $0 small mysql                  # 在 MySQL 上运行小文件删除测试
    $0 batchunlink all              # 在所有引擎上运行 batchunlink 对比测试
    $0 rmr redis                    # 测试 RMR 命令性能
    $0 all all                      # 在所有引擎上运行所有测试

EOF
}

# 获取元数据 URL
get_meta_url() {
    local engine="$1"
    case "$engine" in
        redis) echo "$REDIS_META" ;;
        mysql) echo "$MYSQL_META" ;;
        tikv) echo "$TIKV_META" ;;
        sqlite) echo "$SQLITE_META" ;;
        *) log_error "未知引擎: $engine"; exit 1 ;;
    esac
}

# 运行指定测试
run_test() {
    local test_name="$1"
    local meta_url="$2"
    local engine="$(get_engine_name "$meta_url")"

    # 估算所需空间
    local need_mb=$(estimate_space "$test_name")
    if [ "$need_mb" -gt 0 ]; then
        log_info "测试预计需要空间: $(format_number $need_mb) MB"
    fi

    # 清理并重新初始化
    juicefs_umount
    juicefs_destroy "$meta_url" 2>/dev/null || true
    sleep 1
    juicefs_format "$meta_url" "delete-test-${engine}"
    juicefs_mount "$meta_url"

    case "$test_name" in
        small)
            test_small_file_delete "$meta_url"
            ;;
        large)
            test_large_file_delete "$meta_url"
            ;;
        batchunlink)
            test_batchunlink_comparison "$meta_url"
            ;;
        batchclone)
            test_batchclone_comparison "$meta_url"
            ;;
        rmr)
            test_rmr_performance "$meta_url"
            ;;
        mixed)
            test_mixed_delete_pressure "$meta_url"
            ;;
        trash)
            test_trash_cleanup "$meta_url"
            ;;
        gc)
            local test_dir=$(create_test_dir "gc_only" "$engine")
            test_gc_efficiency "$meta_url" "$test_dir"
            ;;
        *)
            log_error "未知测试: $test_name"
            ;;
    esac

    juicefs_umount
}

main() {
    if [ $# -lt 2 ]; then
        show_usage
        exit 1
    fi

    local cmd="$1"
    local engine_arg="$2"

    # 创建结果目录
    mkdir -p "$RESULT_DIR"
    log_info "测试结果将保存到: $RESULT_DIR"

    # 确定要测试的引擎列表
    local engines=()
    if [ "$engine_arg" == "all" ]; then
        engines=("redis" "mysql" "tikv")
    else
        engines=("$engine_arg")
    fi

    # 确定要运行的测试
    local tests=()
    if [ "$cmd" == "all" ]; then
        tests=("small" "large" "batchunlink" "batchclone" "rmr" "mixed" "trash")
    else
        tests=("$cmd")
    fi

    # 运行测试
    for engine in "${engines[@]}"; do
        local meta_url=$(get_meta_url "$engine")
        log_info "========================================"
        log_info "开始测试引擎: $engine"
        log_info "元数据 URL: $meta_url"
        log_info "========================================"

        for test in "${tests[@]}"; do
            run_test "$test" "$meta_url"
        done
    done

    # 生成报告
    generate_report
}

main "$@"
