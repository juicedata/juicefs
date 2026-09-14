#!/bin/bash -e

MC_VERSION="${MC_VERSION:-RELEASE.2021-04-22T17-40-00Z}"
CACHE_DIR="${MC_CACHE_DIR:-$HOME/.cache/juicefs-ci}"
dest="${1:-./mc}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m | tr '[:upper:]' '[:lower:]')
case "$os" in
    linux*) os=linux ;;
    darwin*) os=darwin ;;
    mingw*|msys*|cygwin*) os=windows ;;
esac
case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    armv*) arch=arm ;;
esac

suffix=""
if [[ "$os" == "windows" ]]; then
    suffix=".exe"
fi
MC_BINARY="mc.${os}-${arch}.${MC_VERSION}${suffix}"
CACHED_MC="$CACHE_DIR/$MC_BINARY"

copy_mc() {
    local src=$1
    mkdir -p "$CACHE_DIR"
    if [[ "$src" != "$CACHED_MC" ]]; then
        cp -f "$src" "$CACHED_MC"
        chmod +x "$CACHED_MC"
    fi
    mkdir -p "$(dirname "$dest")"
    cp -f "$CACHED_MC" "$dest"
    chmod +x "$dest"
}

mc_matches_version() {
    local bin=$1
    [[ -x "$bin" ]] && "$bin" --version 2>/dev/null | grep -q "$MC_VERSION"
}

download_mc() {
    local url="https://github.com/minio/mc/releases/download/$MC_VERSION/$MC_BINARY"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$CACHED_MC"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$url" -O "$CACHED_MC"
    else
        return 1
    fi
    chmod +x "$CACHED_MC"
}

extract_mc_from_image() {
    local cid
    if [[ "$os" != "linux" ]]; then
        return 1
    fi
    if ! command -v docker >/dev/null 2>&1 || ! docker pull quay.io/minio/mc; then
        return 1
    fi
    cid=$(docker create quay.io/minio/mc) || return 1
    if docker cp "$cid:/usr/bin/mc" "$CACHED_MC" 2>/dev/null || docker cp "$cid:/bin/mc" "$CACHED_MC" 2>/dev/null; then
        docker rm "$cid" >/dev/null || true
        chmod +x "$CACHED_MC"
        return 0
    fi
    docker rm "$cid" >/dev/null || true
    return 1
}

mkdir -p "$CACHE_DIR"
if mc_matches_version "$dest"; then
    echo "reusing existing mc at $dest"
    exit 0
elif [[ -x "$CACHED_MC" ]]; then
    echo "reusing cached mc ($MC_VERSION)"
elif command -v mc >/dev/null 2>&1 && mc_matches_version "$(command -v mc)"; then
    echo "reusing mc from PATH"
    copy_mc "$(command -v mc)"
    exit 0
else
    echo "installing mc $MC_VERSION"
    if ! download_mc && ! extract_mc_from_image; then
        echo "Failed to install mc from cache, PATH, GitHub releases, or quay.io/minio/mc"
        exit 1
    fi
fi

copy_mc "$CACHED_MC"
