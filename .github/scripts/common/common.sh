#!/bin/bash -e

# Common variables and initialization
init_platform() {
    case "$(uname -s)" in
        Darwin*)    PLATFORM="mac";;
        Linux*)     PLATFORM="linux";;
        *)          PLATFORM="unknown"
    esac

    # Install jq if missing
    if ! command -v jq &> /dev/null; then
        case "$PLATFORM" in
            mac)    brew install jq;;
            linux)  .github/scripts/apt_install.sh jq;;
            *)      echo "Unsupported platform"; exit 1
        esac
    fi
}

# Platform-agnostic functions with internal branching
prepare_test() {
    case "$PLATFORM" in
        mac)
            ./juicefs umount ~/jfs || true
            umount_jfs ~/jfs "$META_URL"
            sleep 1
            python3 .github/scripts/flush_meta.py "$META_URL"
            rm -rf ~/.juicefs/local/myjfs/ || true
            rm -rf ~/.juicefs/cache || true
            ;;
        linux)
            umount_jfs /jfs "$META_URL"
            python3 .github/scripts/flush_meta.py "$META_URL"
            rm -rf /var/jfs/myjfs || true
            rm -rf /var/jfsCache/myjfs || true
            ;;
    esac
}

umount_jfs() {
    local mp=$1
    local meta_url=$2
    [[ -z "$mp" ]] && echo "mount point is empty" && exit 1
    [[ -z "$meta_url" ]] && echo "meta url is empty" && exit 1
    
    echo "umount_jfs $mp $meta_url"
    [[ ! -f "$mp/.config" ]] && return
    
    ls -l "$mp/.config"
    local status_log="status.log"
    ./juicefs status --log-level error "$meta_url" 2>/dev/null | tee "$status_log"
    
    local pids
    pids=$(jq --arg mp "$mp" '.Sessions[] | select(.MountPoint == $mp) | .ProcessID' "$status_log")
    [[ -z "$pids" ]] && cat "$status_log" && echo "pid is empty" && return
    
    echo "umount is $mp, pids are $pids"
    
    for pid in $pids; do
        case "$PLATFORM" in
            mac)
                if mount | grep -q "$mp"; then
                    diskutil unmount "$mp" || umount "$mp"
                fi
                ;;
            linux)
                umount -l "$mp"
                ;;
        esac
    done
    
    for pid in $pids; do
        wait_mount_process_killed "$pid" 60
    done
}

wait_mount_process_killed() {
    local pid=$1
    local wait_seconds=$2
    [[ -z "$pid" ]] && echo "pid is empty" && exit 1
    [[ -z "$wait_seconds" ]] && echo "wait_seconds is empty" && exit 1
    
    echo "waiting for mount process $pid to exit within $wait_seconds seconds"
    for i in $(seq 1 "$wait_seconds"); do
        case "$PLATFORM" in
            mac)
                if ! ps -p "$pid" > /dev/null; then
                    echo "mount process is killed"
                    break
                fi
                ;;
            linux)
                count=$(ps -ef | grep "juicefs mount" | awk '{print $2}' | grep "^$pid$" | wc -l)
                if [ "$count" -eq 0 ]; then
                    echo "mount process is killed"
                    break
                fi
                ;;
        esac
        
        if [ "$i" -eq "$wait_seconds" ]; then
            case "$PLATFORM" in
                mac)    ps -p "$pid";;
                linux)  ps -ef | grep "juicefs mount" | grep -v "grep";;
            esac
            echo "<FATAL>: mount process is not killed after $wait_seconds"
            exit 1
        fi
        sleep 1
    done
}

compare_md5sum() {
    local file1=$1
    local file2=$2
    
    case "$PLATFORM" in
        mac)
            md51=$(md5 -q "$file1")
            md52=$(md5 -q "$file2")
            ;;
        linux)
            md51=$(md5sum "$file1" | awk '{print $1}')
            md52=$(md5sum "$file2" | awk '{print $1}')
            ;;
    esac
    
    if [ "$md51" != "$md52" ]; then
        echo "md5 are different: $file1 ($md51) vs $file2 ($md52)"
        exit 1
    fi
}

wait_command_success() {
    local command=$1
    local expected=$2
    local timeout=${3:-30}
    
    echo "waiting for command success: cmd='$command', expected='$expected', timeout=$timeout"
    for i in $(seq 1 "$timeout"); do
        result=$(eval "$command" 2>/dev/null | tr -d ' ')
        echo "attempt $i: result=$result"
        
        if [[ "$result" == "$expected" ]]; then
            echo "command succeeded"
            return 0
        fi
        
        if [ "$i" -eq "$timeout" ]; then
            eval "$command"
            echo "command failed after $timeout attempts: $command"
            exit 1
        fi
        sleep 1
    done
}

# macOS specific helper (only defined but used when needed)
ensure_directory() {
    [[ "$PLATFORM" != "mac" ]] && return
    local dir=$1
    if [[ ! -d "$dir" ]]; then
        echo "Creating directory: $dir"
        mkdir -p "$dir"
    fi
}

# Initialize platform detection
init_platform

# Make functions available to subprocesses
export -f prepare_test umount_jfs wait_mount_process_killed compare_md5sum wait_command_success ensure_directory
export PLATFORM META_URL