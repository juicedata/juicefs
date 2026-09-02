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
cleanup_test_mounts() {
    case "$PLATFORM" in
        mac)
            for mp in ~/jfs3 ~/jfs2 ~/jfs; do
                umount_jfs "$mp" "$META_URL"
                ./juicefs umount "$mp" 2>/dev/null || true
            done
            ;;
        linux)
            for mp in /jfs3 /jfs2 /jfs; do
                umount_jfs "$mp" "$META_URL"
                ./juicefs umount "$mp" 2>/dev/null || true
            done
            ;;
    esac
}

prepare_test() {
    case "$PLATFORM" in
        mac)
            cleanup_test_mounts
            sleep 1
            python3 .github/scripts/flush_meta.py "$META_URL"
            rm -rf ~/.juicefs/local/myjfs/ || true
            rm -rf ~/.juicefs/cache || true
            ;;
        linux)
            cleanup_test_mounts
            python3 .github/scripts/flush_meta.py "$META_URL"
            rm -rf /var/jfs/myjfs || true
            rm -rf /var/jfsCache/myjfs || true
            ;;
    esac
}

is_mounted() {
    local mp=$1
    case "$PLATFORM" in
        mac)    mount | grep -qF " on ${mp} " ;;
        linux)  awk -v mp="$mp" '$2 == mp { found = 1 } END { exit !found }' /proc/self/mounts ;;
        *)      return 1 ;;
    esac
}

# `timeout` is GNU coreutils and is absent from a stock macOS runner; fall back
# to gtimeout, then to a watchdog. perl's alarm+exec trick is deliberately not
# used: the Go runtime installs its own SIGALRM handler and silently ignores the
# signal, so it would never interrupt ./juicefs.
run_with_timeout() {
    local secs=$1
    shift
    if command -v timeout >/dev/null 2>&1; then
        timeout "$secs" "$@"
        return $?
    fi
    if command -v gtimeout >/dev/null 2>&1; then
        gtimeout "$secs" "$@"
        return $?
    fi
    "$@" &
    local cmd_pid=$!
    # stdout is redirected so the watchdog never holds the caller's pipe open.
    ( sleep "$secs"; kill -TERM "$cmd_pid" && sleep 2 && kill -KILL "$cmd_pid"; ) >/dev/null 2>&1 &
    local watchdog_pid=$!
    local rc=0
    wait "$cmd_pid" 2>/dev/null || rc=$?
    kill "$watchdog_pid" 2>/dev/null
    wait "$watchdog_pid" 2>/dev/null
    return $rc
}

umount_jfs() {
    local mp=$1
    local meta_url=$2
    [[ -z "$mp" ]] && echo "mount point is empty" && exit 1
    [[ -z "$meta_url" ]] && echo "meta url is empty" && exit 1
    
    echo "umount_jfs $mp $meta_url"
    # Probe the mount table rather than a file inside the mount: when the mount
    # process is gone but its FUSE connection is still alive, any syscall
    # against $mp blocks forever and hangs the whole job (see #7458).
    is_mounted "$mp" || return 0
    
    local status_log="status.log"
    local status_json=""
    if ! status_json=$(run_with_timeout 60 ./juicefs status --log-level error "$meta_url" 2>/dev/null); then
        echo "cannot get status from $meta_url, umount $mp anyway"
    fi
    printf '%s\n' "$status_json" | grep -v '^\[xorm\]' > "$status_log" || true
    cat "$status_log"
    
    local pids
    pids=$(jq --arg mp "$mp" '.Sessions[] | select(.MountPoint == $mp) | .ProcessID' "$status_log" 2>/dev/null) || pids=""
    if [[ -z "$pids" ]]; then
        # $meta_url does not know this mount: it may have been mounted from
        # another volume (/jfs2 is used for both $META_URL2 and test2.db) or the
        # metadata may already be gone. Fall back to the process table, which,
        # unlike the mount point itself, can never block.
        pids=$(ps -eo pid=,args= 2>/dev/null | awk -v mp="$mp" \
            '/juicefs/ { ismount = 0; hasmp = 0
                 for (i = 2; i <= NF; i++) { if ($i == "mount") ismount = 1; if ($i == mp) hasmp = 1 }
                 if (ismount && hasmp) print $1 }')
    fi
    
    echo "umount is $mp, pids are ${pids:-<none>}"
    
    # There may be multiple mounts stacked on the same path (for example, ACL
    # tests remount after changing the volume configuration). Unmount once per
    # session, or at least once when session discovery failed.
    local pid
    for pid in ${pids:-0}; do
        case "$PLATFORM" in
            mac)
                diskutil unmount "$mp" || diskutil unmount force "$mp" || umount -f "$mp" || true
                ;;
            linux)
                umount -l "$mp" || true
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
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "mount process is killed"
            break
        fi
        
        if [ "$i" -eq "$wait_seconds" ]; then
            case "$PLATFORM" in
                mac)    ps -p "$pid";;
                linux)  ps -fp "$pid" 2>/dev/null || true;;
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
export -f cleanup_test_mounts prepare_test is_mounted run_with_timeout umount_jfs wait_mount_process_killed compare_md5sum wait_command_success ensure_directory
export PLATFORM META_URL