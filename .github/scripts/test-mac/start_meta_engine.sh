#!/bin/bash -e

# Helper function to install packages via Homebrew
brew_install() {
    if ! brew list "$1" &>/dev/null; then
        echo "Installing $1..."
        brew install "$1"
    fi
}

start_redis() {
    if pgrep redis-server >/dev/null; then
        echo "Redis is already running"
        return 0
    fi

    if brew services start redis 2>/dev/null; then
        echo "Redis started via brew services"
    elif [ -f /usr/local/bin/redis-server ]; then
        echo "Starting Redis directly..."
        /usr/local/bin/redis-server /usr/local/etc/redis.conf &
    else
        echo "Failed to start Redis"
        return 1
    fi

    sleep 2
    if ! pgrep redis-server >/dev/null; then
        echo "Redis failed to start"
        return 1
    fi
}

clean_minio() {
    if command -v mc >/dev/null; then
        mc ls local/ 2>/dev/null | awk '{print $5}' | while read -r bucket; do
            if [ -n "$bucket" ]; then
                echo "Cleaning bucket: $bucket"
                mc rb --force local/"$bucket" 2>/dev/null || true
            fi
        done
    fi
}

start_minio() {
    if ! command -v minio >/dev/null; then
        brew_install minio/stable/minio
    fi
    
    if ! command -v mc >/dev/null; then
        brew_install minio/stable/mc
    fi

    clean_minio
    
    if ! pgrep minio >/dev/null; then
        mkdir -p /tmp/data
        rm -rf /tmp/data/*
        minio server /tmp/data --console-address :9001 &
        sleep 3
    fi

    mc alias set local http://127.0.0.1:9000 minioadmin minioadmin || true
    
    mc mb local/jfs || true
    mc mb local/test || true
}

start_meta_engine() {
    local meta=$1
    local storage=$2

    case "$meta" in
        redis)
            brew_install redis
            if ! start_redis; then
                echo >&2 "Failed to start Redis"
                return 1
            fi
            ;;
        sqlite3)
            brew_install sqlite3
            echo "SQLite3 ready to use"
            ;;
        *)
            echo >&2 "<FATAL>: Unsupported meta engine: $meta"
            return 1
            ;;
    esac

    if [ "$storage" = "minio" ]; then
        if ! start_minio; then
            echo >&2 "Failed to start MinIO"
            return 1
        fi
    fi
}

get_meta_url() {
    case "$1" in
        redis) echo "redis://127.0.0.1:6379/1" ;;
        sqlite3) echo "sqlite3://test.db" ;;
        *)     echo >&2 "<FATAL>: Unsupported meta: $1"; return 1 ;;
    esac
}

get_meta_url2() {
    case "$1" in
        redis) echo "redis://127.0.0.1:6379/2" ;;
        sqlite3) echo "sqlite3://test2.db" ;;
        *)     echo >&2 "<FATAL>: Unsupported meta: $1"; return 1 ;;
    esac
}

retry() {
    local retries=5
    local delay=3
    local exit=0

    for i in $(seq 1 "$retries"); do
        if "$@"; then
            return 0
        else
            exit=$?
            if [ "$i" -eq "$retries" ]; then
                return "$exit"
            fi
            sleep "$delay"
        fi
    done
}