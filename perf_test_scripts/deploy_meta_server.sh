#!/bin/bash
#
# JuiceFS 元数据服务器部署脚本
# 用途: 在独立的云服务器上部署元数据引擎（Redis/MySQL/TiKV）
#
# 支持一键部署三种元数据引擎:
#   - Redis (单机, 适合快速测试)
#   - MySQL (单实例, 适合生产模拟)
#   - TiKV (最小集群 3 节点, 适合分布式测试)
#
# 使用方式:
#   ./deploy_meta_server.sh [引擎类型] [操作]
#
# 示例:
#   ./deploy_meta_server.sh redis install    # 安装并启动 Redis
#   ./deploy_meta_server.sh mysql install    # 安装并启动 MySQL
#   ./deploy_meta_server.sh tikv install     # 安装并启动 TiKV (单节点测试模式)
#   ./deploy_meta_server.sh redis status     # 查看 Redis 状态
#   ./deploy_meta_server.sh all stop         # 停止所有引擎
#   ./deploy_meta_server.sh all clean        # 清理所有数据
#

set -euo pipefail

# ==================== 配置区域 ====================
# 元数据服务器监听地址（修改为你的内网 IP）
# 默认监听所有接口，允许远程客户端连接
BIND_ADDR="0.0.0.0"

# 数据存储目录（建议使用高性能 SSD）
DATA_DIR="/data/juicefs-meta"

# 日志目录
LOG_DIR="/var/log/juicefs-meta"

# Redis 配置
REDIS_PORT=6379
REDIS_PASSWORD=""           # 留空表示无密码，建议生产环境设置
REDIS_MAXMEMORY="8gb"       # Redis 最大内存

# MySQL 配置
MYSQL_PORT=3306
MYSQL_ROOT_PASSWORD="juicefs123"
MYSQL_DATABASE="juicefs"
MYSQL_USER="juicefs"
MYSQL_PASSWORD="juicefs123"

# TiKV 配置
TIKV_PORT=20160
PD_PORT=2379
TIKV_STATUS_PORT=20180

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

# 检查是否为 root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "请使用 root 权限运行此脚本"
        exit 1
    fi
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

# 安装基础依赖
install_base_deps() {
    log_info "安装基础依赖..."
    if [[ "$OS" == *"Ubuntu"* ]] || [[ "$OS" == *"Debian"* ]]; then
        apt-get update
        apt-get install -y wget curl net-tools vim htop iotop sysstat \
            python3 python3-pip software-properties-common apt-transport-https \
            ca-certificates gnupg lsb-release
    elif [[ "$OS" == *"CentOS"* ]] || [[ "$OS" == *"Rocky"* ]] || [[ "$OS" == *"AlmaLinux"* ]]; then
        yum install -y wget curl net-tools vim htop iotop sysstat \
            python3 python3-pip yum-utils
    else
        log_error "不支持的操作系统: $OS"
        exit 1
    fi
}

# 创建目录
create_dirs() {
    mkdir -p "$DATA_DIR"/{redis,mysql,tikv,pd}
    mkdir -p "$LOG_DIR"/{redis,mysql,tikv,pd}
    chmod 755 "$DATA_DIR" "$LOG_DIR"
}

# ==================== Redis 部署 ====================

