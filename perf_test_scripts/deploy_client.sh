#!/bin/bash
#
# JuiceFS 客户端部署和测试脚本
# 用途: 在客户端云服务器上部署 JuiceFS 并执行删除性能测试
#
# 需要配合 deploy_meta_server.sh 使用:
#   1. 先在元数据服务器上运行 deploy_meta_server.sh
#   2. 获取元数据服务器的 IP 地址
#   3. 在客户端服务器上运行本脚本
#
# 使用方式:
#   ./deploy_client.sh [操作]
#
# 示例:
#   ./deploy_client.sh install     # 安装 JuiceFS 和依赖
#   ./deploy_client.sh setup       # 配置测试环境（需要先设置 META_IP）
#   ./deploy_client.sh mount       # 挂载 JuiceFS
#   ./deploy_client.sh test        # 运行所有测试
#   ./deploy_client.sh full        # 完整流程：安装 + 配置 + 测试
#

set -euo pipefail

# ==================== 配置区域（请根据实际情况修改） ====================
# 元数据服务器 IP（必须修改为实际的元数据服务器地址）
# 可以通过环境变量传入: export META_IP="192.168.1.100"
META_IP="${META_IP:-192.168.1.100}"

# 元数据引擎连接串（根据测试需要选择）
# Redis
REDIS_META="redis://${META_IP}:6379/1"
# MySQL
MYSQL_META="mysql://juicefs:juicefs123@tcp(${META_IP}:3306)/juicefs"
# TiKV
TIKV_META="tikv://${META_IP}:2379/juicefs"

# 当前使用的元数据引擎（redis/mysql/tikv）
CURRENT_META_ENGINE="redis"

# 对象存储配置
# 选项 1: 使用本地磁盘（简单，适合快速测试）
USE_LOCAL_OBJECT_STORAGE=true
LOCAL_OBJECT_PATH="/data/juicefs-object"

# 选项 2: 使用 MinIO/S3（推荐，模拟真实场景）
# USE_LOCAL_OBJECT_STORAGE=false
# MINIO_ENDPOINT="${META_IP}:9000"
# MINIO_ACCESS_KEY="minioadmin"
# MINIO_SECRET_KEY="minioadmin"
# MINIO_BUCKET="juicefs"
# MINIO_USE_SSL="false"

# JuiceFS 安装配置
JUICEFS_VERSION="latest"  # 或指定版本如 "v1.1.0-beta1"
JUICEFS_INSTALL_DIR="/usr/local/bin"

# 测试配置
MOUNT_POINT="/mnt/juicefs"
TEST_DATA_DIR="/tmp/juicefs-test-data"
RESULT_DIR="/tmp/juicefs-test-results-$(date +%Y%m%d-%H%M%S)"

# 测试脚本路径
TEST_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_SCRIPT="${TEST_SCRIPT_DIR}/delete_perf_test.sh"

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

# 获取元数据 URL
get_meta_url() {
    case "$CURRENT_META_ENGINE" in
        redis) echo "$REDIS_META" ;;
        mysql) echo "$MYSQL_META" ;;
        tikv) echo "$TIKV_META" ;;
        *) log_error "未知引擎: $CURRENT_META_ENGINE"; exit 1 ;;
    esac
}

# 检查系统
check_system() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$NAME
        VER=$VERSION_ID
    else
        log_error "无法识别操作系统"
        exit 1
    fi
    log_info "操作系统: $OS $VER"
}

# 检查 FUSE
check_fuse() {
    log_info "检查 FUSE..."
    if ! modprobe fuse 2>/dev/null; then
        if [ ! -c /dev/fuse ]; then
            log_error "FUSE 未安装或不可用"
            log_error "请运行: apt-get install fuse 或 yum install fuse"
            exit 1
        fi
    fi
    log_info "FUSE 检查通过"
}

# 检查 JuiceFS
check_juicefs() {
    if ! command -v juicefs &>/dev/null; then
        log_error "JuiceFS 未安装"
        return 1
    fi
    log_info "JuiceFS 版本: $(juicefs version 2>/dev/null | head -1)"
    return 0
}

# ==================== 安装阶段 ====================

install_base_deps() {
    log_section "安装基础依赖"

    if [[ "$OS" == *"Ubuntu"* ]] || [[ "$OS" == *"Debian"* ]]; then
        apt-get update
        apt-get install -y wget curl net-tools vim htop iotop sysstat \
            python3 python3-pip fuse libfuse2 libfuse-dev \
            make gcc git automake pkg-config
    elif [[ "$OS" == *"CentOS"* ]] || [[ "$OS" == *"Rocky"* ]] || [[ "$OS" == *"AlmaLinux"* ]]; then
        yum install -y wget curl net-tools vim htop iotop sysstat \
            python3 python3-pip fuse fuse-devel \
            make gcc git automake pkgconfig
        modprobe fuse 2>/dev/null || true
    fi
}

