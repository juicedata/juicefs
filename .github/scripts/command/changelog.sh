#!/bin/bash -e
source .github/scripts/common/common.sh

[[ -z "$META" ]] && META=sqlite3
source .github/scripts/start_meta_engine.sh
start_meta_engine $META
META_URL=$(get_meta_url $META)

LOG_DIR=/tmp/changelog-command-test
MOUNT_POINT=/jfs
CHANGELOG_TAIL_PID=

cleanup_artifacts() {
    rm -rf "$LOG_DIR"
    mkdir -p "$LOG_DIR"
    rm -rf /tmp/random-test-log || true
    rm -f /tmp/dump-changelog.json || true
}

cleanup_background_jobs() {
    if [[ -n "${CHANGELOG_TAIL_PID:-}" ]]; then
        kill "$CHANGELOG_TAIL_PID" >/dev/null 2>&1 || true
        wait "$CHANGELOG_TAIL_PID" >/dev/null 2>&1 || true
        CHANGELOG_TAIL_PID=
    fi
}

trap cleanup_background_jobs EXIT

prepare_changelog_test() {
    prepare_test
    cleanup_artifacts
    ./juicefs format "$META_URL" myjfs
}

enable_changelog() {
    ./juicefs config "$META_URL" --changelog
}

mount_jfs() {
    ./juicefs mount -d "$META_URL" "$MOUNT_POINT" --no-usage-report
}

collect_changelog() {
    local from=$1
    local output=$2
    local duration=${3:-8s}
    set +e
    timeout "$duration" ./juicefs changelog --log-level error "$META_URL" --from "$from" > "$output" 2>"${output}.err"
    local st=$?
    set -e
    if [[ $st -ne 0 && $st -ne 124 ]]; then
        cat "${output}.err"
        echo "collect changelog failed with exit code $st"
        exit 1
    fi
    grep -E '^[0-9]+: ' "$output" > "${output}.filtered" || true
    mv "${output}.filtered" "$output"
}

start_changelog_tail() {
    local from=${1:-0}
    local output=$2
    local duration=${3:-12s}
    cleanup_background_jobs
    timeout "$duration" ./juicefs changelog --log-level error "$META_URL" --from "$from" > "$output" 2>"${output}.err" &
    CHANGELOG_TAIL_PID=$!
}

wait_changelog_tail() {
    local output=$1
    if [[ -z "${CHANGELOG_TAIL_PID:-}" ]]; then
        echo "changelog tail process is not started"
        exit 1
    fi
    set +e
    wait "$CHANGELOG_TAIL_PID"
    local st=$?
    set -e
    CHANGELOG_TAIL_PID=
    if [[ $st -ne 0 && $st -ne 124 ]]; then
        cat "${output}.err"
        echo "wait changelog tail failed with exit code $st"
        exit 1
    fi
    grep -E '^[0-9]+: ' "$output" > "${output}.filtered" || true
    mv "${output}.filtered" "$output"
}

full_changelog_from() {
    # SQL/Redis support negative `from` and will return entries with version > from.
    # TiKV uses unsigned ids internally in ScanChangelog, so negative values do not work.
    if [[ "$META" == "tikv" ]]; then
        echo 1
    else
        echo -1
    fi
}

collect_full_changelog() {
    local output=$1
    local duration=${2:-8s}
    collect_changelog "$(full_changelog_from)" "$output" "$duration"
}

show_file_excerpt() {
    local file=$1
    local max_lines=${2:-80}
    if [[ ! -f "$file" ]]; then
        echo "file not found: $file"
        return
    fi
    local lines
    lines=$(wc -l < "$file")
    if (( lines <= max_lines )); then
        cat "$file"
        return
    fi
    local head_lines=$((max_lines / 2))
    local tail_lines=$((max_lines - head_lines))
    echo "--- first ${head_lines} lines of $file ---"
    head -n "$head_lines" "$file"
    echo "--- last ${tail_lines} lines of $file ---"
    tail -n "$tail_lines" "$file"
}

assert_file_contains() {
    local file=$1
    local pattern=$2
    grep -E "$pattern" "$file" >/dev/null || {
        echo "expect '$pattern' in $file"
        show_file_excerpt "$file"
        exit 1
    }
}

assert_file_not_contains() {
    local file=$1
    local pattern=$2
    if grep -E "$pattern" "$file" >/dev/null; then
        echo "unexpected '$pattern' in $file"
        show_file_excerpt "$file"
        exit 1
    fi
}

assert_line_count_ge() {
    local file=$1
    local expected=$2
    local actual
    actual=$(wc -l < "$file")
    if (( actual < expected )); then
        echo "expect at least $expected lines in $file, got $actual"
        show_file_excerpt "$file"
        exit 1
    fi
}

assert_versions_increase() {
    local file=$1
    awk -F': ' '
        BEGIN { prev = -1 }
        /^[0-9]+: / {
            ver = $1 + 0
            if (prev > ver) {
                printf("version out of order: prev=%d current=%d\n", prev, ver)
                exit 1
            }
            prev = ver
        }
    ' "$file"
}

assert_versions_gt() {
    local file=$1
    local lower=$2
    awk -F': ' -v lower="$lower" '
        /^[0-9]+: / {
            ver = $1 + 0
            if (ver <= lower) {
                printf("version %d is not greater than %d\n", ver, lower)
                exit 1
            }
        }
    ' "$file"
}

last_version() {
    local file=$1
    awk -F': ' '/^[0-9]+: / { v = $1 } END { print v + 0 }' "$file"
}

download_random_test_if_needed() {
    [[ -x ./random-test ]] && return
    wget -q https://juicefs-com-static.oss-cn-shanghai.aliyuncs.com/random-test/random-test -O random-test
    chmod +x random-test
}