install_redis() {
    log_section "部署 Redis"

    if [[ "$OS" == *"Ubuntu"* ]] || [[ "$OS" == *"Debian"* ]]; then
        apt-get update
        apt-get install -y redis-server
    elif [[ "$OS" == *"CentOS"* ]] || [[ "$OS" == *"Rocky"* ]] || [[ "$OS" == *"AlmaLinux"* ]]; then
        yum install -y epel-release
        yum install -y redis
    fi

    # 备份原配置
    cp /etc/redis/redis.conf /etc/redis/redis.conf.bak 2>/dev/null || \
        cp /etc/redis.conf /etc/redis.conf.bak 2>/dev/null || true

    # 生成配置文件
    cat > /etc/redis/redis.conf <<EOF
# Redis 配置 for JuiceFS 测试
bind $BIND_ADDR
port $REDIS_PORT
protected-mode no

daemonize yes
pidfile /var/run/redis/redis-server.pid
logfile $LOG_DIR/redis/redis.log
dir $DATA_DIR/redis

# 内存配置
maxmemory $REDIS_MAXMEMORY
maxmemory-policy allkeys-lru

# 持久化配置（测试环境可适当放宽）
save 900 1
save 300 10
save 60 10000
dbfilename juicefs.rdb
appendonly yes
appendfilename "juicefs.aof"
appendfsync everysec

# 性能优化
tcp-keepalive 300
timeout 0
tcp-backlog 511

# 客户端输出缓冲区限制（大删除操作需要）
client-output-buffer-limit normal 0 0 0
client-output-buffer-limit replica 256mb 64mb 60

# 慢查询日志
slowlog-log-slower-than 10000
slowlog-max-len 128
EOF

    mkdir -p /var/run/redis
    chown redis:redis /var/run/redis 2>/dev/null || true

    # 启动 Redis
    systemctl enable redis-server 2>/dev/null || systemctl enable redis 2>/dev/null || true
    systemctl restart redis-server 2>/dev/null || systemctl restart redis 2>/dev/null || true

    sleep 2

    # 验证
    if redis-cli -p $REDIS_PORT ping | grep -q PONG; then
        log_info "Redis 启动成功，端口: $REDIS_PORT"
        log_info "连接串: redis://$BIND_ADDR:$REDIS_PORT/1"
    else
        log_error "Redis 启动失败"
        exit 1
    fi
}

stop_redis() {
    log_info "停止 Redis..."
    systemctl stop redis-server 2>/dev/null || systemctl stop redis 2>/dev/null || true
    pkill -f redis-server 2>/dev/null || true
}

status_redis() {
    log_info "Redis 状态:"
    systemctl status redis-server --no-pager 2>/dev/null || systemctl status redis --no-pager 2>/dev/null || true
    redis-cli -p $REDIS_PORT info server 2>/dev/null | head -5 || log_error "Redis 未运行"
}