install_juicefs() {
    log_section "安装 JuiceFS"

    if [ "$JUICEFS_VERSION" == "latest" ]; then
        log_info "安装最新版本 JuiceFS..."
        curl -sSL https://d.juicefs.com/install | bash -s -
    else
        log_info "安装 JuiceFS ${JUICEFS_VERSION}..."
        local version_url="https://github.com/juicedata/juicefs/releases/download/${JUICEFS_VERSION}/juicefs-${JUICEFS_VERSION}-linux-amd64.tar.gz"
        wget -q -O /tmp/juicefs.tar.gz "$version_url"
        tar -xzf /tmp/juicefs.tar.gz -C /usr/local/bin juicefs
        chmod +x /usr/local/bin/juicefs
        rm /tmp/juicefs.tar.gz
    fi

    if check_juicefs; then
        log_info "JuiceFS 安装成功"
    else
        log_error "JuiceFS 安装失败"
        exit 1
    fi
}

install_from_source() {
    log_section "从源码编译 JuiceFS"

    if [ ! -d "$TEST_SCRIPT_DIR/.." ]; then
        log_error "JuiceFS 源码目录不存在"
        exit 1
    fi

    local src_dir="$TEST_SCRIPT_DIR/.."
    log_info "编译 JuiceFS 源码: $src_dir"

    cd "$src_dir"
    make juicefs 2>/dev/null || make 2>/dev/null || {
        log_error "编译失败，请检查 Go 环境"
        exit 1
    }

    if [ -f ./juicefs ]; then
        cp ./juicefs "$JUICEFS_INSTALL_DIR/"
        chmod +x "$JUICEFS_INSTALL_DIR/juicefs"
        log_info "JuiceFS 编译并安装成功"
    else
        log_error "编译产物不存在"
        exit 1
    fi
}

install_minio_client() {
    log_section "安装 MinIO Client (可选)"

    if ! command -v mc &>/dev/null; then
        wget -q -O /tmp/minio-client https://dl.min.io/client/mc/release/linux-amd64/mc
        chmod +x /tmp/minio-client
        mv /tmp/minio-client /usr/local/bin/mc
    fi

    log_info "MinIO Client 已安装"
}

optimize_system() {
    log_section "系统优化"

    # 内核参数优化
    cat >> /etc/sysctl.conf <<EOF

# JuiceFS 客户端优化
# FUSE 优化
fs.fuse.max_user_batching = 1
fs.fuse.max_user_congestion_threshold = 20
fs.fuse.max_user_wbthresh = 1048576

# 网络优化
net.core.rmem_max = 26214400
net.core.wmem_max = 26214400
net.ipv4.tcp_rmem = 8192 262144 26214400
net.ipv4.tcp_wmem = 8192 262144 26214400

# 文件描述符
fs.file-max = 2097152
EOF

    sysctl -p

    # 文件描述符限制
    cat >> /etc/security/limits.conf <<EOF
* soft nofile 1048576
* hard nofile 1048576
* soft nproc 1048576
* hard nproc 1048576
root soft nofile 1048576
root hard nofile 1048576
root soft nproc 1048576
root hard nproc 1048576
EOF

    log_info "系统优化完成"
}

do_install() {
    check_system
    install_base_deps
    check_fuse

    read -p "如何安装 JuiceFS?
    1) 从官方下载 (推荐)
    2) 从源码编译 ($TEST_SCRIPT_DIR/../)
    选择 [1]: " install_choice

    case "$install_choice" in
        2) install_from_source ;;
        *) install_juicefs ;;
    esac

    optimize_system

    log_info "安装完成！"
    log_info "下一步: ./deploy_client.sh setup"
}

# ==================== 配置阶段 ====================

setup_local_object_storage() {
    log_section "配置本地对象存储"

    mkdir -p "$LOCAL_OBJECT_PATH"
    chmod 755 "$LOCAL_OBJECT_PATH"

    log_info "本地对象存储目录: $LOCAL_OBJECT_PATH"
}