test_changelog_requires_enable()
{
    prepare_changelog_test
    if ./juicefs changelog --log-level error "$META_URL" > "$LOG_DIR/disabled.out" 2>&1; then
        echo "changelog should fail when feature is disabled"
        exit 1
    fi
    assert_file_contains "$LOG_DIR/disabled.out" 'changelog is not enabled'
}

test_changelog_config_flags()
{
    prepare_changelog_test
    enable_changelog

    ./juicefs config "$META_URL" > "$LOG_DIR/config-enabled.out"
    assert_file_contains "$LOG_DIR/config-enabled.out" '"ChangeLog"[[:space:]]*:[[:space:]]*true'
    assert_file_contains "$LOG_DIR/config-enabled.out" '"ChangeLogMaxAge"[[:space:]]*:[[:space:]]*7200'

    ./juicefs config "$META_URL" --changelog-max-age 60s --changelog-max-lines 123
    ./juicefs config "$META_URL" > "$LOG_DIR/config-updated.out"
    assert_file_contains "$LOG_DIR/config-updated.out" '"ChangeLogMaxAge"[[:space:]]*:[[:space:]]*60'
    assert_file_contains "$LOG_DIR/config-updated.out" '"ChangeLogMaxLines"[[:space:]]*:[[:space:]]*123'

    if ./juicefs config "$META_URL" --changelog-max-age -1s > "$LOG_DIR/neg-age.out" 2>&1; then
        echo "negative changelog max age should fail"
        exit 1
    fi
    assert_file_contains "$LOG_DIR/neg-age.out" 'negative duration'

    if ./juicefs config "$META_URL" --changelog-max-lines -1 > "$LOG_DIR/neg-lines.out" 2>&1; then
        echo "negative changelog max lines should fail"
        exit 1
    fi
    assert_file_contains "$LOG_DIR/neg-lines.out" 'negative value'

    ./juicefs config "$META_URL" --changelog=false
    if ./juicefs changelog --log-level error "$META_URL" > "$LOG_DIR/disabled-again.out" 2>&1; then
        echo "changelog should fail after disabling"
        exit 1
    fi
    assert_file_contains "$LOG_DIR/disabled-again.out" 'changelog is not enabled'
}

test_changelog_records_are_correct()
{
    prepare_changelog_test
    enable_changelog
    mount_jfs

    mkdir -p /jfs/changelog-basic/empty-dir
    echo 'hello-changelog' > /jfs/changelog-basic/plain.txt
    mv /jfs/changelog-basic/plain.txt /jfs/changelog-basic/renamed.txt
    ln /jfs/changelog-basic/renamed.txt /jfs/changelog-basic/hard.txt
    truncate -s 1 /jfs/changelog-basic/renamed.txt
    chmod 640 /jfs/changelog-basic/renamed.txt
    rm /jfs/changelog-basic/hard.txt
    rmdir /jfs/changelog-basic/empty-dir
    sync

    umount_jfs "$MOUNT_POINT" "$META_URL"

    collect_full_changelog "$LOG_DIR/basic.log"
    assert_line_count_ge "$LOG_DIR/basic.log" 8
    assert_file_contains "$LOG_DIR/basic.log" '^[0-9]+: [0-9]+\.[0-9]+\|[A-Z_]+\(.*\)\|\([0-9]+,[0-9]+\)$'
    assert_versions_increase "$LOG_DIR/basic.log"

    assert_file_contains "$LOG_DIR/basic.log" 'CLEANSESSION\('
    assert_file_contains "$LOG_DIR/basic.log" 'CREATE\(.*changelog-basic'
    assert_file_contains "$LOG_DIR/basic.log" 'MOVE\(.*plain.txt.*renamed.txt'
    assert_file_contains "$LOG_DIR/basic.log" 'LINK\(.*hard.txt'
    assert_file_contains "$LOG_DIR/basic.log" 'UNLINK\(.*hard.txt'
    assert_file_contains "$LOG_DIR/basic.log" 'WRITE\('
    assert_file_contains "$LOG_DIR/basic.log" 'TRUNCATE\('
    assert_file_contains "$LOG_DIR/basic.log" 'SETATTR\('
    assert_file_contains "$LOG_DIR/basic.log" 'RMDIR\(.*empty-dir'
}

test_changelog_from_parameter()
{
    prepare_changelog_test
    enable_changelog
    mount_jfs

    mkdir -p /jfs/changelog-from
    echo old > /jfs/changelog-from/old.txt
    sync

    collect_full_changelog "$LOG_DIR/all.log"
    assert_line_count_ge "$LOG_DIR/all.log" 2
    local mid
    mid=$(awk -F': ' 'NR==1 { print $1; exit }' "$LOG_DIR/all.log")
    [[ -z "$mid" ]] && mid=1

    collect_changelog "$mid" "$LOG_DIR/from-mid.log"
    assert_line_count_ge "$LOG_DIR/from-mid.log" 1
    if [[ "$META" != "tikv" ]]; then
        assert_versions_gt "$LOG_DIR/from-mid.log" "$mid"
    fi

    # `juicefs changelog` is a tail command and will keep waiting for new entries,
    # so for live-tail validation we start it in background and stop it via timeout.
    start_changelog_tail 0 "$LOG_DIR/from-latest.log" 12s
    sleep 2
    echo new > /jfs/changelog-from/new.txt
    sync
    wait_changelog_tail "$LOG_DIR/from-latest.log"

    assert_file_contains "$LOG_DIR/from-latest.log" 'new.txt'
    assert_file_not_contains "$LOG_DIR/from-latest.log" 'old.txt'

    umount_jfs "$MOUNT_POINT" "$META_URL"
}

source .github/scripts/common/run_test.sh && run_test "$@"