clean_redis() {
    log_info "清理 Redis 数据..."
    stop_redis
    rm -rf "$DATA_DIR/redis"/*
    rm -rf "$LOG_DIR/redis"/*
    log_info "Redis 数据已清理"
}

# ==================== MySQL 部署 ====================

install_mysql() {
    log_section "部署 MySQL"

    if [[ "$OS" == *"Ubuntu"* ]] || [[ "$OS" == *"Debian"* ]]; then
        # 使用官方仓库安装 MySQL 8.0
        wget -q https://dev.mysql.com/get/mysql-apt-config_0.8.29-1_all.deb -O /tmp/mysql-apt-config.deb
        DEBIAN_FRONTEND=noninteractive dpkg -i /tmp/mysql-apt-config.deb || true
        apt-get update
        apt-get install -y mysql-server
    elif [[ "$OS" == *"CentOS"* ]] || [[ "$OS" == *"Rocky"* ]] || [[ "$OS" == *"AlmaLinux"* ]]; then
        rpm -Uvh https://dev.mysql.com/get/mysql80-community-release-el8-11.noarch.rpm 2>/dev/null || true
        yum install -y mysql-server
    fi

    # 生成配置文件
    cat > /etc/mysql/mysql.conf.d/juicefs.cnf 2>/dev/null || \
        cat > /etc/my.cnf.d/juicefs.cnf <<EOF
[mysqld]
# 基础配置
bind-address = $BIND_ADDR
port = $MYSQL_PORT

datadir = $DATA_DIR/mysql
socket = /var/run/mysqld/mysqld.sock
pid-file = /var/run/mysqld/mysqld.pid
log-error = $LOG_DIR/mysql/error.log

# JuiceFS 优化配置
innodb_buffer_pool_size = 8G
innodb_log_file_size = 1G
innodb_flush_log_at_trx_commit = 2
innodb_flush_method = O_DIRECT
innodb_file_per_table = 1

# 连接配置
max_connections = 500
max_allowed_packet = 64M
wait_timeout = 3600
interactive_timeout = 3600

# 性能优化
query_cache_type = 0
query_cache_size = 0
tmp_table_size = 64M
max_heap_table_size = 64M

# 字符集
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci

# 二进制日志（可选，用于复制测试）
# log-bin = mysql-bin
# binlog-format = ROW
EOF

    mkdir -p /var/run/mysqld
    chown mysql:mysql /var/run/mysqld 2>/dev/null || true

    # 初始化 MySQL
    if [ ! -d "$DATA_DIR/mysql/mysql" ]; then
        mysqld --initialize-insecure --datadir="$DATA_DIR/mysql" --user=mysql
    fi

    # 启动 MySQL
    systemctl enable mysql 2>/dev/null || systemctl enable mysqld 2>/dev/null || true
    systemctl restart mysql 2>/dev/null || systemctl restart mysqld 2>/dev/null || true

    sleep 3

    # 设置 root 密码并创建数据库
    mysql -u root <<EOF
ALTER USER 'root'@'localhost' IDENTIFIED WITH mysql_native_password BY '$MYSQL_ROOT_PASSWORD';
CREATE DATABASE IF NOT EXISTS $MYSQL_DATABASE CHARACTER SET utf8mb4;
CREATE USER IF NOT EXISTS '$MYSQL_USER'@'%' IDENTIFIED WITH mysql_native_password BY '$MYSQL_PASSWORD';
GRANT ALL PRIVILEGES ON $MYSQL_DATABASE.* TO '$MYSQL_USER'@'%';
FLUSH PRIVILEGES;
EOF

    # 验证
    if mysql -u root -p"$MYSQL_ROOT_PASSWORD" -e "SELECT 1" >/dev/null 2>&1; then
        log_info "MySQL 启动成功，端口: $MYSQL_PORT"
        log_info "Root 密码: $MYSQL_ROOT_PASSWORD"
        log_info "连接串: mysql://$MYSQL_USER:$MYSQL_PASSWORD@tcp($BIND_ADDR:$MYSQL_PORT)/$MYSQL_DATABASE"
    else
        log_error "MySQL 启动失败"
        exit 1
    fi
}

stop_mysql() {
    log_info "停止 MySQL..."
    systemctl stop mysql 2>/dev/null || systemctl stop mysqld 2>/dev/null || true
    pkill -f mysqld 2>/dev/null || true
}

status_mysql() {
    log_info "MySQL 状态:"
    systemctl status mysql --no-pager 2>/dev/null || systemctl status mysqld --no-pager 2>/dev/null || true
    mysql -u root -p"$MYSQL_ROOT_PASSWORD" -e "SHOW STATUS LIKE 'Uptime';" 2>/dev/null || log_error "MySQL 未运行"
}

clean_mysql() {
    log_info "清理 MySQL 数据..."
    stop_mysql
    rm -rf "$DATA_DIR/mysql"/*
    rm -rf "$LOG_DIR/mysql"/*
    log_info "MySQL 数据已清理"
}

# ==================== TiKV 部署（单节点测试模式） ====================

install_tikv() {
    log_section "部署 TiKV (单节点测试模式)"

    # 下载 TiUP
    if [ ! -f "$HOME/.tiup/bin/tiup" ]; then
        log_info "安装 TiUP..."
        curl --proto '=https' --tlsv1.2 -sSf https://tiup-mirrors.pingcap.com/install.sh | sh
        source "$HOME/.profile" 2>/dev/null || source "$HOME/.bashrc" 2>/dev/null || true
    fi

    export PATH="$HOME/.tiup/bin:$PATH"

    # 安装组件
    tiup install pd tikv 2>/dev/null || true

    # 创建启动脚本
    cat > /usr/local/bin/start-tikv.sh <<'EOF'
#!/bin/bash
export PATH="$HOME/.tiup/bin:$PATH"
DATA_DIR="/data/juicefs-meta"
LOG_DIR="/var/log/juicefs-meta"

# 启动 PD
nohup tiup pd --name=pd1 --data-dir="$DATA_DIR/pd" \
    --client-urls="http://0.0.0.0:2379" \
    --peer-urls="http://0.0.0.0:2380" \
    --log-file="$LOG_DIR/pd/pd.log" > /dev/null 2>&1 &

sleep 3

# 启动 TiKV
nohup tiup tikv --addr="0.0.0.0:20160" \
    --status-addr="0.0.0.0:20180" \
    --pd-endpoints="127.0.0.1:2379" \
    --data-dir="$DATA_DIR/tikv" \
    --log-file="$LOG_DIR/tikv/tikv.log" > /dev/null 2>&1 &
EOF
    chmod +x /usr/local/bin/start-tikv.sh

    # 创建停止脚本
    cat > /usr/local/bin/stop-tikv.sh <<'EOF'
#!/bin/bash
pkill -f "tiup pd" 2>/dev/null || true
pkill -f "tiup tikv" 2>/dev/null || true
sleep 2
EOF
    chmod +x /usr/local/bin/stop-tikv.sh

    # 启动
    /usr/local/bin/start-tikv.sh

    sleep 5

    # 验证
    if curl -s http://localhost:2379/pd/api/v1/status | grep -q "name"; then
        log_info "TiKV 启动成功"
        log_info "PD 端口: 2379, TiKV 端口: 20160"
        log_info "连接串: tikv://$BIND_ADDR:2379/juicefs"
    else
        log_error "TiKV 启动失败"
        exit 1
    fi
}

stop_tikv() {
    log_info "停止 TiKV..."
    /usr/local/bin/stop-tikv.sh 2>/dev/null || true
}

status_tikv() {
    log_info "TiKV 状态:"
    curl -s http://localhost:2379/pd/api/v1/status 2>/dev/null | python3 -m json.tool 2>/dev/null || \
        log_error "TiKV/PB 未运行"
    ps aux | grep -E "tiup (pd|tikv)" | grep -v grep || log_error "TiKV 进程未找到"
}

clean_tikv() {
    log_info "清理 TiKV 数据..."
    stop_tikv
    rm -rf "$DATA_DIR/tikv"/*
    rm -rf "$DATA_DIR/pd"/*
    rm -rf "$LOG_DIR/tikv"/*
    rm -rf "$LOG_DIR/pd"/*
    log_info "TiKV 数据已清理"
}

# ==================== 系统优化 ====================

optimize_system() {
    log_section "系统优化"

    # 内核参数优化
    cat >> /etc/sysctl.conf <<EOF

# JuiceFS 元数据服务器优化
# 网络优化
net.core.somaxconn = 65535
net.core.netdev_max_backlog = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.ipv4.tcp_fin_timeout = 10
net.ipv4.tcp_keepalive_time = 1200
net.ipv4.tcp_max_tw_buckets = 5000

# 内存优化
vm.swappiness = 10
vm.dirty_ratio = 40
vm.dirty_background_ratio = 10

# 文件描述符
fs.file-max = 2097152
fs.nr_open = 2097152
EOF

    sysctl -p

    # 文件描述符限制
    cat >> /etc/security/limits.conf <<EOF
* soft nofile 1048576
* hard nofile 1048576
* soft nproc 1048576
* hard nproc 1048576
EOF

    # 禁用透明大页（对数据库性能有影响）
    if [ -f /sys/kernel/mm/transparent_hugepage/enabled ]; then
        echo never > /sys/kernel/mm/transparent_hugepage/enabled
        echo never > /sys/kernel/mm/transparent_hugepage/defrag
    fi

    log_info "系统优化完成"
}

# ==================== 防火墙配置 ====================

configure_firewall() {
    log_section "配置防火墙"

    if command -v ufw >/dev/null 2>&1; then
        # Ubuntu/Debian
        ufw allow $REDIS_PORT/tcp comment "Redis"
        ufw allow $MYSQL_PORT/tcp comment "MySQL"
        ufw allow $TIKV_PORT/tcp comment "TiKV"
        ufw allow $PD_PORT/tcp comment "TiKV PD"
        ufw allow $TIKV_STATUS_PORT/tcp comment "TiKV Status"
        ufw allow 2380/tcp comment "TiKV PD Peer"
        ufw reload
    elif command -v firewall-cmd >/dev/null 2>&1; then
        # CentOS/Rocky
        firewall-cmd --permanent --add-port=$REDIS_PORT/tcp
        firewall-cmd --permanent --add-port=$MYSQL_PORT/tcp
        firewall-cmd --permanent --add-port=$TIKV_PORT/tcp
        firewall-cmd --permanent --add-port=$PD_PORT/tcp
        firewall-cmd --permanent --add-port=$TIKV_STATUS_PORT/tcp
        firewall-cmd --permanent --add-port=2380/tcp
        firewall-cmd --reload
    else
        log_info "未检测到防火墙，跳过配置"
    fi

    log_info "防火墙配置完成"
}

# ==================== 监控信息输出 ====================

show_connection_info() {
    log_section "连接信息汇总"

    local ip=$(ip route get 1 2>/dev/null | awk '{print $7; exit}' || hostname -I | awk '{print $1}')

    echo ""
    echo "========================================"
    echo "  元数据服务器连接信息"
    echo "========================================"
    echo "服务器 IP: $ip"
    echo ""
    echo "Redis 连接串:"
    echo "  redis://$ip:$REDIS_PORT/1"
    if [ -n "$REDIS_PASSWORD" ]; then
        echo "  redis://:$REDIS_PASSWORD@$ip:$REDIS_PORT/1"
    fi
    echo ""
    echo "MySQL 连接串:"
    echo "  mysql://$MYSQL_USER:$MYSQL_PASSWORD@tcp($ip:$MYSQL_PORT)/$MYSQL_DATABASE"
    echo ""
    echo "TiKV 连接串:"
    echo "  tikv://$ip:$PD_PORT/juicefs"
    echo ""
    echo "========================================"
    echo "  请在客户端服务器上修改以下配置:"
    echo "========================================"
    echo "export REDIS_META=\"redis://$ip:$REDIS_PORT/1\""
    echo "export MYSQL_META=\"mysql://$MYSQL_USER:$MYSQL_PASSWORD@tcp($ip:$MYSQL_PORT)/$MYSQL_DATABASE\""
    echo "export TIKV_META=\"tikv://$ip:$PD_PORT/juicefs\""
    echo ""
}

# ==================== 主函数 ====================

show_usage() {
    cat <<EOF
JuiceFS 元数据服务器部署脚本

用法: $0 [引擎类型] [操作]

引擎类型:
    redis           Redis 元数据引擎
    mysql           MySQL 元数据引擎
    tikv            TiKV 元数据引擎（单节点测试模式）
    all             所有引擎

操作:
    install         安装并启动
    stop            停止服务
    start           启动服务
    restart         重启服务
    status          查看状态
    clean           清理所有数据（危险！）
    optimize        仅执行系统优化

示例:
    $0 redis install        # 安装并启动 Redis
    $0 mysql install        # 安装并启动 MySQL
    $0 tikv install         # 安装并启动 TiKV
    $0 all install          # 安装所有引擎
    $0 redis status         # 查看 Redis 状态
    $0 all stop             # 停止所有引擎
    $0 all clean            # 清理所有数据

EOF
}

main() {
    if [ $# -lt 2 ]; then
        show_usage
        exit 1
    fi

    local engine="$1"
    local action="$2"

    check_root
    check_system
    create_dirs

    case "$action" in
        install)
            install_base_deps
            optimize_system
            configure_firewall
            case "$engine" in
                redis) install_redis ;;
                mysql) install_mysql ;;
                tikv) install_tikv ;;
                all)
                    install_redis
                    install_mysql
                    install_tikv
                    ;;
                *) log_error "未知引擎: $engine"; exit 1 ;;
            esac
            show_connection_info
            ;;
        start)
            case "$engine" in
                redis) systemctl start redis-server 2>/dev/null || systemctl start redis 2>/dev/null ;;
                mysql) systemctl start mysql 2>/dev/null || systemctl start mysqld 2>/dev/null ;;
                tikv) /usr/local/bin/start-tikv.sh ;;
                all)
                    systemctl start redis-server 2>/dev/null || systemctl start redis 2>/dev/null
                    systemctl start mysql 2>/dev/null || systemctl start mysqld 2>/dev/null
                    /usr/local/bin/start-tikv.sh 2>/dev/null || true
                    ;;
            esac
            ;;
        stop)
            case "$engine" in
                redis) stop_redis ;;
                mysql) stop_mysql ;;
                tikv) stop_tikv ;;
                all)
                    stop_redis
                    stop_mysql
                    stop_tikv
                    ;;
            esac
            ;;
        restart)
            $0 "$engine" stop
            sleep 2
            $0 "$engine" start
            ;;
        status)
            case "$engine" in
                redis) status_redis ;;
                mysql) status_mysql ;;
                tikv) status_tikv ;;
                all)
                    status_redis
                    echo ""
                    status_mysql
                    echo ""
                    status_tikv
                    ;;
            esac
            ;;
        clean)
            read -p "确定要清理所有数据吗？此操作不可恢复 [y/N]: " confirm
            if [ "$confirm" == "y" ] || [ "$confirm" == "Y" ]; then
                case "$engine" in
                    redis) clean_redis ;;
                    mysql) clean_mysql ;;
                    tikv) clean_tikv ;;
                    all)
                        clean_redis
                        clean_mysql
                        clean_tikv
                        ;;
                esac
            else
                log_info "已取消清理"
            fi
            ;;
        optimize)
            optimize_system
            ;;
        *)
            log_error "未知操作: $action"
            show_usage
            exit 1
            ;;
    esac
}

main "$@"