setup_object_storage() {
    log_section "配置对象存储"

    if [ "$USE_LOCAL_OBJECT_STORAGE" == "true" ]; then
        setup_local_object_storage
    else
        # 配置 MinIO/S3
        mc alias set myminio "http://${MINIO_ENDPOINT}" "$MINIO_ACCESS_KEY" "$MINIO_SECRET_KEY" 2>/dev/null || true

        # 创建 bucket
        mc mb "myminio/${MINIO_BUCKET}" 2>/dev/null || true
        mc anonymous set download "myminio/${MINIO_BUCKET}" 2>/dev/null || true

        log_info "对象存储配置完成"
    fi
}

get_object_storage_config() {
    if [ "$USE_LOCAL_OBJECT_STORAGE" == "true" ]; then
        echo "--storage file --bucket $LOCAL_OBJECT_PATH"
    else
        if [ "$MINIO_USE_SSL" == "true" ]; then
            echo "--storage s3 --bucket https://${MINIO_ENDPOINT}/${MINIO_BUCKET} --access-key ${MINIO_ACCESS_KEY} --secret-key ${MINIO_SECRET_KEY}"
        else
            echo "--storage s3 --bucket http://${MINIO_ENDPOINT}/${MINIO_BUCKET} --access-key ${MINIO_ACCESS_KEY} --secret-key ${MINIO_SECRET_KEY}"
        fi
    fi
}

test_meta_connection() {
    local meta_url="$1"
    log_info "测试元数据引擎连接: $meta_url"

    case "$CURRENT_META_ENGINE" in
        redis)
            if command -v redis-cli &>/dev/null; then
                redis-cli -h "$META_IP" -p 6379 ping 2>/dev/null | grep -q PONG && \
                    log_info "Redis 连接成功" || log_error "Redis 连接失败"
            else
                log_info "未安装 redis-cli，跳过连接测试"
            fi
            ;;
        mysql)
            if command -v mysql &>/dev/null; then
                mysql -h "$META_IP" -P 3306 -u juicefs -pjuicefs123 -e "SELECT 1" 2>/dev/null && \
                    log_info "MySQL 连接成功" || log_error "MySQL 连接失败"
            else
                log_info "未安装 mysql client，跳过连接测试"
            fi
            ;;
        tikv)
            if command -v pd-ctl &>/dev/null || [ -f "$HOME/.tiup/bin/pd-ctl" ]; then
                "$HOME/.tiup/bin/pd-ctl" -u "http://${META_IP}:2379" store 2>/dev/null | grep -q "id" && \
                    log_info "TiKV 连接成功" || log_error "TiKV 连接失败"
            else
                log_info "未安装 pd-ctl，跳过连接测试"
            fi
            ;;
    esac
}

show_setup_info() {
    log_section "配置信息汇总"

    local meta_url=$(get_meta_url)
    local obj_config=$(get_object_storage_config)

    echo ""
    echo "========================================"
    echo "  客户端配置信息"
    echo "========================================"
    echo "元数据引擎: $CURRENT_META_ENGINE"
    echo "元数据地址: $meta_url"
    echo "元数据服务器: $META_IP"
    echo ""
    echo "对象存储: $obj_config"
    echo ""
    echo "挂载点: $MOUNT_POINT"
    echo "测试数据目录: $TEST_DATA_DIR"
    echo "测试结果目录: $RESULT_DIR"
    echo ""
    echo "========================================"
}

do_setup() {
    check_system
    check_juicefs || install_juicefs

    # 读取配置
    read -p "元数据服务器 IP [$META_IP]: " input_ip
    META_IP="${input_ip:-$META_IP}"

    read -p "使用哪种对象存储? (1)本地磁盘 (2)MinIO/S3 [1]: " obj_choice
    if [ "$obj_choice" == "2" ]; then
        USE_LOCAL_OBJECT_STORAGE=false
        read -p "MinIO Endpoint [$META_IP:9000]: " input_endpoint
        MINIO_ENDPOINT="${input_endpoint:-$META_IP:9000}"
        read -p "MinIO Access Key [minioadmin]: " input_access
        MINIO_ACCESS_KEY="${input_access:-minioadmin}"
        read -p "MinIO Secret Key [minioadmin]: " input_secret
        MINIO_SECRET_KEY="${input_secret:-minioadmin}"
        read -p "MinIO Bucket [juicefs]: " input_bucket
        MINIO_BUCKET="${input_bucket:-juicefs}"
        read -p "使用 SSL? (y/N): " input_ssl
        [ "$input_ssl" == "y" ] && MINIO_USE_SSL="true" || MINIO_USE_SSL="false"
    fi

    read -p "选择元数据引擎: (1)Redis (2)MySQL (3)TiKV [1]: " meta_choice
    case "$meta_choice" in
        2) CURRENT_META_ENGINE="mysql" ;;
        3) CURRENT_META_ENGINE="tikv" ;;
        *) CURRENT_META_ENGINE="redis" ;;
    esac

    # 重新获取更新后的值
    local meta_url=$(get_meta_url)

    # 更新测试脚本中的配置
    if [ -f "$TEST_SCRIPT" ]; then
        log_info "更新测试脚本配置..."
        sed -i "s|REDIS_META=.*|REDIS_META=\"${REDIS_META}\"|" "$TEST_SCRIPT"
        sed -i "s|MYSQL_META=.*|MYSQL_META=\"${MYSQL_META}\"|" "$TEST_SCRIPT"
        sed -i "s|TIKV_META=.*|TIKV_META=\"${TIKV_META}\"|" "$TEST_SCRIPT"
        sed -i "s|OBJECT_STORAGE=.*|OBJECT_STORAGE=\"$(get_object_storage_config)\"|" "$TEST_SCRIPT"
    fi

    setup_object_storage
    test_meta_connection "$meta_url"
    show_setup_info

    log_info "配置完成！"
    log_info "下一步: ./deploy_client.sh mount"
}

# ==================== 挂载阶段 ====================

prepare_mount() {
    log_section "准备挂载环境"

    mkdir -p "$MOUNT_POINT"
    mkdir -p "$TEST_DATA_DIR"
    mkdir -p "$RESULT_DIR"

    # 确保 fusermount 可用
    if ! command -v fusermount &>/dev/null; then
        log_error "fusermount 不可用"
        exit 1
    fi
}

do_mount() {
    check_juicefs
    prepare_mount

    local meta_url=$(get_meta_url)
    local obj_config=$(get_object_storage_config)

    # 卸载已挂载的
    if mountpoint -q "$MOUNT_POINT"; then
        log_info "卸载旧的 JuiceFS..."
        fusermount -u "$MOUNT_POINT" 2>/dev/null || umount "$MOUNT_POINT" 2>/dev/null || true
        sleep 2
    fi

    log_section "挂载 JuiceFS"
    log_info "元数据: $meta_url"
    log_info "对象存储: $obj_config"

    # 检查是否已格式化
    if ! juicefs status "$meta_url" 2>/dev/null | grep -q "Name"; then
        log_info "文件系统未格式化，进行格式化..."
        juicefs format "$meta_url" "juicefs-delete-test" $obj_config --trash-days 1
    fi

    # 执行挂载
    log_info "挂载到 $MOUNT_POINT..."
    juicefs mount "$meta_url" "$MOUNT_POINT" \
        --background \
        --no-usage-report \
        --cache-size 102400 \
        --writeback \
        --io-retries 10

    sleep 3

    # 验证挂载
    if mountpoint -q "$MOUNT_POINT"; then
        log_info "挂载成功！"
        df -h "$MOUNT_POINT"
    else
        log_error "挂载失败，请检查日志"
        exit 1
    fi
}

do_umount() {
    log_section "卸载 JuiceFS"

    if mountpoint -q "$MOUNT_POINT"; then
        fusermount -u "$MOUNT_POINT" 2>/dev/null || umount "$MOUNT_POINT" 2>/dev/null || true
        log_info "卸载完成"
    else
        log_info "未挂载，无需卸载"
    fi
}

# ==================== 测试阶段 ====================

run_test() {
    local test_name="$1"
    log_section "运行测试: $test_name"

    local meta_url=$(get_meta_url)

    if [ ! -f "$TEST_SCRIPT" ]; then
        log_error "测试脚本不存在: $TEST_SCRIPT"
        exit 1
    fi

    chmod +x "$TEST_SCRIPT"

    # 设置环境变量
    export REDIS_META="$REDIS_META"
    export MYSQL_META="$MYSQL_META"
    export TIKV_META="$TIKV_META"

    case "$test_name" in
        small)
            "$TEST_SCRIPT" small "$CURRENT_META_ENGINE"
            ;;
        large)
            "$TEST_SCRIPT" large "$CURRENT_META_ENGINE"
            ;;
        batchunlink)
            "$TEST_SCRIPT" batchunlink "$CURRENT_META_ENGINE"
            ;;
        batchclone)
            "$TEST_SCRIPT" batchclone "$CURRENT_META_ENGINE"
            ;;
        rmr)
            "$TEST_SCRIPT" rmr "$CURRENT_META_ENGINE"
            ;;
        mixed)
            "$TEST_SCRIPT" mixed "$CURRENT_META_ENGINE"
            ;;
        trash)
            "$TEST_SCRIPT" trash "$CURRENT_META_ENGINE"
            ;;
        gc)
            "$TEST_SCRIPT" gc "$CURRENT_META_ENGINE"
            ;;
        all)
            "$TEST_SCRIPT" all "$CURRENT_META_ENGINE"
            ;;
        *)
            log_error "未知测试: $test_name"
            exit 1
            ;;
    esac

    log_info "测试完成"
}

do_test() {
    if ! mountpoint -q "$MOUNT_POINT"; then
        log_error "JuiceFS 未挂载，请先运行: $0 mount"
        exit 1
    fi

    log_section "选择测试类型"
    echo "1) 小文件删除性能测试 (small)"
    echo "2) 大文件删除性能测试 (large)"
    echo "3) BatchUnlink 优化对比 (batchunlink)"
    echo "4) BatchClone 优化对比 (batchclone)"
    echo "5) RMR 命令性能测试 (rmr)"
    echo "6) 综合删除压力测试 (mixed)"
    echo "7) Trash 清理效率测试 (trash)"
    echo "8) GC 效率测试 (gc)"
    echo "9) 运行所有测试 (all)"
    echo ""
    read -p "选择测试类型 [9]: " test_choice

    case "$test_choice" in
        1) run_test small ;;
        2) run_test large ;;
        3) run_test batchunlink ;;
        4) run_test batchclone ;;
        5) run_test rmr ;;
        6) run_test mixed ;;
        7) run_test trash ;;
        8) run_test gc ;;
        *) run_test all ;;
    esac
}

do_full() {
    log_section "执行完整流程"

    do_install
    do_setup
    do_mount
    do_test

    log_section "测试完成！"
    log_info "结果保存在: $RESULT_DIR"
}

# ==================== 监控工具 ====================

show_status() {
    log_section "系统状态"

    echo ""
    echo "--- JuiceFS 挂载状态 ---"
    if mountpoint -q "$MOUNT_POINT"; then
        echo "JuiceFS: 已挂载 ($MOUNT_POINT)"
        df -h "$MOUNT_POINT"
    else
        echo "JuiceFS: 未挂载"
    fi

    echo ""
    echo "--- JuiceFS 版本 ---"
    juicefs version 2>/dev/null || echo "JuiceFS 未安装"

    echo ""
    echo "--- 元数据引擎连接 ---"
    local meta_url=$(get_meta_url)
    echo "引擎: $CURRENT_META_ENGINE"
    echo "地址: $meta_url"

    test_meta_connection "$meta_url"
}

# ==================== 清理工具 ====================

do_clean() {
    log_section "清理环境"

    read -p "这将卸载 JuiceFS 并清理测试数据，确定继续? [y/N]: " confirm
    if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
        log_info "已取消"
        return
    fi

    do_umount

    log_info "清理测试数据..."
    rm -rf "$TEST_DATA_DIR"
    rm -rf "$RESULT_DIR"
    rm -rf "$LOCAL_OBJECT_PATH"/*

    log_info "清理完成"
}

# ==================== 主函数 ====================

show_usage() {
    cat <<EOF
JuiceFS 客户端部署和测试脚本

用法: $0 [操作]

操作:
    install        安装 JuiceFS 和依赖
    setup          配置测试环境（对象存储、元数据连接）
    mount          挂载 JuiceFS 文件系统
    umount         卸载 JuiceFS 文件系统
    test           运行测试（需先挂载）
    full           完整流程：安装 + 配置 + 测试
    status         查看状态
    clean          清理环境（危险！）

环境变量:
    META_IP        元数据服务器 IP 地址

示例:
    # 完整流程（首次使用）
    export META_IP="192.168.1.100"
    $0 full

    # 分步执行
    $0 install      # 安装
    $0 setup       # 配置（修改 META_IP）
    $0 mount       # 挂载
    $0 test        # 测试

    # 只运行特定测试
    $0 test        # 选择测试类型
    # 或直接指定:
    ./delete_perf_test.sh rmr redis

    # 查看状态
    $0 status

EOF
}

main() {
    if [ $# -eq 0 ]; then
        show_usage
        exit 0
    fi

    local action="$1"
    shift

    check_system

    case "$action" in
        install)
            do_install
            ;;
        setup)
            do_setup
            ;;
        mount)
            do_mount
            ;;
        umount|unmount)
            do_umount
            ;;
        test)
            do_test
            ;;
        full)
            do_full
            ;;
        status)
            show_status
            ;;
        clean)
            do_clean
            ;;
        *)
            log_error "未知操作: $action"
            show_usage
            exit 1
            ;;
    esac
}

main "$@"
